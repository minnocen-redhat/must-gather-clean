package obfuscator

import (
	"sort"
	"strings"
)

const reversibleTokenPrefix = "x-mgc1-"

// reversibleTokenSource is an optional, indexed view of a replacement
// tracker. It lets the reversible pipeline locate tokens by scanning a value
// once, instead of checking every known token with strings.Contains.
//
// It deliberately is not part of ReplacementTracker: third-party and test
// trackers continue to work through MultiObfuscator's report-based fallback.
type reversibleTokenSource interface {
	replacementTokenLengths() []int
	hasReplacementToken(string) bool
}

func replacementTokenSource(tracker ReplacementTracker) reversibleTokenSource {
	source, _ := tracker.(reversibleTokenSource)
	return source
}

type reversibleTokenSourceProvider interface {
	reversibleTokenSource() reversibleTokenSource
}

// protectedTokensInValue finds known tokens by looking for the one stable
// prefix used by private reversible artifacts. The number of candidate lengths
// is bounded by the token formats, and does not grow with the number of
// replacements. The exact map lookup keeps this safe for arbitrary text that
// happens to contain the prefix.
func protectedTokensInValue(value string, sources []reversibleTokenSource) []string {
	if value == "" || len(sources) == 0 || !strings.Contains(value, reversibleTokenPrefix) {
		return nil
	}

	var lengths []int
	if len(sources) == 1 {
		lengths = sources[0].replacementTokenLengths()
	} else {
		lengthsSet := make(map[int]struct{})
		for _, source := range sources {
			for _, length := range source.replacementTokenLengths() {
				if length > 0 {
					lengthsSet[length] = struct{}{}
				}
			}
		}
		lengths = make([]int, 0, len(lengthsSet))
		for length := range lengthsSet {
			lengths = append(lengths, length)
		}
	}
	if len(lengths) == 0 {
		return nil
	}

	found := make(map[string]struct{})
	for offset := 0; offset < len(value); {
		relative := strings.Index(value[offset:], reversibleTokenPrefix)
		if relative < 0 {
			break
		}
		start := offset + relative
		for _, length := range lengths {
			if start+length > len(value) {
				continue
			}
			candidate := value[start : start+length]
			for _, source := range sources {
				if source.hasReplacementToken(candidate) {
					found[candidate] = struct{}{}
					break
				}
			}
		}
		offset = start + len(reversibleTokenPrefix)
	}

	result := make([]string, 0, len(found))
	for token := range found {
		result = append(result, token)
	}
	sort.Slice(result, func(i, j int) bool {
		if len(result[i]) != len(result[j]) {
			return len(result[i]) > len(result[j])
		}
		return result[i] < result[j]
	})
	return result
}
