package deobfuscator

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	processBufferSize     = 32 * 1024
	runTokenPrefix        = "x-mgc1-"
	runTokenTagLength     = 24
	minimumRunTokenPrefix = len(runTokenPrefix) + runTokenTagLength + 1
)

var runTokenPattern = regexp.MustCompile(`x-mgc1-([0-9a-fA-F]{24})-`)

// runTagValidator detects tokens from a different reversible cleaning run
// while preserving streaming processing. The tail keeps enough bytes for a
// run-token prefix split across two reader chunks.
type runTagValidator struct {
	expected string
	tail     []byte
}

func newRunTagValidator(runID string) *runTagValidator {
	if len(runID) < runTokenTagLength {
		return &runTagValidator{}
	}
	return &runTagValidator{expected: strings.ToLower(runID[:runTokenTagLength])}
}

func (v *runTagValidator) check(data []byte) error {
	if v == nil || v.expected == "" {
		return nil
	}
	combined := make([]byte, 0, len(v.tail)+len(data))
	combined = append(combined, v.tail...)
	combined = append(combined, data...)
	for _, match := range runTokenPattern.FindAllStringSubmatch(string(combined), -1) {
		if strings.ToLower(match[1]) != v.expected {
			return fmt.Errorf("support response contains a token from a different cleaning run (run tag %s, map run tag %s)", strings.ToLower(match[1]), v.expected)
		}
	}
	keep := minimumRunTokenPrefix - 1
	if len(combined) > keep {
		combined = combined[len(combined)-keep:]
	}
	v.tail = append(v.tail[:0], combined...)
	return nil
}

// Deobfuscate restores only unambiguous tokens. Ambiguous and unknown tokens
// are intentionally left untouched.
func (m *Map) Deobfuscate(input string) string {
	replacer := m.newReplacer()
	if replacer == nil {
		return input
	}
	return replacer.Replace(input)
}

func (m *Map) newReplacer() *strings.Replacer {
	if m == nil || len(m.Rules) == 0 {
		return nil
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
		return nil
	}
	return strings.NewReplacer(arguments...)
}

func (m *Map) maxObfuscatedLength() int {
	maxLength := 0
	for _, rule := range m.Rules {
		if len(rule.Obfuscated) > maxLength {
			maxLength = len(rule.Obfuscated)
		}
	}
	return maxLength
}

func (m *Map) safePrefixLength(data []byte, maxTokenLength int) int {
	safeLength := len(data) - maxTokenLength
	if safeLength <= 0 {
		return 0
	}
	text := string(data)
	for _, rule := range m.Rules {
		token := rule.Obfuscated
		if token == "" {
			continue
		}
		for offset := 0; offset < safeLength; {
			index := strings.Index(text[offset:], token)
			if index < 0 {
				break
			}
			index += offset
			if index+len(token) > safeLength {
				safeLength = index
			}
			offset = index + 1
		}
		// A token may be incomplete at the end of the current input. Keep its
		// prefix in memory so a token split across reader chunks is restored.
		for start := max(0, safeLength-len(token)+1); start < safeLength; start++ {
			if strings.HasPrefix(token, text[start:]) {
				safeLength = start
				break
			}
		}
	}
	return safeLength
}

func Process(m *Map, input io.Reader, output io.Writer) error {
	if m == nil {
		return fmt.Errorf("deobfuscation map is nil")
	}
	validator := newRunTagValidator(m.RunID)
	replacer := m.newReplacer()
	maxTokenLength := m.maxObfuscatedLength()
	if maxTokenLength == 0 || replacer == nil {
		buffer := make([]byte, processBufferSize)
		for {
			read, err := input.Read(buffer)
			if read > 0 {
				if validationErr := validator.check(buffer[:read]); validationErr != nil {
					return validationErr
				}
				if _, writeErr := output.Write(buffer[:read]); writeErr != nil {
					return fmt.Errorf("failed to copy support response: %w", writeErr)
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return fmt.Errorf("failed to read support response: %w", err)
			}
		}
	}

	buffer := make([]byte, processBufferSize)
	pending := make([]byte, 0, processBufferSize+maxTokenLength)
	write := func(data []byte) error {
		if len(data) == 0 {
			return nil
		}
		if _, err := io.WriteString(output, replacer.Replace(string(data))); err != nil {
			return fmt.Errorf("failed to write deobfuscated support response: %w", err)
		}
		return nil
	}

	for {
		read, err := input.Read(buffer)
		if read > 0 {
			if validationErr := validator.check(buffer[:read]); validationErr != nil {
				return validationErr
			}
			pending = append(pending, buffer[:read]...)
			if len(pending) > maxTokenLength {
				safeLength := m.safePrefixLength(pending, maxTokenLength)
				if err := write(pending[:safeLength]); err != nil {
					return err
				}
				if safeLength > 0 {
					remaining := append([]byte(nil), pending[safeLength:]...)
					pending = remaining
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("failed to read support response: %w", err)
		}
	}
	if err := write(pending); err != nil {
		return err
	}
	return nil
}
