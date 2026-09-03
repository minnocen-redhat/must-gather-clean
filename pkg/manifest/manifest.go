package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	version "github.com/openshift/must-gather-clean/pkg/version"
	"gopkg.in/yaml.v3"
)

const (
	FileName           = "must-gather-clean-manifest.yaml"
	CurrentVersion     = 1
	StatusCompleted    = "completed"
	PrivateMapFileName = "deobfuscation-map.yaml"
)

// Manifest identifies a must-gather that has been successfully processed by
// must-gather-clean. It intentionally contains no customer values.
type Manifest struct {
	Version               int    `yaml:"version"`
	Status                string `yaml:"status"`
	ToolVersion           string `yaml:"toolVersion"`
	CompletedAt           string `yaml:"completedAt"`
	ConfigSHA256          string `yaml:"configSha256"`
	DeobfuscationMap      string `yaml:"deobfuscationMap"`
	DeobfuscationMapRunID string `yaml:"deobfuscationMapRunId"`
}

func New(configPath string, mapRunID string) (*Manifest, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read configuration for manifest: %w", err)
	}
	hash := sha256.Sum256(data)

	return &Manifest{
		Version:               CurrentVersion,
		Status:                StatusCompleted,
		ToolVersion:           version.GetVersion().Version,
		CompletedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		ConfigSHA256:          hex.EncodeToString(hash[:]),
		DeobfuscationMap:      PrivateMapFileName,
		DeobfuscationMapRunID: mapRunID,
	}, nil
}

func (m *Manifest) Write(directory string) error {
	if m == nil {
		return fmt.Errorf("cannot write a nil manifest")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("failed to create manifest directory: %w", err)
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed to encode manifest: %w", err)
	}
	path := filepath.Join(directory, FileName)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write manifest %s: %w", path, err)
	}
	return nil
}

func Read(directory string) (*Manifest, error) {
	path := filepath.Join(directory, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	result := &Manifest{}
	if err := yaml.Unmarshal(data, result); err != nil {
		return nil, fmt.Errorf("failed to decode manifest %s: %w", path, err)
	}
	if result.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported must-gather-clean manifest version %d, expected %d", result.Version, CurrentVersion)
	}
	if result.Status != StatusCompleted {
		return nil, fmt.Errorf("must-gather-clean manifest %s has unsupported status %q", path, result.Status)
	}
	return result, nil
}
