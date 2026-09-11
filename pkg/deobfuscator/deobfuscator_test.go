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
		Rules:   []Rule{{Original: "10.0.0.1", Obfuscated: "x-mgc-v1-run-o0001-x-ipv4-0000000001-x"}},
	}
	input := []byte("before x-mgc-v1-run-o0001-x-ipv4-0000000001-x after")
	var output bytes.Buffer
	err := Process(privateMap, &chunkReader{data: input, size: 3}, &output)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if got, want := output.String(), "before 10.0.0.1 after"; got != want {
		t.Fatalf("unexpected output %q, want %q", got, want)
	}
}
