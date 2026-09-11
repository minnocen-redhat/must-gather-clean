package watermarking

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	version "github.com/openshift/must-gather-clean/pkg/version"
)

const (
	watermarkTimeLayout = "2006-01-02 15:04:05 -0700 MST"
	maxWatermarkSize    = 512
)

var watermarkVersionPattern = regexp.MustCompile(`^(unknown|v?[0-9]+(?:\.[0-9]+){1,2}(?:[-+][0-9A-Za-z.-]+)?)$`)

type WaterMarker interface {
	// WriteWaterMarkFile creates a watermark file in the specified path
	WriteWaterMarkFile(path string) error
}

type SimpleWaterMarker struct{}

func NewSimpleWaterMarker() *SimpleWaterMarker {
	return &SimpleWaterMarker{}
}

func (s *SimpleWaterMarker) WriteWaterMarkFile(path string) error {
	timestampUTC := time.Now().UTC().String()
	version := version.GetVersion().Version
	contents := fmt.Sprintf("%s\n%s\n", timestampUTC, version)
	err := os.WriteFile(filepath.Join(path, "watermark.txt"), []byte(contents), 0644)
	if err != nil {
		return fmt.Errorf("failed to create watermark file in output folder: %w", err)
	}
	return nil
}

// IsValidWatermarkFile reports whether path contains a watermark written by
// this tool. A watermark is intentionally small and consists of the UTC
// timestamp and the tool version emitted by SimpleWaterMarker.
func IsValidWatermarkFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to inspect input watermark: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxWatermarkSize {
		return false, nil
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("failed to read input watermark: %w", err)
	}
	return IsValidWatermarkContents(contents), nil
}

// IsValidWatermarkContents validates the stable format written by
// SimpleWaterMarker. The version check prevents an unrelated customer file
// containing a timestamp and arbitrary text from being treated as a prior
// cleaning run.
func IsValidWatermarkContents(contents []byte) bool {
	if len(contents) > maxWatermarkSize {
		return false
	}

	lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
	if len(lines) != 2 {
		return false
	}
	if _, err := time.Parse(watermarkTimeLayout, strings.TrimSpace(lines[0])); err != nil {
		return false
	}
	return watermarkVersionPattern.MatchString(strings.TrimSpace(lines[1]))
}
