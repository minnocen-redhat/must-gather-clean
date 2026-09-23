package deobfuscator

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/openshift/must-gather-clean/pkg/reporting"
	"gopkg.in/yaml.v3"
)

// Mapping contains the replacements from an obfuscation report.
type Mapping map[string]string

// LoadReport reads an obfuscation report and builds its reverse lookup.
func LoadReport(path string) (Mapping, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read report %s: %w", path, err)
	}

	var report reporting.Report
	if err := yaml.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("failed to parse report %s: %w", path, err)
	}
	return NewMapping(report.Replacements), nil
}

// NewMapping builds a reverse lookup from the replacement groups in a report.
func NewMapping(groups [][]reporting.Replacement) Mapping {
	mapping := make(Mapping)
	for _, group := range groups {
		for _, replacement := range group {
			if replacement.Canonical == "" || replacement.ReplacedWith == "" {
				continue
			}
			mapping[replacement.ReplacedWith] = replacement.Canonical
		}
	}
	return mapping
}

// Replace restores tokens in input. Unknown tokens are left unchanged.
func (m Mapping) Replace(input string) string {
	if len(m) == 0 {
		return input
	}

	keys := make([]string, 0, len(m))
	for token := range m {
		keys = append(keys, token)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})

	args := make([]string, 0, len(keys)*2)
	for _, token := range keys {
		args = append(args, token, m[token])
	}
	return strings.NewReplacer(args...).Replace(input)
}
