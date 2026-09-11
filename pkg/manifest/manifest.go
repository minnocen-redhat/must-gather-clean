package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
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
	Version               int      `yaml:"version"`
	Status                string   `yaml:"status"`
	ToolVersion           string   `yaml:"toolVersion"`
	CompletedAt           string   `yaml:"completedAt"`
	ConfigSHA256          string   `yaml:"configSha256"`
	DeobfuscationMap      string   `yaml:"deobfuscationMap"`
	DeobfuscationMapRunID string   `yaml:"deobfuscationMapRunId"`
	DeobfuscationStatus   string   `yaml:"deobfuscationStatus"`
	DeobfuscationScope    string   `yaml:"deobfuscationScope"`
	DeobfuscationReasons  []string `yaml:"deobfuscationReasons,omitempty"`
}

func New(configPath string, mapRunID string, capabilities ...deobfuscator.Capability) (*Manifest, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read configuration for manifest: %w", err)
	}
	hash := sha256.Sum256(data)

	capability := deobfuscator.Capability{ResponseAvailable: true}
	if len(capabilities) > 0 {
		capability = capabilities[0]
	}
	status := "unavailable"
	scope := ""
	if capability.ResponseAvailable {
		status = "available"
		scope = string(deobfuscator.ScopeResponse)
	}
	mapName := ""
	if capability.ResponseAvailable {
		mapName = PrivateMapFileName
	}
	if !capability.ResponseAvailable {
		mapRunID = ""
	}
	return &Manifest{
		Version:               CurrentVersion,
		Status:                StatusCompleted,
		ToolVersion:           version.GetVersion().Version,
		CompletedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		ConfigSHA256:          hex.EncodeToString(hash[:]),
		DeobfuscationMap:      mapName,
		DeobfuscationMapRunID: mapRunID,
		DeobfuscationStatus:   status,
		DeobfuscationScope:    scope,
		DeobfuscationReasons:  append([]string{}, capability.ResponseReasons...),
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
	temporary, err := os.CreateTemp(directory, ".must-gather-clean-manifest-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary manifest: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("failed to secure temporary manifest: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("failed to write temporary manifest: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("failed to sync temporary manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("failed to close temporary manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("failed to publish manifest %s: %w", path, err)
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
