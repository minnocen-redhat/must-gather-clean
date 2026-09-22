package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openshift/must-gather-clean/pkg/cleaner"
	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
	"github.com/openshift/must-gather-clean/pkg/fsutil"
	"github.com/openshift/must-gather-clean/pkg/obfuscator"
	"github.com/openshift/must-gather-clean/pkg/omitter"
	"github.com/openshift/must-gather-clean/pkg/reporting"
	"github.com/openshift/must-gather-clean/pkg/schema"
	"github.com/openshift/must-gather-clean/pkg/traversal"
	watermarking "github.com/openshift/must-gather-clean/pkg/watermarker"
	"k8s.io/klog/v2"
)

const (
	reportFileName            = "report.yaml"
	versionedReportNamePrefix = "report-"
	versionedReportNameSuffix = ".yaml"
)

// RunOptions controls optional directory-cleaning behavior. Keeping these
// options in a struct makes adding future flags possible without changing the
// public RunWithOptions signature again.
type RunOptions struct {
	DeleteOutputFolder bool
	// ReportingFolder is the directory where reports are written. In the
	// response-aware workflow it also stores the immutable per-run reports.
	ReportingFolder string
	WorkerCount     int
	// Reversible enables run-scoped tokens and report-based restoration of
	// unchanged tokens in support responses.
	Reversible bool
}

func versionedReportNameForRun(runID string) string {
	return versionedReportNamePrefix + runID + versionedReportNameSuffix
}

func RunPipe(configPath string, stdin io.Reader, stdout io.Writer) error {
	return RunPipeWithOptions(configPath, stdin, stdout, false)
}

func RunPipeWithOptions(configPath string, stdin io.Reader, stdout io.Writer, reversible bool) error {
	if reversible {
		return fmt.Errorf("reversible workflow is unavailable: pipe-mode does not produce a versioned report")
	}

	var multiObfuscator *obfuscator.MultiObfuscator
	if configPath != "" {
		config, err := schema.ReadConfigFromPath(configPath)
		if err != nil {
			return fmt.Errorf("failed to read config at %s: %w", configPath, err)
		}
		// we cannot logically prescan because the end of input isn't clear
		multiObfuscator, _, err = createObfuscatorsFromConfig(config)
		if err != nil {
			return fmt.Errorf("failed to create obfuscators via config at %s: %w", configPath, err)
		}
	} else {
		ipObfuscator, err := obfuscator.NewIPObfuscator(schema.ObfuscateReplacementTypeConsistent, obfuscator.NewSimpleTracker())
		if err != nil {
			return fmt.Errorf("failed to create IP obfuscator: %w", err)
		}

		macObfuscator, err := obfuscator.NewMacAddressObfuscator(schema.ObfuscateReplacementTypeConsistent, obfuscator.NewSimpleTracker())
		if err != nil {
			return fmt.Errorf("failed to create MAC obfuscator: %w", err)
		}

		multiObfuscator = obfuscator.NewMultiObfuscator([]obfuscator.ReportingObfuscator{
			ipObfuscator,
			macObfuscator,
		})
	}

	contentObfuscator := cleaner.ContentObfuscator{Obfuscator: multiObfuscator}
	err := contentObfuscator.ObfuscateReader(stdin, stdout)
	if err != nil {
		return fmt.Errorf("failed to obfuscate via pipe: %w", err)
	}

	return nil
}

func Run(configPath string, inputPath string, outputPath string, deleteOutputFolder bool, reportingFolder string, workerCount int) error {
	return runLegacy(configPath, inputPath, outputPath, deleteOutputFolder, reportingFolder, workerCount)
}

func RunWithOptions(configPath string, inputPath string, outputPath string, options RunOptions) error {
	if !options.Reversible {
		return runLegacy(configPath, inputPath, outputPath, options.DeleteOutputFolder, options.ReportingFolder, options.WorkerCount)
	}
	reportingFolder := options.ReportingFolder
	if reportingFolder == "" {
		reportingFolder = "."
	}
	return runWithResponseDeobfuscation(configPath, inputPath, outputPath, options.DeleteOutputFolder, reportingFolder, options.WorkerCount)
}

func runWithResponseDeobfuscation(configPath string, inputPath string, outputPath string, deleteOutputFolder bool, reportingFolder string, workerCount int) (runErr error) {
	if workerCount < 1 {
		return fmt.Errorf("invalid number of workers specified %d", workerCount)
	}
	if _, err := os.Stat(inputPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("input folder does not exist: %w", err)
		}
		return fmt.Errorf("failed to stat input folder: %w", err)
	}

	config, err := schema.ReadConfigFromPath(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config at %s: %w", configPath, err)
	}

	alreadyCleaned, err := inputHasCleaningWatermark(inputPath)
	if err != nil {
		return err
	}
	capability := deobfuscator.EvaluateCapability(alreadyCleaned, false)
	if !capability.Available(deobfuscator.ScopeResponse) {
		return fmt.Errorf("reversible workflow is unavailable (%s); fix the configuration or use a suitable original input", strings.Join(capability.ResponseReasons, ", "))
	}
	klog.Infof("Deobfuscation: AVAILABLE for support responses")
	runID, err := deobfuscator.NewRunID()
	if err != nil {
		return err
	}
	versionedReportName := versionedReportNameForRun(runID)

	outputTransaction, err := fsutil.BeginOutputTransaction(inputPath, outputPath, deleteOutputFolder)
	if err != nil {
		return err
	}
	defer func() { _ = outputTransaction.Cleanup() }()
	if reportingFolder == "" {
		reportingFolder = "."
	}
	artifactDirectory, err := ensureArtifactsOutsideInputOutput(reportingFolder, inputPath, outputTransaction.FinalPath, versionedReportName)
	if err != nil {
		return err
	}
	if artifactDirectory, err = prepareReportingFolder(artifactDirectory); err != nil {
		return err
	}
	if err := preserveExistingReport(artifactDirectory); err != nil {
		return err
	}
	artifactTransaction, err := newArtifactTransaction(artifactDirectory)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := artifactTransaction.Rollback(); rollbackErr != nil {
			rollbackErr = fmt.Errorf("failed to roll back reports: %w", rollbackErr)
			if runErr == nil {
				runErr = rollbackErr
			} else {
				runErr = errorsJoin(runErr, rollbackErr)
			}
		}
	}()

	// Keep the full run ID in the report, but use a shorter 96-bit tag in
	// every token to limit path and prompt growth.
	tokenPrefix := "x-mgc1-" + runID[:24] + "-"

	mro, err := createResponseOmittersFromConfig(config, inputPath)
	if err != nil {
		return fmt.Errorf("failed to create omitters via config at %s: %w", configPath, err)
	}
	prescanOmitter, err := createPrescanOmitterFromConfig(config, inputPath)
	if err != nil {
		return fmt.Errorf("failed to create prescan omitters via config at %s: %w", configPath, err)
	}

	obfuscator, prescanObfuscator, err := createObfuscatorsFromConfigWithOptions(config, tokenPrefix)
	if err != nil {
		return fmt.Errorf("failed to create obfuscators via config at %s: %w", configPath, err)
	}

	// this pass allows obfuscators that first need to scan the input to determine what needs to be obfuscated to run before
	// redactor actually happens. The empty input path signals a dry-run.
	prescanCleaner := cleaner.NewFileCleaner(inputPath, "", prescanObfuscator, prescanOmitter)
	prescanWorkerFactory := func(id int) traversal.QueueProcessor {
		return traversal.NewWorker(id, prescanCleaner)
	}
	if err := traversal.NewParallelFileWalker(inputPath, workerCount, prescanWorkerFactory).TraverseWithError(); err != nil {
		return fmt.Errorf("failed during obfuscator prescan: %w", err)
	}

	fileCleaner := cleaner.NewFileCleaner(inputPath, outputTransaction.StagingPath, obfuscator, mro)

	workerFactory := func(id int) traversal.QueueProcessor {
		return traversal.NewWorker(id, fileCleaner)
	}
	if err := traversal.NewParallelFileWalker(inputPath, workerCount, workerFactory).TraverseWithError(); err != nil {
		return fmt.Errorf("failed during cleaning: %w", err)
	}

	reporter := reporting.NewSimpleReporterWithRunID(config, runID)
	reporter.CollectOmitterReport(mro.Report())
	obfuscatorReports := obfuscator.ReportPerObfuscator()
	reporter.CollectObfuscatorReport(obfuscatorReports)

	reporterErr := reporter.WriteReport(artifactTransaction.Stage(reportFileName))
	if reporterErr != nil {
		return reporterErr
	}
	if err := reporter.WriteReport(artifactTransaction.Stage(versionedReportName)); err != nil {
		return err
	}
	watermarker := watermarking.NewSimpleWaterMarker()
	if err := watermarker.WriteWaterMarkFile(outputTransaction.StagingPath); err != nil {
		return err
	}

	if err := artifactTransaction.Publish(true, versionedReportName); err != nil {
		return err
	}
	if err := outputTransaction.Commit(); err != nil {
		return err
	}
	if err := artifactTransaction.Finalize(); err != nil {
		klog.Warningf("cleaning completed, but report artifact cleanup failed: %v", err)
	}
	klog.Infof("Cleaning completed. Deobfuscation: AVAILABLE for support responses; report: %s", filepath.Join(artifactDirectory, versionedReportName))
	return nil
}

// runLegacy preserves the pre-deobfuscation directory workflow. In
// particular, it does not inspect or create manifests, impose reporting-path
// restrictions, or stage artifacts through the reversible workflow.
func runLegacy(configPath string, inputPath string, outputPath string, deleteOutputFolder bool, reportingFolder string, workerCount int) error {
	if workerCount < 1 {
		return fmt.Errorf("invalid number of workers specified %d", workerCount)
	}

	if err := fsutil.EnsureInputOutputPath(inputPath, outputPath, deleteOutputFolder); err != nil {
		return err
	}

	config, err := schema.ReadConfigFromPath(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config at %s: %w", configPath, err)
	}

	configuredObfuscator, prescanObfuscator, err := createObfuscatorsFromConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create obfuscators via config at %s: %w", configPath, err)
	}

	prescanCleaner := cleaner.NewFileCleaner(inputPath, "", prescanObfuscator, &omitter.NoopOmitter{})
	prescanWorkerFactory := func(id int) traversal.QueueProcessor {
		return traversal.NewWorker(id, prescanCleaner)
	}
	traversal.NewParallelFileWalker(inputPath, workerCount, prescanWorkerFactory).Traverse()

	mro, err := createOmittersFromConfig(config, inputPath)
	if err != nil {
		return fmt.Errorf("failed to create omitters via config at %s: %w", configPath, err)
	}
	fileCleaner := cleaner.NewFileCleaner(inputPath, outputPath, configuredObfuscator, mro)
	workerFactory := func(id int) traversal.QueueProcessor {
		return traversal.NewWorker(id, fileCleaner)
	}
	traversal.NewParallelFileWalker(inputPath, workerCount, workerFactory).Traverse()

	reporter := reporting.NewSimpleReporter(config)
	reporter.CollectOmitterReport(mro.Report())
	reporter.CollectObfuscatorReport(configuredObfuscator.ReportPerObfuscator())
	if err := reporter.WriteReport(filepath.Join(reportingFolder, reportFileName)); err != nil {
		return err
	}

	watermarker := watermarking.NewSimpleWaterMarker()
	return watermarker.WriteWaterMarkFile(outputPath)
}

// prepareReportingFolder keeps the response-aware workflow on the same
// reporting path model as the legacy workflow. Historical per-run reports are
// retained there.
func prepareReportingFolder(path string) (string, error) {
	if path == "" {
		path = "."
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("failed to create reporting folder: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("failed to inspect reporting folder: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("reporting path %s is not a directory", path)
	}
	return filepath.Abs(path)
}

// preserveExistingReport prevents a response-aware run from destroying the
// only copy of a report produced by an older run. Reports that already carry a
// run ID keep the normal versioned name; legacy reports get a timestamped
// history name because they have no run ID to reuse.
func preserveExistingReport(directory string) error {
	latestPath := filepath.Join(directory, reportFileName)
	info, err := os.Stat(latestPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect existing report: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("existing report %s is not a regular file", latestPath)
	}

	data, err := os.ReadFile(latestPath)
	if err != nil {
		return fmt.Errorf("failed to read existing report: %w", err)
	}
	existing, readErr := reporting.ReadReport(latestPath)
	name := "report-legacy-" + time.Now().UTC().Format("20060102T150405.000000000Z") + versionedReportNameSuffix
	if readErr == nil && existing.RunID != "" {
		name = versionedReportNameForRun(existing.RunID)
	}

	for attempt := 0; ; attempt++ {
		candidate := name
		if attempt > 0 {
			candidate = strings.TrimSuffix(name, versionedReportNameSuffix) + fmt.Sprintf("-%d", attempt) + versionedReportNameSuffix
		}
		target := filepath.Join(directory, candidate)
		output, createErr := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if os.IsExist(createErr) {
			if previous, readPreviousErr := os.ReadFile(target); readPreviousErr == nil && bytes.Equal(previous, data) {
				return nil
			}
			continue
		}
		if createErr != nil {
			return fmt.Errorf("failed to preserve existing report: %w", createErr)
		}
		if _, writeErr := output.Write(data); writeErr != nil {
			_ = output.Close()
			_ = os.Remove(target)
			return fmt.Errorf("failed to preserve existing report: %w", writeErr)
		}
		if syncErr := output.Sync(); syncErr != nil {
			_ = output.Close()
			_ = os.Remove(target)
			return fmt.Errorf("failed to sync preserved report: %w", syncErr)
		}
		if closeErr := output.Close(); closeErr != nil {
			_ = os.Remove(target)
			return fmt.Errorf("failed to close preserved report: %w", closeErr)
		}
		return nil
	}
}

func ensureArtifactsOutsideInputOutput(reportingFolder, inputPath, outputPath, versionedReportName string) (string, error) {
	inputResolved, err := fsutil.ResolvePathForComparison(inputPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve input path: %w", err)
	}
	outputResolved, err := fsutil.ResolvePathForComparison(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve output path: %w", err)
	}
	reportingResolved, err := fsutil.ResolvePathForComparison(reportingFolder)
	if err != nil {
		return "", fmt.Errorf("failed to resolve reporting folder: %w", err)
	}
	if fsutil.IsPathWithin(inputResolved, reportingResolved) {
		return "", fmt.Errorf("reporting folder %s must be outside input directory %s", reportingFolder, inputPath)
	}
	if fsutil.IsPathWithin(outputResolved, reportingResolved) {
		return "", fmt.Errorf("reporting folder %s must be outside cleaned output directory %s", reportingFolder, outputPath)
	}
	for _, name := range []string{reportFileName, versionedReportName} {
		artifactPath := filepath.Join(reportingResolved, name)
		artifactResolved, err := fsutil.ResolvePathForComparison(artifactPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve report %s: %w", name, err)
		}
		if fsutil.IsPathWithin(inputResolved, artifactResolved) {
			return "", fmt.Errorf("report %s must be outside input directory %s", artifactPath, inputPath)
		}
		if fsutil.IsPathWithin(outputResolved, artifactResolved) {
			return "", fmt.Errorf("report %s must be outside cleaned output directory %s", artifactPath, outputPath)
		}
	}
	return reportingResolved, nil
}

// inputHasCleaningWatermark identifies output produced by this tool without
// introducing a public manifest into the cleaned must-gather. Such input is
// not suitable for a new response-restoration report because its existing
// run-scoped tokens belong to an earlier run.
func inputHasCleaningWatermark(inputPath string) (bool, error) {
	return watermarking.IsValidWatermarkFile(filepath.Join(inputPath, "watermark.txt"))
}

func createOmittersFromConfig(config *schema.SchemaJson, inputPath string) (omitter.ReportingOmitter, error) {
	var fileOmitters []omitter.FileOmitter
	var k8sOmitters []omitter.KubernetesResourceOmitter
	for _, o := range config.Config.Omit {
		switch o.Type {
		case schema.OmitTypeSymbolicLink:
			fileOmitters = append(fileOmitters, omitter.NewSymlinkOmitter(inputPath))
		case schema.OmitTypeFile:
			om, err := omitter.NewFilenamePatternOmitter(*o.Pattern)
			if err != nil {
				return nil, err
			}
			fileOmitters = append(fileOmitters, om)
		case schema.OmitTypeKubernetes:
			if o.KubernetesResource == nil {
				klog.Exitf("type Kubernetes must also include a 'kubernetesResource'. Given: %v", o)
			}
			kr := *o.KubernetesResource
			om, err := omitter.NewKubernetesResourceOmitter(kr.ApiVersion, kr.Kind, kr.Namespaces)
			if err != nil {
				return nil, err
			}
			k8sOmitters = append(k8sOmitters, om)
		}
	}

	return omitter.NewMultiReportingOmitter(fileOmitters, k8sOmitters), nil
}

func createPrescanOmitterFromConfig(config *schema.SchemaJson, inputPath string) (omitter.Omitter, error) {
	fileOmitters, k8sOmitters, err := buildOmittersFromConfig(config, inputPath)
	if err != nil {
		return nil, err
	}
	return omitter.NewMultiOmitter(fileOmitters, k8sOmitters), nil
}

func createResponseOmittersFromConfig(config *schema.SchemaJson, inputPath string) (omitter.ReportingOmitter, error) {
	fileOmitters, k8sOmitters, err := buildOmittersFromConfig(config, inputPath)
	if err != nil {
		return nil, err
	}
	return omitter.NewMultiReportingOmitter(fileOmitters, k8sOmitters), nil
}

func buildOmittersFromConfig(config *schema.SchemaJson, inputPath string) ([]omitter.FileOmitter, []omitter.KubernetesResourceOmitter, error) {
	var fileOmitters []omitter.FileOmitter
	var k8sOmitters []omitter.KubernetesResourceOmitter
	for _, o := range config.Config.Omit {
		switch o.Type {
		case schema.OmitTypeSymbolicLink:
			fileOmitters = append(fileOmitters, omitter.NewSymlinkOmitter(inputPath))
		case schema.OmitTypeFile:
			om, err := omitter.NewFilenamePatternOmitter(*o.Pattern)
			if err != nil {
				return nil, nil, err
			}
			fileOmitters = append(fileOmitters, om)
		case schema.OmitTypeKubernetes:
			if o.KubernetesResource == nil {
				return nil, nil, fmt.Errorf("type Kubernetes must also include a 'kubernetesResource'. Given: %v", o)
			}
			kr := *o.KubernetesResource
			om, err := omitter.NewKubernetesResourceOmitter(kr.ApiVersion, kr.Kind, kr.Namespaces)
			if err != nil {
				return nil, nil, err
			}
			k8sOmitters = append(k8sOmitters, om)
		}
	}

	return fileOmitters, k8sOmitters, nil
}

// finalObfuscator is the obfuscator to use to actually clean a directory.
// prescanObfuscator is an obfuscator that shares some instances of individual obfuscators with the finalObfuscator, but is run in
// a dryRun mode (no output directory) to pre-scan the input and determine the full set of strings to elide.  This allows for
// usage patterns like:
//
//	file/B (exact name unknown) may contain strings like /subscription/ID, where ID needs to be redacted in all files,
//	but file/A contains only ID.  We won't recognize ID as needing redaction until we read file/B.  This means we need to first
//	scan all files, then redact.
func createObfuscatorsFromConfig(config *schema.SchemaJson) (finalObfuscator *obfuscator.MultiObfuscator, prescanObfuscator *obfuscator.MultiObfuscator, finalErr error) {
	return buildObfuscatorsFromConfig(config, "", true)
}

func createObfuscatorsFromConfigWithOptions(config *schema.SchemaJson, tokenPrefix string) (finalObfuscator *obfuscator.MultiObfuscator, prescanObfuscator *obfuscator.MultiObfuscator, finalErr error) {
	return buildObfuscatorsFromConfig(config, tokenPrefix, false)
}

func buildObfuscatorsFromConfig(config *schema.SchemaJson, tokenPrefix string, prescanAllTargets bool) (finalObfuscator *obfuscator.MultiObfuscator, prescanObfuscator *obfuscator.MultiObfuscator, finalErr error) {
	var obfuscators []obfuscator.NamedReportingObfuscator
	var prescanObfuscators []obfuscator.ReportingObfuscator
	options := obfuscator.BuildOptions{
		TokenPrefix:       tokenPrefix,
		RandSeed:          config.Config.RandSeed,
		PrescanAllTargets: prescanAllTargets,
	}
	for index, value := range config.Config.Obfuscate {
		entryOptions := options
		if tokenPrefix != "" {
			entryOptions.TokenPrefix = fmt.Sprintf("%so%d-", tokenPrefix, index+1)
		}
		configured, err := obfuscator.BuildConfiguredObfuscator(value, entryOptions)
		if err != nil {
			return nil, nil, err
		}
		obfuscators = append(obfuscators, obfuscator.NamedReportingObfuscator{
			Type:       configured.Type,
			Obfuscator: configured.Final,
			Reversible: configured.Reversible,
		})
		if configured.Prescan != nil {
			prescanObfuscators = append(prescanObfuscators, configured.Prescan)
		}
	}
	return obfuscator.NewNamedMultiObfuscator(obfuscators), obfuscator.NewMultiObfuscator(prescanObfuscators), nil
}
