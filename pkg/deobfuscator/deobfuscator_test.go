package deobfuscator

import (
	"bytes"
	"io"
	"testing"
)

func TestDeobfuscateUsesLongestOverlappingTokenFirst(t *testing.T) {
	privateMap := &Map{
		Version: CurrentMapVersion,
		Rules: []Rule{
			{Obfuscated: "token-long", Original: "long"},
			{Obfuscated: "token", Original: "short"},
		},
	}

	if got := privateMap.Deobfuscate("token-long token"); got != "long short" {
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
	privateMap := &Map{
		Version: CurrentMapVersion,
		Rules:   []Rule{{Original: "10.0.0.1", Obfuscated: "x-mgc1-run-tag-o1-x-ipv4-0000000001-x"}},
	}
	input := []byte("before x-mgc1-run-tag-o1-x-ipv4-0000000001-x after")
	var output bytes.Buffer
	err := Process(privateMap, &chunkReader{data: input, size: 3}, &output)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if got, want := output.String(), "before 10.0.0.1 after"; got != want {
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
	privateMap := &Map{Version: CurrentMapVersion, Rules: rules}
	input := []byte("prefix x-mgc1-run-tag-o1-token-a-a suffix\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var output bytes.Buffer
		if err := Process(privateMap, bytes.NewReader(input), &output); err != nil {
			b.Fatal(err)
		}
	}
}
