package deobfuscator

import (
	"github.com/openshift/must-gather-clean/pkg/obfuscator"
	"github.com/openshift/must-gather-clean/pkg/schema"
)

type Scope string

const (
	ScopeResponse Scope = "response"
)

type Capability struct {
	ResponseAvailable bool
	ResponseReasons   []string
}

func (c Capability) Available(scope Scope) bool {
	return scope == ScopeResponse && c.ResponseAvailable
}

func EvaluateCapability(config schema.SchemaJsonConfig, inputAlreadyCleaned bool, pipeMode bool) Capability {
	capability := Capability{ResponseAvailable: true}
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
		addReason(&capability.ResponseReasons, reason)
	}

	if pipeMode {
		responseUnavailable("pipe-mode")
	}
	if inputAlreadyCleaned {
		responseUnavailable("previously-cleaned-input")
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
