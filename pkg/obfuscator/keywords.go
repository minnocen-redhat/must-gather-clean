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
	byKeyword skiplist.MapI[string, int32]
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
		entries:   make([]keywordEntry, 0, len(replacements)),
		byKeyword: skiplist.NewSkipListMap[string, int32](skiplist.OrderedComparator[string]{}),
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
		matcher.byKeyword.Insert(keyword, entryIndex)
		matcher.first[keyword[0]] = true
		if len(keyword) > matcher.maxLength {
			matcher.maxLength = len(keyword)
		}
	}
	return matcher
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
	matched := -1
	matchedLength := 0
	for length := 1; length <= limit; length++ {
		entry, err := m.byKeyword.Get(input[start : start+length])
		if err == nil {
			matched = int(entry)
			matchedLength = length
		}
	}
	return matched, matchedLength
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
