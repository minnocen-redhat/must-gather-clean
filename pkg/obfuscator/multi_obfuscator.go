package obfuscator

import (
	"sort"
	"strings"
)

type MultiObfuscator struct {
	entries []NamedReportingObfuscator
}

type NamedReportingObfuscator struct {
	Type       string
	Obfuscator ReportingObfuscator
	Reversible bool
}

func (m *MultiObfuscator) Path(s string) string {
	for index, entry := range m.entries {
		protected := m.protectPriorTokens(index, s)
		s = entry.Obfuscator.Path(protected.value)
		s = protected.restore(s)
	}

	return s
}

func (m *MultiObfuscator) Contents(s string) string {
	for index, entry := range m.entries {
		// A reversible replacement is a token, not ordinary input. Protect
		// tokens emitted by earlier stages while a later stage is processing
		// the string: later obfuscators must not rewrite a canonical-looking
		// part of an already generated token.
		protected := m.protectPriorTokens(index, s)
		s = entry.Obfuscator.Contents(protected.value)
		s = protected.restore(s)
	}

	return s
}

func (m *MultiObfuscator) protectPriorTokens(index int, value string) protectedValue {
	tokens := make(map[string]struct{})
	for _, entry := range m.entries[:index] {
		if !entry.Reversible {
			continue
		}
		for _, replacement := range entry.Obfuscator.Report().Replacements {
			// Run-scoped reversible tokens are deliberately namespaced. Legacy
			// replacements do not use this prefix and retain their old behavior.
			if strings.HasPrefix(replacement.ReplacedWith, "x-mgc1-") {
				tokens[replacement.ReplacedWith] = struct{}{}
			}
		}
	}
	list := make([]string, 0, len(tokens))
	for token := range tokens {
		if strings.Contains(value, token) {
			list = append(list, token)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if len(list[i]) != len(list[j]) {
			return len(list[i]) > len(list[j])
		}
		return list[i] < list[j]
	})

	placeholders := make([]tokenProtection, 0, len(list))
	protected := value
	for i, token := range list {
		placeholder := multiTokenPlaceholder(i)
		for strings.Contains(protected, placeholder) {
			placeholder = multiTokenPlaceholder(i + len(placeholders) + 1)
		}
		protected = strings.ReplaceAll(protected, token, placeholder)
		placeholders = append(placeholders, tokenProtection{token: token, placeholder: placeholder})
	}
	return protectedValue{value: protected, protections: placeholders}
}

type tokenProtection struct{ token, placeholder string }

type protectedValue struct {
	value       string
	protections []tokenProtection
}

func (p protectedValue) restore(value string) string {
	for _, protection := range p.protections {
		value = strings.ReplaceAll(value, protection.placeholder, protection.token)
	}
	return value
}

func multiTokenPlaceholder(index int) string {
	// Surround the marker with whitespace. Azure's path patterns require a
	// non-whitespace character in each captured component, so a protected
	// token cannot be mistaken for a subscription/resource name.
	return string([]byte{' ', 0, 3, byte(index >> 8), byte(index), 4, 0, ' '})
}

func (m *MultiObfuscator) Report() ReplacementReport {
	var replacements []Replacement
	for _, entry := range m.entries {
		report := entry.Obfuscator.Report()
		replacements = append(replacements, report.Replacements...)
	}

	return ReplacementReport{Replacements: replacements}
}

func (m *MultiObfuscator) ReportPerObfuscator() []ReplacementReport {
	var multiReport []ReplacementReport
	for i := range m.entries {
		multiReport = append(multiReport, m.entries[i].Obfuscator.Report())
	}

	return multiReport
}

func (m *MultiObfuscator) ReversibleReports() []ReversibleObfuscatorReport {
	reports := make([]ReversibleObfuscatorReport, len(m.entries))
	for i, entry := range m.entries {
		report := ReversibleObfuscatorReport{
			Type:              entry.Type,
			UnsupportedReason: "obfuscator is not configured for reversible replacement",
		}
		if entry.Reversible {
			reporter, ok := entry.Obfuscator.(ReversibleReporter)
			if !ok {
				report.UnsupportedReason = "obfuscator did not provide a reversible ledger"
			} else {
				report.Reversible = true
				report.Replacements = reporter.ReversibleReport()
			}
		}
		reports[i] = report
	}
	return reports
}

func NewMultiObfuscator(o []ReportingObfuscator) *MultiObfuscator {
	entries := make([]NamedReportingObfuscator, len(o))
	for i, value := range o {
		entries[i] = NamedReportingObfuscator{Obfuscator: value}
	}
	return NewNamedMultiObfuscator(entries)
}

func NewNamedMultiObfuscator(entries []NamedReportingObfuscator) *MultiObfuscator {
	return &MultiObfuscator{entries: entries}
}
