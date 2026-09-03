package deobfuscator

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Deobfuscate restores only unambiguous tokens. Ambiguous and unknown tokens
// are intentionally left untouched.
func (m *Map) Deobfuscate(input string) string {
	if m == nil || len(m.Rules) == 0 {
		return input
	}

	rules := append([]Rule(nil), m.Rules...)
	sort.SliceStable(rules, func(i, j int) bool {
		return len(rules[i].Obfuscated) > len(rules[j].Obfuscated)
	})

	arguments := make([]string, 0, len(rules)*2)
	for _, rule := range rules {
		if rule.Obfuscated == "" || rule.Original == "" {
			continue
		}
		arguments = append(arguments, rule.Obfuscated, rule.Original)
	}
	if len(arguments) == 0 {
		return input
	}

	return strings.NewReplacer(arguments...).Replace(input)
}

func Process(m *Map, input io.Reader, output io.Writer) error {
	if m == nil {
		return fmt.Errorf("deobfuscation map is nil")
	}
	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("failed to read support response: %w", err)
	}
	if _, err := io.WriteString(output, m.Deobfuscate(string(data))); err != nil {
		return fmt.Errorf("failed to write deobfuscated support response: %w", err)
	}
	return nil
}
