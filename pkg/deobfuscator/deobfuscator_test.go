package deobfuscator

import (
	"bytes"
	"io"
	"testing"
)

func TestDeobfuscateUsesLongestOverlappingTokenFirst(t *testing.T) {
	mapping := &Mapping{
		Rules: []Rule{
			{Obfuscated: "token-long", Original: "long"},
			{Obfuscated: "token", Original: "short"},
		},
	}

	if got := mapping.Deobfuscate("token-long token"); got != "long short" {
		t.Fatalf("unexpected deobfuscated value %q", got)
	}
}

type chunkReader struct {
	data []byte
	size int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.size
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func TestProcessRestoresTokensSplitAcrossReaderChunks(t *testing.T) {
	mapping := &Mapping{
		Rules: []Rule{{Original: "10.0.0.1", Obfuscated: "x-mgc1-run-tag-o1-x-ipv4-0000000001-x"}},
	}
	input := []byte("before x-mgc1-run-tag-o1-x-ipv4-0000000001-x after")
	var output bytes.Buffer
	err := Process(mapping, &chunkReader{data: input, size: 3}, &output)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if got, want := output.String(), "before 10.0.0.1 after"; got != want {
		t.Fatalf("unexpected output %q, want %q", got, want)
	}
}

func TestProcessRejectsTokenFromDifferentRun(t *testing.T) {
	mapping := &Mapping{
		RunID: "0123456789abcdef0123456789abcdef",
		Rules: []Rule{{
			Original:   "10.0.0.1",
			Obfuscated: "x-mgc1-0123456789abcdef01234567-o1-x-ipv4-0000000001-x",
		}},
	}
	input := bytes.NewBufferString("response x-mgc1-fedcba9876543210fedcba98-o1-x-ipv4-0000000001-x")
	var output bytes.Buffer

	err := Process(mapping, input, &output)
	if err == nil {
		t.Fatal("Process succeeded for a token from a different run")
	}
	if got := output.String(); got != "" {
		t.Fatalf("Process wrote output before rejecting the response: %q", got)
	}
}

func TestProcessAcceptsResponseWithoutTokensForRunScopedMapping(t *testing.T) {
	mapping := &Mapping{
		RunID: "0123456789abcdef0123456789abcdef",
		Rules: []Rule{{
			Original:   "10.0.0.1",
			Obfuscated: "x-mgc1-0123456789abcdef01234567-o1-x-ipv4-0000000001-x",
		}},
	}
	const want = "response without an obfuscation token"
	input := bytes.NewBufferString(want)
	var output bytes.Buffer

	if err := Process(mapping, input, &output); err != nil {
		t.Fatalf("Process rejected a response without tokens: %v", err)
	}
	if got := output.String(); got != want {
		t.Fatalf("unexpected output %q, want %q", got, want)
	}
}

func BenchmarkProcessWithManyRules(b *testing.B) {
	rules := make([]Rule, 256)
	for i := range rules {
		rules[i] = Rule{
			Original:   "original-value-" + string(rune('a'+i%26)),
			Obfuscated: "x-mgc1-run-tag-o1-token-" + string(rune('a'+i%26)) + "-" + string(rune('a'+i/26)),
		}
	}
	mapping := &Mapping{Rules: rules}
	input := []byte("prefix x-mgc1-run-tag-o1-token-a-a suffix\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var output bytes.Buffer
		if err := Process(mapping, bytes.NewReader(input), &output); err != nil {
			b.Fatal(err)
		}
	}
}
