package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
	"github.com/openshift/must-gather-clean/pkg/kube"
	"github.com/openshift/must-gather-clean/pkg/manifest"
	"github.com/openshift/must-gather-clean/pkg/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestRunWritesPrivateDeobfuscationMap(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := t.TempDir()

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

	require.NoError(t, Run(configPath, inputDir, outputDir, false, reportDir, 1))

	mapPath := filepath.Join(reportDir, deobfuscationMapName)
	privateMap, err := deobfuscator.ReadMap(mapPath)
	require.NoError(t, err)
	cleaned, err := os.ReadFile(filepath.Join(outputDir, "input.log"))
	require.NoError(t, err)
	assert.Equal(t, "node 192.167.122.2\n", privateMap.Deobfuscate(string(cleaned)))
	reportPath := filepath.Join(reportDir, reportFileName)
	assert.FileExists(t, reportPath)
	reportInfo, err := os.Stat(reportPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), reportInfo.Mode().Perm())
	completedManifest, err := manifest.Read(outputDir)
	require.NoError(t, err)
	assert.Equal(t, privateMap.RunID, completedManifest.DeobfuscationMapRunID)
}

func TestRunDefaultObfuscatorsRoundTripThroughPrivateMap(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := t.TempDir()
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

	require.NoError(t, Run(configPath, inputDir, outputDir, false, reportDir, 1))
	cleaned, err := os.ReadFile(filepath.Join(outputDir, "input.log"))
	require.NoError(t, err)
	assert.NotContains(t, string(cleaned), "192.167.122.2")
	assert.NotContains(t, string(cleaned), "EB:A1:2A:B2:09:BF")

	privateMap, err := deobfuscator.ReadMap(filepath.Join(reportDir, deobfuscationMapName))
	require.NoError(t, err)
	restored := privateMap.Deobfuscate(string(cleaned))
	assert.Contains(t, restored, "192.167.122.2")
	assert.Contains(t, restored, "EB:A1:2A:B2:09:BF")
	assert.Contains(t, restored, "api.dev.rhcloud.com")
	assert.Contains(t, restored, "/subscriptions/subscription-id")
	assert.Contains(t, restored, "group-name")
	assert.Contains(t, restored, "vm-name")
}

func TestRunRejectsPrivateMapInsideOutput(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
`), 0600))

	err := Run(configPath, inputDir, outputDir, false, outputDir, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be outside cleaned output directory")
}

func TestRunRejectsReportingArtifactsInsideInput(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Static
`), 0600))

	err := Run(configPath, inputDir, outputDir, false, inputDir, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be outside input directory")
}

func TestRunRejectsReportingSymlinkIntoOutput(t *testing.T) {
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

	err := Run(configPath, inputDir, outputDir, false, reportLink, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be outside cleaned output directory")
}

func TestRunAllowsRecleanButDisablesDeobfuscation(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Consistent
`), 0600))

	completedManifest := &manifest.Manifest{
		Version: manifest.CurrentVersion,
		Status:  manifest.StatusCompleted,
	}
	require.NoError(t, completedManifest.Write(inputDir))

	reportDir := t.TempDir()
	err := Run(configPath, inputDir, outputDir, false, reportDir, 1)
	require.NoError(t, err)
	completedManifest, err = manifest.Read(outputDir)
	require.NoError(t, err)
	assert.Equal(t, "unavailable", completedManifest.DeobfuscationStatus)
	assert.Contains(t, completedManifest.DeobfuscationReasons, "previously-cleaned-input")
	assert.NoFileExists(t, filepath.Join(reportDir, deobfuscationMapName))
}

func TestRunRejectsInvalidInputManifest(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, manifest.FileName), []byte("version: 999\nstatus: completed\n"), 0600))
	require.NoError(t, os.WriteFile(configPath, []byte("config:\n  obfuscate:\n    - type: IP\n      replacementType: Consistent\n"), 0600))

	err := Run(configPath, inputDir, outputDir, false, t.TempDir(), 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid must-gather-clean manifest")
	assert.NoDirExists(t, outputDir)
}

func TestRunRejectsReportingArtifactsInsideOutput(t *testing.T) {
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

	err := Run(configPath, inputDir, outputDir, false, outputDir, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be outside cleaned output directory")
	assert.NoDirExists(t, outputDir)
}

func TestRunRequiresResponseDeobfuscationAllowsOmissions(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	reportDir := t.TempDir()
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

	err := RunWithOptions(configPath, inputDir, outputDir, false, reportDir, 1, "response")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(reportDir, deobfuscationMapName))
	completedManifest, err := manifest.Read(outputDir)
	require.NoError(t, err)
	assert.Equal(t, "available", completedManifest.DeobfuscationStatus)
}

func TestRunDoesNotMapValuesFromOmittedFiles(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := t.TempDir()
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

	require.NoError(t, RunWithOptions(configPath, inputDir, outputDir, false, reportDir, 1, "response"))
	privateMap, err := deobfuscator.ReadMap(filepath.Join(reportDir, deobfuscationMapName))
	require.NoError(t, err)
	assert.Len(t, privateMap.Rules, 1)
	assert.Equal(t, string(schema.ObfuscateTypeIP), privateMap.Rules[0].Type)
	assert.NoFileExists(t, filepath.Join(outputDir, "omitted.secret"))
}

func TestRunAzurePrescanRespectsTarget(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := t.TempDir()
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

	require.NoError(t, RunWithOptions(configPath, inputDir, outputDir, false, reportDir, 1, "response"))
	privateMap, err := deobfuscator.ReadMap(filepath.Join(reportDir, deobfuscationMapName))
	require.NoError(t, err)
	assert.Empty(t, privateMap.Rules)
	cleaned, err := os.ReadFile(filepath.Join(outputDir, "kept.log"))
	require.NoError(t, err)
	assert.Contains(t, string(cleaned), "content-subscription")
}

func TestRunRequiresResponseDeobfuscationRejectsStaticCleaning(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
config:
  obfuscate:
    - type: IP
      replacementType: Static
`), 0600))

	err := RunWithOptions(configPath, inputDir, outputDir, false, t.TempDir(), 1, "response")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported-obfuscator:IP")
	assert.NoDirExists(t, outputDir)
}

func TestRunNamespacesRepeatedReversibleObfuscators(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := t.TempDir()
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

	err := RunWithOptions(configPath, inputDir, outputDir, false, reportDir, 1, "response")
	require.NoError(t, err)
	privateMap, err := deobfuscator.ReadMap(filepath.Join(reportDir, deobfuscationMapName))
	require.NoError(t, err)
	assert.Len(t, privateMap.Rules, 2)
	assert.Empty(t, privateMap.Ambiguous)
}

func TestRunDeobfuscateReadsAndWritesFiles(t *testing.T) {
	privateMap := &deobfuscator.Map{
		Version: deobfuscator.CurrentMapVersion,
		Rules: []deobfuscator.Rule{{
			Type:       "IP",
			Original:   "10.0.0.1",
			Obfuscated: "x-ipv4-0000000001-x",
		}},
	}
	mapPath := filepath.Join(t.TempDir(), "deobfuscation-map.yaml")
	require.NoError(t, privateMap.Write(mapPath))

	inputPath := filepath.Join(t.TempDir(), "support-response.txt")
	outputPath := filepath.Join(t.TempDir(), "support-response-local.txt")
	require.NoError(t, os.WriteFile(inputPath, []byte("node x-ipv4-0000000001-x\n"), 0600))

	require.NoError(t, RunDeobfuscate(mapPath, inputPath, outputPath))
	output, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, "node 10.0.0.1\n", string(output))
}

func TestRunDeobfuscateRejectsSameInputAndOutput(t *testing.T) {
	privateMap := &deobfuscator.Map{Version: deobfuscator.CurrentMapVersion}
	mapPath := filepath.Join(t.TempDir(), "deobfuscation-map.yaml")
	require.NoError(t, privateMap.Write(mapPath))

	responsePath := filepath.Join(t.TempDir(), "support-response.txt")
	require.NoError(t, os.WriteFile(responsePath, []byte("response\n"), 0600))

	err := RunDeobfuscate(mapPath, responsePath, responsePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be different files")
	contents, readErr := os.ReadFile(responsePath)
	require.NoError(t, readErr)
	assert.Equal(t, "response\n", string(contents))
}

func TestRunDeobfuscateRejectsInputOutputAliases(t *testing.T) {
	privateMap := &deobfuscator.Map{Version: deobfuscator.CurrentMapVersion}
	mapPath := filepath.Join(t.TempDir(), "deobfuscation-map.yaml")
	require.NoError(t, privateMap.Write(mapPath))

	responsePath := filepath.Join(t.TempDir(), "support-response.txt")
	aliasPath := filepath.Join(t.TempDir(), "support-response-alias.txt")
	require.NoError(t, os.WriteFile(responsePath, []byte("response\n"), 0600))
	require.NoError(t, os.Symlink(responsePath, aliasPath))

	err := RunDeobfuscate(mapPath, responsePath, aliasPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be different files")
	contents, readErr := os.ReadFile(responsePath)
	require.NoError(t, readErr)
	assert.Equal(t, "response\n", string(contents))
}
