package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openshift/must-gather-clean/pkg/cleaner"
	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
	"github.com/openshift/must-gather-clean/pkg/fsutil"
	resourcehostname "github.com/openshift/must-gather-clean/pkg/hostname"
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
	capability := deobfuscator.EvaluateCapability(config.Config, alreadyCleaned, false)
	if required != "" && !capability.Available(required) {
		reasons := capability.ResponseReasons
		if required == deobfuscator.ScopeComplete {
			reasons = capability.CompleteReasons
		}
		return fmt.Errorf("deobfuscation is required but unavailable (%s); fix the configuration or use a suitable original input", strings.Join(reasons, ", "))
	}
	knownHostnames := []string{}
	needsHostnameDiscovery := false
	for _, entry := range config.Config.Obfuscate {
		if entry.Type == schema.ObfuscateTypeHostname {
			needsHostnameDiscovery = true
		}
	}
	if needsHostnameDiscovery {
		discovered, discoverErr := resourcehostname.Discover(inputPath)
		if discoverErr != nil {
			if required != "" {
				return fmt.Errorf("deobfuscation is required but hostname discovery failed: %w", discoverErr)
			}
			capability.ResponseAvailable = false
			capability.CompleteAvailable = false
			capability.ResponseReasons = append(capability.ResponseReasons, "hostname-discovery-failed")
			capability.CompleteReasons = append(capability.CompleteReasons, "hostname-discovery-failed")
		} else {
			knownHostnames = append(knownHostnames, discovered...)
		}
	}
	if capability.ResponseAvailable {
		if err := ensurePrivateMapOutsideOutput(reportingFolder, outputPath); err != nil {
			return err
		}
	}
	if capability.ResponseAvailable {
		klog.Infof("Deobfuscation: AVAILABLE for support responses")
	} else {
		klog.Warningf("Deobfuscation: UNAVAILABLE (%s)", strings.Join(capability.ResponseReasons, ", "))
	}
	if !capability.CompleteAvailable {
		klog.Infof("Complete must-gather recovery: UNAVAILABLE (%s)", strings.Join(capability.CompleteReasons, ", "))
	}

	cleanupOutputOnFailure := outputNeedsCleanup(outputPath)
	err = fsutil.EnsureInputOutputPath(inputPath, outputPath, deleteOutputFolder)
	if err != nil {
		return err
	}

	runID := ""
	runSecret := ""
	tokenPrefix := ""
	if capability.ResponseAvailable {
		runID, err = deobfuscator.NewRunID()
		if err != nil {
			return err
		}
		runSecret, err = deobfuscator.NewRunSecret()
		if err != nil {
			return err
		}
		tokenPrefix = "x-mgc-v1-" + runID + "-"
	}

	obfuscator, prescanObfuscator, err := createObfuscatorsFromConfigWithOptions(config, tokenPrefix, runSecret, knownHostnames)
	if err != nil {
		return fmt.Errorf("failed to create obfuscators via config at %s: %w", configPath, err)
	}

	// this pass allows obfuscators that first need to scan the input to determine what needs to be obfuscated to run before
	// redactor actually happens. The empty input path signals a dry-run.
	prescanCleaner := cleaner.NewFileCleaner(inputPath, "", prescanObfuscator, &omitter.NoopOmitter{})
	prescanWorkerFactory := func(id int) traversal.QueueProcessor {
		return traversal.NewWorker(id, prescanCleaner)
	}
	traversal.NewParallelFileWalker(inputPath, workerCount, prescanWorkerFactory).Traverse()

	mro, err := createOmittersFromConfig(config, inputPath)
	if err != nil {
		return fmt.Errorf("failed to create omitters via config at %s: %w", configPath, err)
	}
	fileCleaner := cleaner.NewFileCleaner(inputPath, outputPath, obfuscator, mro)

	workerFactory := func(id int) traversal.QueueProcessor {
		return traversal.NewWorker(id, fileCleaner)
	}
	traversal.NewParallelFileWalker(inputPath, workerCount, workerFactory).Traverse()

	reporter := reporting.NewSimpleReporter(config)
	reporter.CollectOmitterReport(mro.Report())
	obfuscatorReports := obfuscator.ReportPerObfuscator()
	reporter.CollectObfuscatorReport(obfuscatorReports)

	var privateMap *deobfuscator.Map
	if capability.ResponseAvailable {
		reversibleReports := obfuscator.ReversibleReports()
		privateMap, err = deobfuscator.NewMapFromLedger(config.Config, reversibleReports, runID)
		if err != nil {
			return err
		}
		if len(privateMap.Ambiguous) > 0 || len(privateMap.Unsupported) > 0 {
			if required != "" {
				cleanupErr := cleanupOutputAfterRequiredFailure(outputPath, cleanupOutputOnFailure)
				if cleanupErr != nil {
					return fmt.Errorf("deobfuscation is required but the generated ledger is incomplete (%d ambiguous, %d unsupported mappings); additionally failed to clean output: %w", len(privateMap.Ambiguous), len(privateMap.Unsupported), cleanupErr)
				}
				return fmt.Errorf("deobfuscation is required but the generated ledger is incomplete (%d ambiguous, %d unsupported mappings)", len(privateMap.Ambiguous), len(privateMap.Unsupported))
			}
			capability.ResponseAvailable = false
			capability.CompleteAvailable = false
			capability.ResponseReasons = append(capability.ResponseReasons, "incomplete-ledger")
			capability.CompleteReasons = append(capability.CompleteReasons, "incomplete-ledger")
			klog.Warningf("deobfuscation map not written: %d ambiguous and %d unsupported mappings", len(privateMap.Ambiguous), len(privateMap.Unsupported))
		} else if err := privateMap.Write(filepath.Join(reportingFolder, deobfuscationMapName)); err != nil {
			return err
		}
	}

	reporterErr := reporter.WriteReport(filepath.Join(reportingFolder, reportFileName))
	if reporterErr != nil {
		return reporterErr
	}

	watermarker := watermarking.NewSimpleWaterMarker()
	if err := watermarker.WriteWaterMarkFile(outputPath); err != nil {
		return err
	}

	completedManifest, err := manifest.New(configPath, runID, capability)
	if err != nil {
		return err
	}
	if err := completedManifest.Write(outputPath); err != nil {
		return err
	}
	if capability.ResponseAvailable {
		klog.Infof("Cleaning completed. Deobfuscation: AVAILABLE for support responses")
	} else {
		klog.Warningf("Cleaning completed. Deobfuscation: UNAVAILABLE (%s)", strings.Join(capability.ResponseReasons, ", "))
	}
	if !capability.CompleteAvailable {
		klog.Infof("Complete must-gather recovery: UNAVAILABLE (%s)", strings.Join(capability.CompleteReasons, ", "))
	}
	return nil
}

func outputNeedsCleanup(outputPath string) bool {
	if outputPath == "" {
		return false
	}

	info, err := os.Stat(outputPath)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil || !info.IsDir() {
		return false
	}
	// An existing empty directory is also safe to remove: EnsureInputOutputPath
	// only accepts it as an output destination and this run owns its contents.
	// deleteOutputFolder is intentionally not part of this decision because a
	// required deobfuscation failure must not leave a partial output behind.
	return true
}

func cleanupOutputAfterRequiredFailure(outputPath string, shouldCleanup bool) error {
	if !shouldCleanup || outputPath == "" {
		return nil
	}
	if err := os.RemoveAll(outputPath); err != nil {
		return fmt.Errorf("failed to remove incomplete output %s: %w", outputPath, err)
	}
	return nil
}

func ensurePrivateMapOutsideOutput(reportingFolder string, outputPath string) error {
	if outputPath == "" {
		return nil
	}
	mapPath, err := filepath.Abs(filepath.Join(reportingFolder, deobfuscationMapName))
	if err != nil {
		return fmt.Errorf("failed to resolve deobfuscation map path: %w", err)
	}
	outputPath, err = filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("failed to resolve output path: %w", err)
	}

	relative, err := filepath.Rel(outputPath, mapPath)
	if err != nil {
		return fmt.Errorf("failed to compare deobfuscation map and output paths: %w", err)
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
		return fmt.Errorf("deobfuscation map %s must be outside cleaned output directory %s", mapPath, outputPath)
	}
	return nil
}

func requiredDeobfuscationScope(value string) (deobfuscator.Scope, error) {
	switch value {
	case "":
		return "", nil
	case string(deobfuscator.ScopeResponse):
		return deobfuscator.ScopeResponse, nil
	case string(deobfuscator.ScopeComplete):
		return deobfuscator.ScopeComplete, nil
	default:
		return "", fmt.Errorf("invalid --require-deobfuscation value %q, expected response or complete", value)
	}
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

// finalObfuscator is the obfuscator to use to actually clean a directory.
// prescanObfuscator is an obfuscator that shares some instances of individual obfuscators with the finalObfuscator, but is run in
// a dryRun mode (no output directory) to pre-scan the input and determine the full set of strings to elide.  This allows for
// usage patterns like:
//
//	file/B (exact name unknown) may contain strings like /subscription/ID, where ID needs to be redacted in all files,
//	but file/A contains only ID.  We won't recognize ID as needing redaction until we read file/B.  This means we need to first
//	scan all files, then redact.
func createObfuscatorsFromConfig(config *schema.SchemaJson) (finalObfuscator *obfuscator.MultiObfuscator, prescanObfuscator *obfuscator.MultiObfuscator, finalErr error) {
	return createObfuscatorsFromConfigWithOptions(config, "", "", nil)
}

func createObfuscatorsFromConfigWithOptions(config *schema.SchemaJson, tokenPrefix string, runSecret string, knownHostnames []string) (finalObfuscator *obfuscator.MultiObfuscator, prescanObfuscator *obfuscator.MultiObfuscator, finalErr error) {
	var obfuscators []obfuscator.ReportingObfuscator
	var prescanObfuscators []obfuscator.ReportingObfuscator
	for _, o := range config.Config.Obfuscate {
		var (
			k   obfuscator.ReportingObfuscator
			err error
		)
		tracker := obfuscator.NewSimpleTrackerMap(o.Replacement)
		if tokenPrefix != "" && deobfuscator.IsSupportedReversibleObfuscator(o) {
			tracker = obfuscator.NewSimpleTrackerWithTokenPrefix(tokenPrefix)
		}
		switch o.Type {
		case schema.ObfuscateTypeKeywords:
			k = obfuscator.NewKeywordsObfuscator(o.Replacement)
		case schema.ObfuscateTypeMAC:
			k, err = obfuscator.NewMacAddressObfuscator(o.ReplacementType, tracker)
			if err != nil {
				return nil, nil, err
			}
		case schema.ObfuscateTypeRegex:
			k, err = obfuscator.NewRegexObfuscator(*o.Regex, tracker)
			if err != nil {
				return nil, nil, err
			}
		case schema.ObfuscateTypeDomain:
			k, err = obfuscator.NewDomainObfuscator(o.DomainNames, o.ReplacementType, tracker)
			if err != nil {
				return nil, nil, err
			}
		case schema.ObfuscateTypeAzureResources:
			k, err = obfuscator.NewAzureResourceObfuscator(o.ReplacementType, tracker, config.Config.RandSeed)
			if err != nil {
				return nil, nil, err
			}
			prescanObfuscators = append(prescanObfuscators, k)
		case schema.ObfuscateTypeHostname:
			k = obfuscator.NewHostnameObfuscatorWithSecret(knownHostnames, tracker, runSecret)
		case schema.ObfuscateTypeExact:
			k = obfuscator.NewExactReplacementObfuscator(o.ExactReplacements, tracker)
		case schema.ObfuscateTypeIP:
			k, err = obfuscator.NewIPObfuscator(o.ReplacementType, tracker)
			if err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, fmt.Errorf("unknown obfuscator type %s", o.Type)
		}
		k = obfuscator.NewTargetObfuscator(o.Target, k)
		obfuscators = append(obfuscators, k)
	}
	return obfuscator.NewMultiObfuscator(obfuscators), obfuscator.NewMultiObfuscator(prescanObfuscators), nil
}
