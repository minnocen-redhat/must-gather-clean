package deobfuscator

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

func EvaluateCapability(inputAlreadyCleaned bool, pipeMode bool) Capability {
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
	return capability
}
