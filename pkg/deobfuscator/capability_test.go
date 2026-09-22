package deobfuscator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvaluateCapabilityAllowsResponseDeobfuscation(t *testing.T) {
	capability := EvaluateCapability(false, false)

	assert.True(t, capability.ResponseAvailable)
	assert.Empty(t, capability.ResponseReasons)
}

func TestEvaluateCapabilityRejectsReclean(t *testing.T) {
	capability := EvaluateCapability(true, false)

	assert.False(t, capability.ResponseAvailable)
	assert.Contains(t, capability.ResponseReasons, "previously-cleaned-input")
}

func TestEvaluateCapabilityRejectsPipeMode(t *testing.T) {
	capability := EvaluateCapability(false, true)
	assert.False(t, capability.ResponseAvailable)
	assert.Contains(t, capability.ResponseReasons, "pipe-mode")
}
