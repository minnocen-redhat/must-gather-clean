package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openshift/must-gather-clean/pkg/cleaner"
	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
	"github.com/openshift/must-gather-clean/pkg/fsutil"
	"github.com/openshift/must-gather-clean/pkg/manifest"
	"github.com/openshift/must-gather-clean/pkg/obfuscator"
	"github.com/openshift/must-gather-clean/pkg/omitter"
	"github.com/openshift/must-gather-clean/pkg/reporting"
	"github.com/openshift/must-gather-clean/pkg/schema"
	"github.com/openshift/must-gather-clean/pkg/traversal"
	watermarking "github.com/openshift/must-gather-clean/pkg/watermarker"
	"k8s.io/klog/v2"
)

const (
	reportFileName       = "report.yaml"
	deobfuscationMapName = manifest.PrivateMapFileName
)

func RunPipe(configPath string, stdin io.Reader, stdout io.Writer) error {
	return RunPipeWithOptions(configPath, stdin, stdout, "")
}

func RunPipeWithOptions(configPath string, stdin io.Reader, stdout io.Writer, requiredScope string) error {
	if requiredScope != "" {
		return fmt.Errorf("deobfuscation is required but unavailable: pipe-mode does not produce a private map")
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
	return RunWithOptions(configPath, inputPath, outputPath, deleteOutputFolder, reportingFolder, workerCount, "")
}

func RunWithOptions(configPath string, inputPath string, outputPath string, deleteOutputFolder bool, reportingFolder string, workerCount int, requiredScope string) error {
	if workerCount < 1 {
		return fmt.Errorf("invalid number of workers specified %d", workerCount)
	}
	if _, err := os.Stat(inputPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("input folder does not exist: %w", err)
		}
		return fmt.Errorf("failed to stat input folder: %w", err)
	}

	required, err := requiredDeobfuscationScope(requiredScope)
	if err != nil {
		return err
	}

	config, err := schema.ReadConfigFromPath(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config at %s: %w", configPath, err)
	}

	_, manifestErr := manifest.Read(inputPath)
	alreadyCleaned := manifestErr == nil
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		return fmt.Errorf("input contains an invalid must-gather-clean manifest: %w", manifestErr)
	}
	capability := deobfuscator.EvaluateCapability(config.Config, alreadyCleaned, false)
	if required != "" && !capability.Available(required) {
		return fmt.Errorf("deobfuscation is required but unavailable (%s); fix the configuration or use a suitable original input", strings.Join(capability.ResponseReasons, ", "))
	}
	if capability.ResponseAvailable {
		klog.Infof("Deobfuscation: AVAILABLE for support responses")
	} else {
		klog.Warningf("Deobfuscation: UNAVAILABLE (%s)", strings.Join(capability.ResponseReasons, ", "))
	}

	outputTransaction, err := fsutil.BeginOutputTransaction(inputPath, outputPath, deleteOutputFolder)
	if err != nil {
		return err
	}
	defer func() { _ = outputTransaction.Cleanup() }()
	if err := ensureArtifactsOutsideInputOutput(reportingFolder, inputPath, outputTransaction.FinalPath); err != nil {
		return err
	}

	reportingAbsolute, err := filepath.Abs(reportingFolder)
	if err != nil {
		return fmt.Errorf("failed to resolve reporting folder: %w", err)
	}
	artifactDirectory := reportingAbsolute
	if !capability.ResponseAvailable {
		relative, relativeErr := filepath.Rel(outputTransaction.FinalPath, reportingAbsolute)
		if relativeErr == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))) {
			artifactDirectory = filepath.Join(outputTransaction.StagingPath, relative)
		}
	}
	if err := os.MkdirAll(artifactDirectory, 0700); err != nil {
		return fmt.Errorf("failed to create reporting folder: %w", err)
	}
	artifactTransaction, err := newArtifactTransaction(artifactDirectory)
	if err != nil {
		return err
	}
	defer func() { _ = artifactTransaction.Rollback() }()

	runID := ""
	tokenPrefix := ""
	if capability.ResponseAvailable {
		runID, err = deobfuscator.NewRunID()
		if err != nil {
			return err
		}
		tokenPrefix = "x-mgc-v1-" + runID + "-"
	}

	mro, err := createOmittersFromConfig(config, inputPath)
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
	if err := traversal.NewParallelFileWalker(inputPath, workerCount, prescanWorkerFactory).Traverse(); err != nil {
		return fmt.Errorf("failed during obfuscator prescan: %w", err)
	}

	fileCleaner := cleaner.NewFileCleaner(inputPath, outputTransaction.StagingPath, obfuscator, mro)

	workerFactory := func(id int) traversal.QueueProcessor {
		return traversal.NewWorker(id, fileCleaner)
	}
	if err := traversal.NewParallelFileWalker(inputPath, workerCount, workerFactory).Traverse(); err != nil {
		return fmt.Errorf("failed during cleaning: %w", err)
	}

	reporter := reporting.NewSimpleReporter(config)
	reporter.CollectOmitterReport(mro.Report())
	obfuscatorReports := obfuscator.ReportPerObfuscator()
	reporter.CollectObfuscatorReport(obfuscatorReports)

	var privateMap *deobfuscator.Map
	if capability.ResponseAvailable {
		reversibleReports := obfuscator.ReversibleReports()
		privateMap, err = deobfuscator.NewMapFromLedger(reversibleReports, runID)
		if err != nil {
			return err
		}
		if len(privateMap.Ambiguous) > 0 || len(privateMap.Unsupported) > 0 {
			return fmt.Errorf("deobfuscation ledger is incomplete (%d ambiguous, %d unsupported mappings); no cleaned output was published", len(privateMap.Ambiguous), len(privateMap.Unsupported))
		} else if err := privateMap.Write(artifactTransaction.Stage(deobfuscationMapName)); err != nil {
			return err
		}
	}

	reporterErr := reporter.WriteReport(artifactTransaction.Stage(reportFileName))
	if reporterErr != nil {
		return reporterErr
	}

	watermarker := watermarking.NewSimpleWaterMarker()
	if err := watermarker.WriteWaterMarkFile(outputTransaction.StagingPath); err != nil {
		return err
	}

	completedManifest, err := manifest.New(configPath, runID, capability)
	if err != nil {
		return err
	}
	if err := completedManifest.Write(outputTransaction.StagingPath); err != nil {
		return err
	}
	if err := artifactTransaction.Publish(capability.ResponseAvailable); err != nil {
		return err
	}
	if err := outputTransaction.Commit(); err != nil {
		return err
	}
	if err := artifactTransaction.Finalize(); err != nil {
		return err
	}
	if capability.ResponseAvailable {
		klog.Infof("Cleaning completed. Deobfuscation: AVAILABLE for support responses")
	} else {
		klog.Warningf("Cleaning completed. Deobfuscation: UNAVAILABLE (%s)", strings.Join(capability.ResponseReasons, ", "))
	}
	return nil
}

func ensureArtifactsOutsideInputOutput(reportingFolder, inputPath, outputPath string) error {
	inputResolved, err := fsutil.ResolvePathForComparison(inputPath)
	if err != nil {
		return fmt.Errorf("failed to resolve input path: %w", err)
	}
	outputResolved, err := fsutil.ResolvePathForComparison(outputPath)
	if err != nil {
		return fmt.Errorf("failed to resolve output path: %w", err)
	}
	for _, name := range []string{reportFileName, deobfuscationMapName} {
		artifactPath := filepath.Join(reportingFolder, name)
		artifactResolved, err := fsutil.ResolvePathForComparison(artifactPath)
		if err != nil {
			return fmt.Errorf("failed to resolve reporting artifact %s: %w", name, err)
		}
		if fsutil.IsPathWithin(inputResolved, artifactResolved) {
			return fmt.Errorf("reporting artifact %s must be outside input directory %s", artifactPath, inputPath)
		}
		if fsutil.IsPathWithin(outputResolved, artifactResolved) {
			return fmt.Errorf("reporting artifact %s must be outside cleaned output directory %s", artifactPath, outputPath)
		}
	}
	return nil
}

func requiredDeobfuscationScope(value string) (deobfuscator.Scope, error) {
	switch value {
	case "":
		return "", nil
	case string(deobfuscator.ScopeResponse):
		return deobfuscator.ScopeResponse, nil
	default:
		return "", fmt.Errorf("invalid --require-deobfuscation value %q, expected response", value)
	}
}

func createOmittersFromConfig(config *schema.SchemaJson, inputPath string) (omitter.ReportingOmitter, error) {
	fileOmitters, k8sOmitters, err := buildOmittersFromConfig(config, inputPath)
	if err != nil {
		return nil, err
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
	return createObfuscatorsFromConfigWithOptions(config, "")
}

func createObfuscatorsFromConfigWithOptions(config *schema.SchemaJson, tokenPrefix string) (finalObfuscator *obfuscator.MultiObfuscator, prescanObfuscator *obfuscator.MultiObfuscator, finalErr error) {
	var obfuscators []obfuscator.NamedReportingObfuscator
	var prescanObfuscators []obfuscator.ReportingObfuscator
	options := obfuscator.BuildOptions{
		TokenPrefix: tokenPrefix,
		RandSeed:    config.Config.RandSeed,
	}
	for index, value := range config.Config.Obfuscate {
		entryOptions := options
		if tokenPrefix != "" {
			entryOptions.TokenPrefix = fmt.Sprintf("%so%04d-", tokenPrefix, index+1)
		}
		configured, err := obfuscator.BuildConfiguredObfuscator(value, entryOptions)
		if err != nil {
			return nil, nil, err
		}
		obfuscators = append(obfuscators, obfuscator.NamedReportingObfuscator{
			Type:       configured.Type,
			Obfuscator: configured.Final,
		})
		if configured.Prescan != nil {
			prescanObfuscators = append(prescanObfuscators, configured.Prescan)
		}
	}
	return obfuscator.NewNamedMultiObfuscator(obfuscators), obfuscator.NewMultiObfuscator(prescanObfuscators), nil
}
