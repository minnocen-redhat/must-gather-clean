package deobfuscator

import (
	"bufio"
	"errors"
	"fmt"
	"io"
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
// Tokens with more than one canonical value are omitted because they cannot be
// restored safely.
func NewMapping(groups [][]reporting.Replacement) Mapping {
	mapping := make(Mapping)
	ambiguous := make(map[string]struct{})
	for _, group := range groups {
		for _, replacement := range group {
			if replacement.Canonical == "" || replacement.ReplacedWith == "" {
				continue
			}
			token := replacement.ReplacedWith
			if _, skipped := ambiguous[token]; skipped {
				continue
			}
			if previous, exists := mapping[token]; exists && previous != replacement.Canonical {
				delete(mapping, token)
				ambiguous[token] = struct{}{}
				continue
			}
			mapping[token] = replacement.Canonical
		}
	}
	return mapping
}

// Replace restores tokens in input. Unknown tokens are left unchanged.
func (m Mapping) Replace(input string) string {
	if len(m) == 0 {
		return input
	}

	return m.newReplacer().Replace(input)
}

// ReplaceReader restores tokens line by line. Tokens containing newlines use
// the whole-input fallback to preserve replacement behavior.
func (m Mapping) ReplaceReader(input io.Reader, output io.Writer) error {
	if len(m) == 0 {
		_, err := io.Copy(output, input)
		return err
	}

	replacer := m.newReplacer()
	for token := range m {
		if strings.Contains(token, "\n") {
			data, err := io.ReadAll(input)
			if err != nil {
				return err
			}
			return writeString(output, replacer.Replace(string(data)))
		}
	}

	reader := bufio.NewReader(input)
	writer := bufio.NewWriter(output)
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			if _, err := writer.WriteString(replacer.Replace(line)); err != nil {
				return err
			}
		}
		if readErr != nil {
			flushErr := writer.Flush()
			if errors.Is(readErr, io.EOF) {
				return flushErr
			}
			return errors.Join(readErr, flushErr)
		}
	}
}

func (m Mapping) newReplacer() *strings.Replacer {
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
	return strings.NewReplacer(args...)
}

func writeString(output io.Writer, value string) error {
	written, err := io.WriteString(output, value)
	if err != nil {
		return err
	}
	if written != len(value) {
		return io.ErrShortWrite
	}
	return nil
}
