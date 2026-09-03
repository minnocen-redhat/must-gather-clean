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
	return RunWithOptions(configPath, inputPath, outputPath, deleteOutputFolder, reportingFolder, workerCount, false)
}

func RunWithOptions(configPath string, inputPath string, outputPath string, deleteOutputFolder bool, reportingFolder string, workerCount int, forceReclean bool) error {
	if workerCount < 1 {
		return fmt.Errorf("invalid number of workers specified %d", workerCount)
	}
	if err := ensurePrivateMapOutsideOutput(reportingFolder, outputPath); err != nil {
		return err
	}

	if !forceReclean {
		if _, err := manifest.Read(inputPath); err == nil {
			return fmt.Errorf("input folder %s was already processed by must-gather-clean; use --force-reclean to process it again", inputPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	err := fsutil.EnsureInputOutputPath(inputPath, outputPath, deleteOutputFolder)
	if err != nil {
		return err
	}

	config, err := schema.ReadConfigFromPath(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config at %s: %w", configPath, err)
	}

	obfuscator, prescanObfuscator, err := createObfuscatorsFromConfig(config)
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
	reporterErr := reporter.WriteReport(filepath.Join(reportingFolder, reportFileName))
	if reporterErr != nil {
		return reporterErr
	}

	privateMap, err := deobfuscator.NewMap(config.Config, obfuscatorReports)
	if err != nil {
		return err
	}
	if err := privateMap.Write(filepath.Join(reportingFolder, deobfuscationMapName)); err != nil {
		return err
	}
	if len(privateMap.Ambiguous) > 0 || len(privateMap.Unsupported) > 0 {
		klog.Warningf("deobfuscation map contains %d ambiguous and %d unsupported mappings", len(privateMap.Ambiguous), len(privateMap.Unsupported))
	}

	watermarker := watermarking.NewSimpleWaterMarker()
	if err := watermarker.WriteWaterMarkFile(outputPath); err != nil {
		return err
	}

	completedManifest, err := manifest.New(configPath, privateMap.RunID)
	if err != nil {
		return err
	}
	return completedManifest.Write(outputPath)
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
	var obfuscators []obfuscator.ReportingObfuscator
	var prescanObfuscators []obfuscator.ReportingObfuscator
	for _, o := range config.Config.Obfuscate {
		var (
			k   obfuscator.ReportingObfuscator
			err error
		)
		tracker := obfuscator.NewSimpleTrackerMap(o.Replacement)
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
