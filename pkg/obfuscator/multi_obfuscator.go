package obfuscator

type MultiObfuscator struct {
	entries []NamedReportingObfuscator
}

type NamedReportingObfuscator struct {
	Type       string
	Obfuscator ReportingObfuscator
	Reversible bool
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
