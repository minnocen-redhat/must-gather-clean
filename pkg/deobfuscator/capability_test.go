package deobfuscator

import (
	"testing"

	"github.com/openshift/must-gather-clean/pkg/schema"
	"github.com/stretchr/testify/assert"
)

func TestEvaluateCapabilityAllowsResponseDeobfuscationWithOmissions(t *testing.T) {
	capability := EvaluateCapability(schema.SchemaJsonConfig{
		Obfuscate: []schema.Obfuscate{{
			Type:            schema.ObfuscateTypeIP,
			ReplacementType: schema.ObfuscateReplacementTypeConsistent,
		}},
		Omit: []schema.Omit{{Type: schema.OmitTypeFile}},
	}, false, false)

	assert.True(t, capability.ResponseAvailable)
	assert.False(t, capability.CompleteAvailable)
	assert.Contains(t, capability.CompleteReasons, "omitted-data")
}

func TestEvaluateCapabilityRejectsRecleanAndUnsupportedObfuscators(t *testing.T) {
	capability := EvaluateCapability(schema.SchemaJsonConfig{
		Obfuscate: []schema.Obfuscate{{Type: schema.ObfuscateTypeKeywords}},
	}, true, false)

	assert.False(t, capability.ResponseAvailable)
	assert.False(t, capability.CompleteAvailable)
	assert.Contains(t, capability.ResponseReasons, "previously-cleaned-input")
	assert.Contains(t, capability.ResponseReasons, "unsupported-obfuscator:Keywords")
}

func TestEvaluateCapabilityRejectsPipeMode(t *testing.T) {
	capability := EvaluateCapability(schema.SchemaJsonConfig{}, false, true)
	assert.False(t, capability.ResponseAvailable)
	assert.Contains(t, capability.ResponseReasons, "pipe-mode")
}
