package obfuscator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHostnameObfuscatorReplacesKnownHostnamesOnly(t *testing.T) {
	obfuscator := NewHostnameObfuscator(
		[]string{"console.apps.example.com", "api.example.com"},
		NewSimpleTrackerWithTokenPrefix("x-mgc-v1-run-"),
	)

	output := obfuscator.Contents("url=https://CONSOLE.APPS.EXAMPLE.COM:6443/health other.example.com")
	assert.Contains(t, output, "https://x-mgc-v1-run-hostname-0000000001.invalid:6443/health")
	assert.Contains(t, output, "other.example.com")
	assert.NotContains(t, output, "CONSOLE.APPS.EXAMPLE.COM")
}
