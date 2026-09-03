package deobfuscator

import "testing"

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
