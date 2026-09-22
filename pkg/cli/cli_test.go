package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
	"github.com/openshift/must-gather-clean/pkg/kube"
	"github.com/openshift/must-gather-clean/pkg/reporting"
	"github.com/openshift/must-gather-clean/pkg/schema"
	watermarking "github.com/openshift/must-gather-clean/pkg/watermarker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func findVersionedReport(t *testing.T, directory string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, versionedReportNamePrefix+"*"+versionedReportNameSuffix))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	return matches[0]
}

func reportingTestDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func readVersionedMapping(t *testing.T, path string) *deobfuscator.Mapping {
	t.Helper()
	report, err := reporting.ReadReport(path)
	require.NoError(t, err)
	mapping, err := deobfuscator.NewMappingFromReport(report)
	require.NoError(t, err)
	return mapping
}

func TestRunFailsOnNegativeAndZeroWorkers(t *testing.T) {
	err := Run("", "", "", false, "", 0)
	assert.Equal(t, fmt.Errorf("invalid number of workers specified %d", 0), err)
	err = Run("", "", "", false, "", -2)
	assert.Equal(t, fmt.Errorf("invalid number of workers specified %d", -2), err)
}

func TestRunFailsOnNotExistingInputPath(t *testing.T) {
	err := Run("", "", "", false, "", 1)
	assert.Equal(t, "input folder does not exist: stat : no such file or directory", err.Error())
}

func TestFailConfigReading(t *testing.T) {
	testDir, err := os.MkdirTemp(os.TempDir(), "test-dir-*")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	err = Run("some.yaml", "", testDir, false, "", 1)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestCreateObfuscatorFromFullConfig(t *testing.T) {
	sampleRegex := "^would-match$"
	config := &schema.SchemaJson{Config: schema.SchemaJsonConfig{
		Obfuscate: []schema.Obfuscate{
			{
				Type: schema.ObfuscateTypeKeywords,
				Replacement: map[string]string{
					"something": "something else",
				},
				Target: schema.ObfuscateTargetFileContents,
			},
			{
				Type:            schema.ObfuscateTypeMAC,
				ReplacementType: schema.ObfuscateReplacementTypeStatic,
				Target:          schema.ObfuscateTargetFileContents,
			},
			{
				Type:   schema.ObfuscateTypeRegex,
				Regex:  &sampleRegex,
				Target: schema.ObfuscateTargetFileContents,
			},
			{
				Type:            schema.ObfuscateTypeDomain,
				DomainNames:     []string{"something.com"},
				Target:          schema.ObfuscateTargetFileContents,
				ReplacementType: schema.ObfuscateReplacementTypeStatic,
			},
			{
				Type:            schema.ObfuscateTypeIP,
				ReplacementType: schema.ObfuscateReplacementTypeStatic,
				Target:          schema.ObfuscateTargetFileContents,
			},
		},
		Omit: nil,
	}}

	mfo, _, err := createObfuscatorsFromConfig(config)
	require.NoError(t, err)
	assert.Equal(t, "something else", mfo.Contents("something"))
}

func TestCreateOmitter(t *testing.T) {
	sampleApiVersion := "v1"
	sampleKind := "Resource"
	sampleRegex := "would-match"

	config := &schema.SchemaJson{Config: schema.SchemaJsonConfig{
		Omit: []schema.Omit{
			{
				Type: schema.OmitTypeKubernetes,
				KubernetesResource: &schema.OmitKubernetesResource{
					ApiVersion: &sampleApiVersion,
					Kind:       &sampleKind,
					Namespaces: []string{"kube-system"},
				}},
			{
				Type:    schema.OmitTypeFile,
				Pattern: &sampleRegex,
			},
		},
	}}

	om, err := createOmittersFromConfig(config, "")
	require.NoError(t, err)

	match, err := om.OmitPath("would-match")
	require.NoError(t, err)
	assert.Truef(t, match, "'would-match' should match the path omission config")
	match, err = om.OmitPath("would-not-match")
	require.NoError(t, err)
	assert.Falsef(t, match, "'would-not-match' should match the path omission config")

	match, err = om.OmitKubeResource(&kube.ResourceListWithPath{
		ResourceList: kube.ResourceList{
			Items: []kube.Resource{
				{ApiVersion: sampleApiVersion, Kind: sampleKind, Metadata: kube.Metadata{Namespace: "kube-system"}},
			},
		},
		Path: "some-path",
	})
	require.NoError(t, err)
	assert.Truef(t, match, "k8s resource with the exact same input should match")
}

func TestRunPipeNoConfig(t *testing.T) {
	file, err := os.CreateTemp("", "temp-file")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(file.Name())
	}()

	_, err = file.WriteString("some IP 192.167.122.2 that needs to be obfuscated\nand some mac eb:a1:2a:b2:09:bf\n")
	require.NoError(t, err)

	require.NoError(t, file.Close())
	inputFile, err := os.Open(file.Name())
	require.NoError(t, err)
	defer func() {
		_ = inputFile.Close()
	}()

	outputFile, err := os.CreateTemp("", "temp-file")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(outputFile.Name())
	}()

	err = RunPipe("", inputFile, outputFile)
	require.NoError(t, err)
	require.NoError(t, outputFile.Close())

	bytes, err := os.ReadFile(outputFile.Name())
	require.NoError(t, err)

	assert.Equal(t, "some IP x-ipv4-0000000001-x that needs to be obfuscated\nand some mac x-mac-0000000001-x\n", string(bytes))
}

func TestRunPipeWithOptionsRejectsReversibleMode(t *testing.T) {
	err := RunPipeWithOptions("", strings.NewReader("input"), io.Discard, true)
	require.EqualError(t, err, "reversible workflow is unavailable: pipe-mode does not produce a versioned report")
}

func TestRunPipeConfigMacOnly(t *testing.T) {
	cfgFile, err := os.CreateTemp("", "temp-file-*.yaml")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(cfgFile.Name())
	}()

	_, err = cfgFile.WriteString(`
config:
  obfuscate:
    - type: MAC
      replacementType: Consistent
      target: All
`)
	require.NoError(t, err)
	require.NoError(t, cfgFile.Close())

	file, err := os.CreateTemp("", "temp-file")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(file.Name())
	}()

	_, err = file.WriteString("some IP 192.167.122.2 that should not to be obfuscated\nand some mac eb:a1:2a:b2:09:bf\n")
	require.NoError(t, err)

	require.NoError(t, file.Close())
	inputFile, err := os.Open(file.Name())
	require.NoError(t, err)
	defer func() {
		_ = inputFile.Close()
	}()

	outputFile, err := os.CreateTemp("", "temp-file")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(outputFile.Name())
	}()

	err = RunPipe(cfgFile.Name(), inputFile, outputFile)
	require.NoError(t, err)
	require.NoError(t, outputFile.Close())

	bytes, err := os.ReadFile(outputFile.Name())
	require.NoError(t, err)

	assert.Equal(t, "some IP 192.167.122.2 that should not to be obfuscated\nand some mac x-mac-0000000001-x\n", string(bytes))
}

func TestWaterMarkerNotCreatedOnFail(t *testing.T) {
	testDir, err := os.MkdirTemp(os.TempDir(), "test-dir-*")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	err = Run("some.yaml", "", testDir, false, "", 1)
	assert.ErrorIs(t, err, os.ErrNotExist)
	require.NoFileExists(t, filepath.Join(testDir, "watermark.txt"))
}

func TestRunWritesVersionedResponseReport(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := reportingTestDir(t)

	inputFile := filepath.Join(inputDir, "input.log")
	require.NoError(t, os.WriteFile(inputFile, []byte("node 192.167.122.2\n"), 0600))

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
      target: All
`), 0600))

	require.NoError(t, RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true}))

	reportPath := findVersionedReport(t, reportDir)
	mapping := readVersionedMapping(t, reportPath)
	require.Len(t, mapping.Rules, 1)
	assert.Contains(t, mapping.Rules[0].Obfuscated, "x-mgc1-")
	assert.Contains(t, mapping.Rules[0].Obfuscated, "-o1-")
	cleaned, err := os.ReadFile(filepath.Join(outputDir, "input.log"))
	require.NoError(t, err)
	assert.Equal(t, "node 192.167.122.2\n", mapping.Deobfuscate(string(cleaned)))
	latestReportPath := filepath.Join(reportDir, reportFileName)
	assert.FileExists(t, latestReportPath)
	reportInfo, err := os.Stat(latestReportPath)
	require.NoError(t, err)
	assert.NotEqual(t, os.FileMode(0000), reportInfo.Mode().Perm())
	assert.NotEmpty(t, mapping.RunID)
	assert.Equal(t, versionedReportNameForRun(mapping.RunID), filepath.Base(reportPath))
	assert.NoFileExists(t, filepath.Join(outputDir, "must-gather-clean-manifest.yaml"))
}

func TestRunReversibleUsesReportingFolder(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	inputDir := filepath.Join(root, "input")
	outputDir := filepath.Join(root, "cleaned")
	reportDir := filepath.Join(root, "report")
	require.NoError(t, os.Mkdir(inputDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "input.log"), []byte("ip 192.167.122.2\n"), 0600))
	configPath := filepath.Join(root, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
      target: All
`), 0600))

	require.NoError(t, RunWithOptions(configPath, inputDir, outputDir, RunOptions{
		ReportingFolder: reportDir,
		WorkerCount:     1,
		Reversible:      true,
	}))
	assert.FileExists(t, filepath.Join(reportDir, reportFileName))
	assert.FileExists(t, findVersionedReport(t, reportDir))
}

func TestRunDefaultObfuscatorsRoundTripThroughReport(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := reportingTestDir(t)
	input := `ip 192.167.122.2
mac EB:A1:2A:B2:09:BF
host api.dev.rhcloud.com
resource /subscriptions/subscription-id/resourceGroups/group-name/providers/Microsoft.Compute/virtualMachines/vm-name
`
	inputPath := filepath.Join(inputDir, "input.log")
	require.NoError(t, os.WriteFile(inputPath, []byte(input), 0600))

	configPath := filepath.Join(t.TempDir(), "openshift_default.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
      target: All
    - type: MAC
      replacementType: Consistent
      target: All
    - type: Domain
      replacementType: Consistent
      target: All
      domainNames:
        - "rhcloud.com"
        - "dev.rhcloud.com"
    - type: AzureResources
      replacementType: Consistent
      target: All
  randSeed: 1
`), 0600))

	require.NoError(t, RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true}))
	cleaned, err := os.ReadFile(filepath.Join(outputDir, "input.log"))
	require.NoError(t, err)
	assert.NotContains(t, string(cleaned), "192.167.122.2")
	assert.NotContains(t, string(cleaned), "EB:A1:2A:B2:09:BF")

	mapping := readVersionedMapping(t, findVersionedReport(t, reportDir))
	restored := mapping.Deobfuscate(string(cleaned))
	assert.Contains(t, restored, "192.167.122.2")
	assert.Contains(t, restored, "EB:A1:2A:B2:09:BF")
	assert.Contains(t, restored, "api.dev.rhcloud.com")
	assert.Contains(t, restored, "/subscriptions/subscription-id")
	assert.Contains(t, restored, "group-name")
	assert.Contains(t, restored, "vm-name")
}

func TestRunAzureResourcesRoundTripCanonicalResourceAndSubscription(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := reportingTestDir(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	input := "/subscriptions/subscription/resourceGroups/resource/providers/Microsoft.Compute/virtualMachines/resource\n"
	canonicalInput := "/subscriptions/subscription/resourcegroups/resource/providers/Microsoft.Compute/virtualMachines/resource\n"
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "input.log"), []byte(input), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: AzureResources
      replacementType: Consistent
      target: All
  randSeed: 1
`), 0600))

	require.NoError(t, RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true}))
	cleaned, err := os.ReadFile(filepath.Join(outputDir, "input.log"))
	require.NoError(t, err)
	assert.NotEqual(t, input, string(cleaned))

	mapping := readVersionedMapping(t, findVersionedReport(t, reportDir))
	require.NotEmpty(t, mapping.Rules)
	restored := mapping.Deobfuscate(string(cleaned))
	assert.Equal(t, canonicalInput, restored)

	var foundResource, foundSubscription bool
	for _, rule := range mapping.Rules {
		foundResource = foundResource || rule.Original == "resource"
		foundSubscription = foundSubscription || rule.Original == "subscription"
	}
	assert.True(t, foundResource)
	assert.True(t, foundSubscription)
}

func TestRunAzureResourcesRoundTripAcrossGlobalCanonicalPass(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := reportingTestDir(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	input := "/subscriptions/12345678-1234-1234-1234-123456789abc/resourceGroups/resourcegroup/providers/Microsoft.Compute/virtualMachines/resource\nresourcegroup\n"
	canonicalInput := "/subscriptions/12345678-1234-1234-1234-123456789abc/resourcegroups/resourcegroup/providers/Microsoft.Compute/virtualMachines/resource\nresourcegroup\n"
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "input.log"), []byte(input), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: AzureResources
      replacementType: Consistent
      target: All
  randSeed: 1
`), 0600))

	require.NoError(t, RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true}))
	cleaned, err := os.ReadFile(filepath.Join(outputDir, "input.log"))
	require.NoError(t, err)
	mapping := readVersionedMapping(t, findVersionedReport(t, reportDir))
	assert.Equal(t, canonicalInput, mapping.Deobfuscate(string(cleaned)))
}

func TestRunResponseDeobfuscationKeepsReportsForMultipleRuns(t *testing.T) {
	inputDir := t.TempDir()
	firstOutputDir := filepath.Join(t.TempDir(), "first-cleaned")
	secondOutputDir := filepath.Join(t.TempDir(), "second-cleaned")
	reportDir := reportingTestDir(t)
	inputPath := filepath.Join(inputDir, "input.log")
	require.NoError(t, os.WriteFile(inputPath, []byte("node 192.167.122.2\n"), 0600))
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
      target: All
`), 0600))

	require.NoError(t, RunWithOptions(configPath, inputDir, firstOutputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true}))
	firstReportPath := findVersionedReport(t, reportDir)
	firstReport := readVersionedMapping(t, firstReportPath)

	require.NoError(t, RunWithOptions(configPath, inputDir, secondOutputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true}))
	reportPaths, err := filepath.Glob(filepath.Join(reportDir, versionedReportNamePrefix+"*"+versionedReportNameSuffix))
	require.NoError(t, err)
	require.Len(t, reportPaths, 2)
	assert.FileExists(t, firstReportPath)
	secondReportPath := reportPaths[0]
	if secondReportPath == firstReportPath {
		secondReportPath = reportPaths[1]
	}
	secondReport := readVersionedMapping(t, secondReportPath)
	assert.NotEqual(t, firstReport.RunID, secondReport.RunID)

	firstCleaned := string(mustReadFile(t, filepath.Join(firstOutputDir, "input.log")))
	secondCleaned := string(mustReadFile(t, filepath.Join(secondOutputDir, "input.log")))
	assert.Equal(t, "node 192.167.122.2\n", firstReport.Deobfuscate(firstCleaned))
	assert.Equal(t, "node 192.167.122.2\n", secondReport.Deobfuscate(secondCleaned))
	assert.NotEqual(t, firstCleaned, secondCleaned)
}

func TestRunWithoutRequireKeepsLegacyObfuscationAndDoesNotWriteVersionedReport(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := t.TempDir()
	previousReport := []byte("previous report must survive a legacy run\n")
	previousReportName := versionedReportNameForRun("previous-run")
	require.NoError(t, os.WriteFile(filepath.Join(reportDir, previousReportName), previousReport, 0600))
	inputPath := filepath.Join(inputDir, "input.log")
	require.NoError(t, os.WriteFile(inputPath, []byte("node 192.167.122.2\n"), 0600))

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
      target: All
`), 0600))

	require.NoError(t, Run(configPath, inputDir, outputDir, false, reportDir, 1))
	cleaned, err := os.ReadFile(filepath.Join(outputDir, "input.log"))
	require.NoError(t, err)
	assert.Equal(t, "node x-ipv4-0000000001-x\n", string(cleaned))
	actualReport, err := os.ReadFile(filepath.Join(reportDir, previousReportName))
	require.NoError(t, err)
	assert.Equal(t, previousReport, actualReport)
}

func TestRunReusesReportWithoutRequire(t *testing.T) {
	inputDir := t.TempDir()
	firstOutputDir := filepath.Join(t.TempDir(), "first-cleaned")
	secondOutputDir := filepath.Join(t.TempDir(), "second-cleaned")
	firstReportDir := reportingTestDir(t)
	secondReportDir := reportingTestDir(t)
	inputPath := filepath.Join(inputDir, "input.log")
	require.NoError(t, os.WriteFile(inputPath, []byte("node 192.167.122.2\n"), 0600))

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
      target: All
`), 0600))

	require.NoError(t, Run(configPath, inputDir, firstOutputDir, false, firstReportDir, 1))
	require.NoError(t, Run(filepath.Join(firstReportDir, reportFileName), inputDir, secondOutputDir, false, secondReportDir, 1))

	first, err := os.ReadFile(filepath.Join(firstOutputDir, "input.log"))
	require.NoError(t, err)
	second, err := os.ReadFile(filepath.Join(secondOutputDir, "input.log"))
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))
	reportPaths, err := filepath.Glob(filepath.Join(secondReportDir, versionedReportNamePrefix+"*"+versionedReportNameSuffix))
	require.NoError(t, err)
	assert.Empty(t, reportPaths)
}

func TestRunWithoutRequireAllowsReportingInsideOutput(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
`), 0600))

	require.NoError(t, Run(configPath, inputDir, outputDir, false, outputDir, 1))
	assert.FileExists(t, filepath.Join(outputDir, reportFileName))
}

func TestRunWithoutRequireAllowsReportingInsideInput(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Static
`), 0600))

	require.NoError(t, Run(configPath, inputDir, outputDir, false, inputDir, 1))
	assert.FileExists(t, filepath.Join(inputDir, reportFileName))
}

func TestRunWithoutRequireAllowsReportingSymlinkIntoOutput(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	require.NoError(t, os.Mkdir(outputDir, 0755))
	reportLink := filepath.Join(t.TempDir(), "report-link")
	require.NoError(t, os.Symlink(outputDir, reportLink))
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Static
`), 0600))

	require.NoError(t, Run(configPath, inputDir, outputDir, false, reportLink, 1))
	assert.FileExists(t, filepath.Join(outputDir, reportFileName))
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func TestRunTreatsExistingManifestAsAnInputFile(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
`), 0600))

	manifestPath := filepath.Join(inputDir, "must-gather-clean-manifest.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte("version: 1\nstatus: completed\n"), 0600))

	reportDir := reportingTestDir(t)
	require.NoError(t, Run(configPath, inputDir, outputDir, false, reportDir, 1))
	loadedManifest, err := os.ReadFile(filepath.Join(outputDir, filepath.Base(manifestPath)))
	require.NoError(t, err)
	assert.Equal(t, "version: 1\nstatus: completed\n", string(loadedManifest))
	reportPaths, err := filepath.Glob(filepath.Join(reportDir, versionedReportNamePrefix+"*"+versionedReportNameSuffix))
	require.NoError(t, err)
	assert.Empty(t, reportPaths)
}

func TestRunTreatsInvalidManifestAsAnInputFile(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(inputDir, "must-gather-clean-manifest.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte("version: 999\nstatus: completed\n"), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte("config:\n  obfuscate:\n    - type: IP\n      replacementType: Consistent\n"), 0600))

	require.NoError(t, Run(configPath, inputDir, outputDir, false, t.TempDir(), 1))
	manifestBytes, err := os.ReadFile(filepath.Join(outputDir, filepath.Base(manifestPath)))
	require.NoError(t, err)
	assert.Contains(t, string(manifestBytes), "version: 999")
}

func TestRunWithoutRequireAllowsReportingInsideOutputWithFiles(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "input.log"), []byte("ip 192.167.122.2\n"), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Static
`), 0600))

	require.NoError(t, Run(configPath, inputDir, outputDir, false, outputDir, 1))
	assert.FileExists(t, filepath.Join(outputDir, reportFileName))
	assert.FileExists(t, filepath.Join(outputDir, "input.log"))
}

func TestRunRequiresResponseDeobfuscationAllowsOmissions(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	reportDir := reportingTestDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "input.log"), []byte("ip 192.167.122.2\n"), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
  omit:
    - type: File
      pattern: "*.secret"
`), 0600))

	err := RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true})
	require.NoError(t, err)
	assert.FileExists(t, findVersionedReport(t, reportDir))
}

func TestRunDoesNotIncludeValuesFromOmittedFiles(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := reportingTestDir(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "kept.log"), []byte("ip 192.167.122.2\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "omitted.secret"), []byte("/subscriptions/omitted-subscription/resourceGroups/omitted-group/providers/Microsoft.Compute/virtualMachines/omitted-vm\n"), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
      target: All
    - type: AzureResources
      replacementType: Consistent
      target: All
  omit:
    - type: File
      pattern: "*.secret"
  randSeed: 1
`), 0600))

	require.NoError(t, RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true}))
	mapping := readVersionedMapping(t, findVersionedReport(t, reportDir))
	assert.Len(t, mapping.Rules, 1)
	assert.Equal(t, string(schema.ObfuscateTypeIP), mapping.Rules[0].Type)
	assert.NoFileExists(t, filepath.Join(outputDir, "omitted.secret"))
}

func TestRunAzurePrescanRespectsTarget(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := reportingTestDir(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "kept.log"), []byte("/subscriptions/content-subscription/resourceGroups/content-group/providers/Microsoft.Compute/virtualMachines/content-vm\n"), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: AzureResources
      replacementType: Consistent
      target: FilePath
  randSeed: 1
`), 0600))

	require.NoError(t, RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true}))
	mapping := readVersionedMapping(t, findVersionedReport(t, reportDir))
	assert.Empty(t, mapping.Rules)
	cleaned, err := os.ReadFile(filepath.Join(outputDir, "kept.log"))
	require.NoError(t, err)
	assert.Contains(t, string(cleaned), "content-subscription")
}

func TestRunResponseDeobfuscationAllowsBestEffortCleaning(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := reportingTestDir(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "input.log"), []byte("ip 192.167.122.2 mac EB:A1:2A:B2:09:BF\n"), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Static
    - type: MAC
      replacementType: Consistent
`), 0600))

	require.NoError(t, RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true}))
	mapping := readVersionedMapping(t, findVersionedReport(t, reportDir))
	require.Len(t, mapping.Rules, 1)
	require.Len(t, mapping.Unsupported, 1)
	assert.Equal(t, string(schema.ObfuscateTypeIP), mapping.Unsupported[0].Type)
	cleaned := string(mustReadFile(t, filepath.Join(outputDir, "input.log")))
	assert.Contains(t, cleaned, "xxx.xxx.xxx.xxx")
	assert.Contains(t, mapping.Deobfuscate(cleaned), "EB:A1:2A:B2:09:BF")
}

func TestRunNamespacesRepeatedReversibleObfuscators(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := reportingTestDir(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "192.167.122.1"), []byte("ip 192.167.122.2\n"), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
      target: FileContents
    - type: IP
      replacementType: Consistent
      target: FilePath
`), 0600))

	err := RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: reportDir, WorkerCount: 1, Reversible: true})
	require.NoError(t, err)
	mapping := readVersionedMapping(t, findVersionedReport(t, reportDir))
	assert.Len(t, mapping.Rules, 2)
	assert.Empty(t, mapping.Ambiguous)
}

func writeReportForDeobfuscationTest(t *testing.T, runID, token string) string {
	t.Helper()
	report := reporting.Report{
		RunID: runID,
		Config: schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
			Type:            schema.ObfuscateTypeIP,
			ReplacementType: schema.ObfuscateReplacementTypeConsistent,
		}}},
		Replacements: [][]reporting.Replacement{{{
			Canonical:    "10.0.0.1",
			ReplacedWith: token,
		}}},
	}
	path := filepath.Join(t.TempDir(), "report.yaml")
	data, err := yaml.Marshal(report)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

func TestRunDeobfuscateReadsAndWritesFiles(t *testing.T) {
	reportPath := writeReportForDeobfuscationTest(t, "", "x-ipv4-0000000001-x")
	inputPath := filepath.Join(t.TempDir(), "support-response.txt")
	outputPath := filepath.Join(t.TempDir(), "support-response-local.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("node x-ipv4-0000000001-x\n"), 0600))

	require.NoError(t, RunDeobfuscate(reportPath, inputPath, outputPath))
	output, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, "node 10.0.0.1\n", string(output))
}

func TestRunDeobfuscateStdoutDoesNotPublishBeforeRunValidation(t *testing.T) {
	reportPath := writeReportForDeobfuscationTest(t, "0123456789abcdef0123456789abcdef", "x-mgc1-0123456789abcdef01234567-o1-x-ipv4-0000000001-x")

	inputPath := filepath.Join(t.TempDir(), "support-response.txt")
	foreignToken := "x-mgc1-fedcba9876543210fedcba98-o1-x-ipv4-0000000001-x"
	input := strings.Repeat("valid response prefix ", 4096) + foreignToken
	require.NoError(t, os.WriteFile(inputPath, []byte(input), 0600))

	oldStdout := os.Stdout
	stdoutReader, stdoutWriter, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = stdoutWriter
	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
	})

	err = RunDeobfuscate(reportPath, inputPath, "")
	require.Error(t, err)
	require.NoError(t, stdoutWriter.Close())
	output, readErr := io.ReadAll(stdoutReader)
	require.NoError(t, readErr)
	assert.Empty(t, output)
}

func TestRunDeobfuscateRejectsSameInputAndOutput(t *testing.T) {
	reportPath := writeReportForDeobfuscationTest(t, "", "x-ipv4-0000000001-x")

	responsePath := filepath.Join(t.TempDir(), "support-response.txt")
	require.NoError(t, os.WriteFile(responsePath, []byte("response\n"), 0600))

	err := RunDeobfuscate(reportPath, responsePath, responsePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be different files")
	contents, readErr := os.ReadFile(responsePath)
	require.NoError(t, readErr)
	assert.Equal(t, "response\n", string(contents))
}

func TestRunDeobfuscateRejectsReportAsOutput(t *testing.T) {
	reportPath := writeReportForDeobfuscationTest(t, "run-id", "x-ipv4-0000000001-x")
	originalReport, err := os.ReadFile(reportPath)
	require.NoError(t, err)

	err = RunDeobfuscate(reportPath, "", reportPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "report and response output must be different files")
	assert.Equal(t, originalReport, mustReadFile(t, reportPath))
}

func TestRunDeobfuscateRejectsInputOutputAliases(t *testing.T) {
	reportPath := writeReportForDeobfuscationTest(t, "", "x-ipv4-0000000001-x")

	responsePath := filepath.Join(t.TempDir(), "support-response.txt")
	aliasPath := filepath.Join(t.TempDir(), "support-response-alias.txt")
	require.NoError(t, os.WriteFile(responsePath, []byte("response\n"), 0600))
	require.NoError(t, os.Symlink(responsePath, aliasPath))

	err := RunDeobfuscate(reportPath, responsePath, aliasPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be different files")
	contents, readErr := os.ReadFile(responsePath)
	require.NoError(t, readErr)
	assert.Equal(t, "response\n", string(contents))
}

func TestRunRequiresResponseDeobfuscationRejectsPreviouslyCleanedInput(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, watermarking.NewSimpleWaterMarker().WriteWaterMarkFile(inputDir))
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
`), 0600))

	err := RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: t.TempDir(), WorkerCount: 1, Reversible: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "previously-cleaned-input")
	assert.NoDirExists(t, outputDir)
}

func TestRunRequiresResponseDeobfuscationIgnoresUnrelatedWatermark(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportingDir := reportingTestDir(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "watermark.txt"), []byte("2026-09-11 10:00:00 +0000 UTC\ncustomer-data\n"), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
`), 0600))

	err := RunWithOptions(configPath, inputDir, outputDir, RunOptions{ReportingFolder: reportingDir, WorkerCount: 1, Reversible: true})
	require.NoError(t, err)
	assert.DirExists(t, outputDir)
}
