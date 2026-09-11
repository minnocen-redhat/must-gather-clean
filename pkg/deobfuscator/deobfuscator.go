package deobfuscator

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const processBufferSize = 32 * 1024

// Deobfuscate restores only unambiguous tokens. Ambiguous and unknown tokens
// are intentionally left untouched.
func (m *Map) Deobfuscate(input string) string {
	if m == nil || len(m.Rules) == 0 {
		return input
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
		return input
	}

	return strings.NewReplacer(arguments...).Replace(input)
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
	maxTokenLength := m.maxObfuscatedLength()
	if maxTokenLength == 0 {
		if _, err := io.Copy(output, input); err != nil {
			return fmt.Errorf("failed to copy support response: %w", err)
		}
		return nil
	}

	buffer := make([]byte, processBufferSize)
	pending := make([]byte, 0, processBufferSize+maxTokenLength)
	write := func(data []byte) error {
		if len(data) == 0 {
			return nil
		}
		if _, err := io.WriteString(output, m.Deobfuscate(string(data))); err != nil {
			return fmt.Errorf("failed to write deobfuscated support response: %w", err)
		}
		return nil
	}

	for {
		read, err := input.Read(buffer)
		if read > 0 {
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
