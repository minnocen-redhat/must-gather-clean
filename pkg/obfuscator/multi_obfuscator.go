package obfuscator

type MultiObfuscator struct {
	entries []NamedReportingObfuscator
}

type NamedReportingObfuscator struct {
	Type       string
	Obfuscator ReportingObfuscator
}

func (m *MultiObfuscator) Path(s string) string {
	for _, entry := range m.entries {
		s = entry.Obfuscator.Path(s)
	}

	return s
}

func (m *MultiObfuscator) Contents(s string) string {
	for _, entry := range m.entries {
		s = entry.Obfuscator.Contents(s)
	}

	return s
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
		reporter, reversible := entry.Obfuscator.(ReversibleReportingObfuscator)
		var replacements []ReversibleReplacement
		if reversible {
			replacements = reporter.ReversibleReport()
		}
		reports[i] = ReversibleObfuscatorReport{
			Type:              entry.Type,
			Reversible:        reversible,
			UnsupportedReason: "obfuscator did not provide a reversible ledger",
			Replacements:      replacements,
		}
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
