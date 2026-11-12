package obfuscator

import (
	"strings"

	"github.com/thomasjungblut/go-sstables/skiplist"
)

type keywordsObfuscator struct {
	ReplacementTracker
	matcher *keywordMatcher
}

type keywordEntry struct {
	text        string
	replacement string
	// generator is separate so the hot path can write replacement directly.
	generator GenerateReplacement
}

type keywordMatcher struct {
	entries   []keywordEntry
	prefixes  skiplist.MapI[string, int32]
	first     [256]bool
	maxLength int
}

func (o *keywordsObfuscator) Path(name string) string {
	return o.matcher.replace(name, o.ReplacementTracker)
}

func (o *keywordsObfuscator) Contents(contents string) string {
	return o.matcher.replace(contents, o.ReplacementTracker)
}

func newKeywordMatcher(replacements map[string]string) *keywordMatcher {
	matcher := &keywordMatcher{
		entries:  make([]keywordEntry, 0, len(replacements)),
		prefixes: skiplist.NewSkipListMap[string, int32](skiplist.OrderedComparator[string]{}),
	}
	for keyword, replacement := range replacements {
		if keyword == "" {
			continue
		}
		entryIndex := int32(len(matcher.entries))
		matcher.entries = append(matcher.entries, keywordEntry{
			text:        keyword,
			replacement: replacement,
			generator: func() string {
				return replacement
			},
		})
		matcher.prefixes.Insert(keyword, entryIndex)
		matcher.first[keyword[0]] = true
		if len(keyword) > matcher.maxLength {
			matcher.maxLength = len(keyword)
		}
	}
	matcher.buildPrefixes()
	return matcher
}

// buildPrefixes adds nonterminal prefixes missing from prefixes. Each gets the
// nearest terminal entry on its path; terminal keywords were inserted first.
func (m *keywordMatcher) buildPrefixes() {
	for index := range m.entries {
		keyword := m.entries[index].text
		inherited := int32(-1)
		for length := 1; length <= len(keyword); length++ {
			prefix := keyword[:length]
			if entry, err := m.prefixes.Get(prefix); err == nil {
				inherited = entry
				continue
			}
			m.prefixes.Insert(prefix, inherited)
		}
	}
}

// matchAt returns the entry index and byte length of the longest keyword
// beginning at start, or -1 if no keyword begins there.
func (m *keywordMatcher) matchAt(input string, start int) (int, int) {
	if !m.first[input[start]] {
		return -1, 0
	}

	limit := len(input) - start
	if limit > m.maxLength {
		limit = m.maxLength
	}

	best := int32(-1)
	known := 0
	probe := 1
	for probe <= limit {
		entry, err := m.prefixes.Get(input[start : start+probe])
		if err != nil {
			break
		}
		best = entry
		known = probe
		probe *= 2
	}

	if known == limit {
		return m.result(best)
	}

	low := known + 1
	high := probe - 1
	if high > limit {
		high = limit
	}
	for low <= high {
		middle := low + (high-low)/2
		entry, err := m.prefixes.Get(input[start : start+middle])
		if err != nil {
			high = middle - 1
			continue
		}
		best = entry
		low = middle + 1
	}
	return m.result(best)
}

func (m *keywordMatcher) result(entry int32) (int, int) {
	if entry < 0 {
		return -1, 0
	}
	return int(entry), len(m.entries[entry].text)
}

// replace performs one literal, byte-oriented, case-sensitive, non-overlapping
// pass over the input. At each position the longest keyword wins. Replacement
// text is not scanned again during this pass.
func (m *keywordMatcher) replace(input string, reporter ReplacementTracker) string {
	if len(m.entries) == 0 {
		return input
	}

	const inlineCounts = 8
	var countStorage [inlineCounts]uint
	var counts []uint
	var output strings.Builder

	for index := 0; index < len(input); {
		entryIndex, matchedLength := m.matchAt(input, index)
		if entryIndex < 0 {
			if counts != nil {
				output.WriteByte(input[index])
			}
			index++
			continue
		}

		if counts == nil {
			if len(m.entries) <= inlineCounts {
				counts = countStorage[:len(m.entries)]
			} else {
				counts = make([]uint, len(m.entries))
			}
			output.Grow(len(input))
			output.WriteString(input[:index])
		}

		entry := &m.entries[entryIndex]
		output.WriteString(entry.replacement)
		counts[entryIndex]++
		index += matchedLength
	}

	if counts == nil {
		return input
	}
	for index, count := range counts {
		if count == 0 {
			continue
		}
		entry := &m.entries[index]
		_ = reporter.GenerateIfAbsent(entry.text, entry.text, count, entry.generator)
	}
	return output.String()
}

// NewKeywordsObfuscator returns an Obfuscator which replaces all occurrences
// of keys in the map passed to it with the corresponding values.
func NewKeywordsObfuscator(replacements map[string]string) ReportingObfuscator {
	return &keywordsObfuscator{
		ReplacementTracker: NewSimpleTrackerMap(replacements),
		matcher:            newKeywordMatcher(replacements),
	}
}
