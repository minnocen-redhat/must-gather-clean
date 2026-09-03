package obfuscator

import (
	"strings"

	"github.com/openshift/must-gather-clean/pkg/schema"
)

type exactObfuscator struct {
	ReplacementTracker
	exactReplacements []ExactReplacement
}

type ExactReplacement struct {
	Original    string
	Replacement string
}

func (r *exactObfuscator) Path(s string) string {
	return r.replace(s)
}

func (r *exactObfuscator) Contents(s string) string {
	return r.replace(s)
}

func (r *exactObfuscator) replace(input string) string {
	output := input
	for _, e := range r.exactReplacements {
		count := uint(strings.Count(output, e.Original))
		if count > 0 {
			r.GenerateIfAbsent(e.Original, e.Original, count, func() string {
				return e.Replacement
			})
		}
		output = strings.ReplaceAll(output, e.Original, e.Replacement)
	}

	return output
}

func NewExactReplacementObfuscator(exactReplacements []schema.ObfuscateExactReplacementsElem, tracker ReplacementTracker) ReportingObfuscator {
	ret := &exactObfuscator{
		ReplacementTracker: tracker,
	}
	for _, curr := range exactReplacements {
		ret.exactReplacements = append(ret.exactReplacements, ExactReplacement{
			Original:    curr.Original,
			Replacement: curr.Replacement,
		})
	}

	return ret
}
