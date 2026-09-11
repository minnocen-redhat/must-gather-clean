package obfuscator

// ReversibleReplacement is the private ledger entry used by the
// deobfuscation workflow. It is deliberately separate from the public
// ReplacementReport.
type ReversibleReplacement struct {
	Canonical    string
	ReplacedWith string
	Counter      map[string]uint
}

// ReversibleObfuscatorReport keeps the identity of the obfuscator together
// with its ledger. Keeping these values together avoids correlating a config
// slice with a separate report slice by position.
type ReversibleObfuscatorReport struct {
	Type              string
	Reversible        bool
	UnsupportedReason string
	Replacements      []ReversibleReplacement
}

// ReversibleReporter is implemented by obfuscators that can provide a
// complete and unambiguous ledger for their generated replacements.
type ReversibleReporter interface {
	ReversibleReport() []ReversibleReplacement
}

func (s *SimpleTracker) ReversibleReport() []ReversibleReplacement {
	s.lock.RLock()
	defer s.lock.RUnlock()

	result := make([]ReversibleReplacement, 0, len(s.mapping))
	for _, replacement := range s.mapping {
		if replacement.ReplacedWith == "" {
			continue
		}
		counter := make(map[string]uint, len(replacement.Counter))
		for original, count := range replacement.Counter {
			if count > 0 {
				counter[original] = count
			}
		}
		if len(counter) == 0 {
			continue
		}
		result = append(result, ReversibleReplacement{
			Canonical:    replacement.Canonical,
			ReplacedWith: replacement.ReplacedWith,
			Counter:      counter,
		})
	}
	return result
}

func reversibleReportFor(value interface{}) []ReversibleReplacement {
	reporter, ok := value.(ReversibleReporter)
	if !ok {
		return nil
	}
	return reporter.ReversibleReport()
}
