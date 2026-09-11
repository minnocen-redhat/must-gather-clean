package deobfuscator

import (
	"github.com/openshift/must-gather-clean/pkg/obfuscator"
	"github.com/openshift/must-gather-clean/pkg/schema"
)

type Scope string

const (
	ScopeResponse Scope = "response"
	ScopeComplete Scope = "complete"
)

type Capability struct {
	ResponseAvailable bool
	CompleteAvailable bool
	ResponseReasons   []string
	CompleteReasons   []string
}

func (c Capability) Available(scope Scope) bool {
	if scope == ScopeComplete {
		return c.CompleteAvailable
	}
	return c.ResponseAvailable
}

func EvaluateCapability(config schema.SchemaJsonConfig, inputAlreadyCleaned bool, pipeMode bool) Capability {
	capability := Capability{ResponseAvailable: true, CompleteAvailable: true}
	addReason := func(reasons *[]string, reason string) {
		for _, existing := range *reasons {
			if existing == reason {
				return
			}
		}
		*reasons = append(*reasons, reason)
	}
	responseUnavailable := func(reason string) {
		capability.ResponseAvailable = false
		capability.CompleteAvailable = false
		addReason(&capability.ResponseReasons, reason)
		addReason(&capability.CompleteReasons, reason)
	}
	completeUnavailable := func(reason string) {
		capability.CompleteAvailable = false
		addReason(&capability.CompleteReasons, reason)
	}

	if pipeMode {
		responseUnavailable("pipe-mode")
	}
	if inputAlreadyCleaned {
		responseUnavailable("previously-cleaned-input")
	}
	if len(config.Omit) > 0 {
		completeUnavailable("omitted-data")
	}

	for _, obfuscate := range config.Obfuscate {
		if !IsSupportedReversibleObfuscator(obfuscate) {
			responseUnavailable("unsupported-obfuscator:" + string(obfuscate.Type))
		}
	}
	return capability
}

func IsSupportedReversibleObfuscator(obfuscate schema.Obfuscate) bool {
	return obfuscator.IsReversibleConfiguration(obfuscate)
}
