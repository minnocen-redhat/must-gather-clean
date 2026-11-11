package reporting

import (
	"fmt"
	"sort"

	"github.com/openshift/must-gather-clean/pkg/obfuscator"
	"github.com/openshift/must-gather-clean/pkg/schema"
)

type Replacement struct {
	Canonical    string       `yaml:"canonical,omitempty"`
	ReplacedWith string       `yaml:"replacedWith,omitempty"`
	Occurrences  []Occurrence `yaml:"occurrences,omitempty"`
}

type Occurrence struct {
	Original string `yaml:"original,omitempty"`
	Count    uint   `yaml:"count,omitempty"`
}

type Report struct {
	Replacements [][]Replacement         `yaml:"replacements,omitempty"`
	Omissions    []string                `yaml:"omissions,omitempty"`
	Config       schema.SchemaJsonConfig `yaml:"config,omitempty"`
}

type Reporter interface {
	// WriteReport writes the final report into the given path, will create folders if necessary.
	WriteReport(path string) error

	// CollectOmitterReport collects the omitter's omission results.
	CollectOmitterReport(omitter []string)

	// CollectObfuscatorReport will call the Report method on the obfuscator and collect the individual obfuscation results.
	CollectObfuscatorReport(obfuscatorReport []obfuscator.ReplacementReport)
}

type SimpleReporter struct {
	replacements  [][]Replacement
	omissions     []string
	config        *schema.SchemaJson
	collectionErr error
}

var _ Reporter = (*SimpleReporter)(nil)

func (s *SimpleReporter) WriteReport(path string) error {
	report, err := s.buildReport()
	if err != nil {
		return err
	}
	return reportFilePublisher{}.Publish(path, report)
}

func (s *SimpleReporter) buildReport() (Report, error) {
	if s.collectionErr != nil {
		return Report{}, s.collectionErr
	}
	if s.config == nil {
		return Report{}, fmt.Errorf("cannot write report without a configuration")
	}
	if len(s.replacements) != len(s.config.Config.Obfuscate) {
		return Report{}, fmt.Errorf(
			"cannot write report: %d replacement groups for %d configured obfuscators",
			len(s.replacements), len(s.config.Config.Obfuscate),
		)
	}
	return Report{
		Replacements: s.replacements,
		Omissions:    s.omissions,
		Config:       s.config.Config,
	}, nil
}

func (s *SimpleReporter) CollectOmitterReport(report []string) {
	s.omissions = append(s.omissions, report...)
	sort.Strings(s.omissions)
}

func (s *SimpleReporter) CollectObfuscatorReport(obfuscatorReport []obfuscator.ReplacementReport) {
	for _, report := range obfuscatorReport {
		var replacements []Replacement
		for _, r := range report.Replacements {
			var occurrences []Occurrence
			for original, cnt := range r.Counter {
				occurrences = append(occurrences, Occurrence{
					Original: original,
					Count:    cnt,
				})
			}
			sortOccurrences(occurrences)
			replacements = append(replacements, Replacement{
				Canonical:    r.Canonical,
				ReplacedWith: r.ReplacedWith,
				Occurrences:  occurrences,
			})
		}
		sortReplacements(replacements)
		s.replacements = append(s.replacements, replacements)
	}

	if s.config == nil {
		s.setCollectionError(fmt.Errorf("cannot collect obfuscator replacements without a configuration"))
		return
	}
	if len(s.replacements) != len(s.config.Config.Obfuscate) {
		s.setCollectionError(fmt.Errorf(
			"collected %d replacement groups for %d configured obfuscators",
			len(s.replacements), len(s.config.Config.Obfuscate),
		))
		return
	}
	for i := range s.config.Config.Obfuscate {
		if s.config.Config.Obfuscate[i].Replacement == nil {
			s.config.Config.Obfuscate[i].Replacement = map[string]string{}
		}
		doneReplacements := s.replacements[i]
		for _, replacement := range doneReplacements {
			for _, oc := range replacement.Occurrences {
				s.config.Config.Obfuscate[i].Replacement[oc.Original] = replacement.ReplacedWith
			}
		}
	}
}

func (s *SimpleReporter) setCollectionError(err error) {
	if s.collectionErr == nil {
		s.collectionErr = err
	}
}

func NewSimpleReporter(config *schema.SchemaJson) Reporter {
	return &SimpleReporter{
		replacements: [][]Replacement{},
		omissions:    []string{},
		config:       config,
	}
}

func sortReplacements(replacements []Replacement) {
	sort.SliceStable(replacements, func(first, second int) bool {
		if replacements[first].Canonical != replacements[second].Canonical {
			return replacements[first].Canonical < replacements[second].Canonical
		}
		return replacements[first].ReplacedWith < replacements[second].ReplacedWith
	})
}
func sortOccurrences(occurrences []Occurrence) {
	sort.SliceStable(occurrences, func(first, second int) bool {
		if occurrences[first].Original != occurrences[second].Original {
			return occurrences[first].Original < occurrences[second].Original
		}
		return occurrences[first].Count < occurrences[second].Count
	})
}
