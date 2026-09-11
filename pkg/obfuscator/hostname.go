package obfuscator

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

type hostnameObfuscator struct {
	ReplacementTracker
	pattern  *regexp.Regexp
	secret   string
	sequence uint64
}

// NewHostnameObfuscator replaces only the supplied, resource-discovered
// hostnames. It does not attempt to infer hostnames from arbitrary text.
func NewHostnameObfuscator(hostnames []string, tracker ReplacementTracker) ReportingObfuscator {
	return NewHostnameObfuscatorWithSecret(hostnames, tracker, "")
}

func NewHostnameObfuscatorWithSecret(hostnames []string, tracker ReplacementTracker, secret string) ReportingObfuscator {
	canonical := map[string]struct{}{}
	for _, hostname := range hostnames {
		hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
		if hostname != "" {
			canonical[hostname] = struct{}{}
		}
	}
	values := make([]string, 0, len(canonical))
	for hostname := range canonical {
		values = append(values, hostname)
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	if len(values) == 0 {
		return &hostnameObfuscator{ReplacementTracker: tracker}
	}
	quoted := make([]string, len(values))
	for i, hostname := range values {
		quoted[i] = regexp.QuoteMeta(hostname)
	}
	pattern := regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_.-])(` + strings.Join(quoted, "|") + `)([^A-Za-z0-9_.-]|$)`)
	return &hostnameObfuscator{ReplacementTracker: tracker, pattern: pattern, secret: secret}
}

func (o *hostnameObfuscator) Path(s string) string {
	return o.replace(s)
}

func (o *hostnameObfuscator) Contents(s string) string {
	return o.replace(s)
}

func (o *hostnameObfuscator) replace(input string) string {
	if o.pattern == nil {
		return input
	}
	return o.pattern.ReplaceAllStringFunc(input, func(match string) string {
		groups := o.pattern.FindStringSubmatch(match)
		if len(groups) != 4 {
			return match
		}
		canonical := strings.ToLower(strings.TrimSuffix(groups[2], "."))
		replacement := o.GenerateIfAbsent(canonical, canonical, 1, func() string {
			if o.secret != "" {
				hasher := hmac.New(sha256.New, []byte(o.secret))
				_, _ = hasher.Write([]byte(canonical))
				return "hostname-" + hex.EncodeToString(hasher.Sum(nil)[:10]) + ".invalid"
			}
			sequence := atomic.AddUint64(&o.sequence, 1)
			return fmt.Sprintf("hostname-%010d.invalid", sequence)
		})
		return groups[1] + replacement + groups[3]
	})
}

func (o *hostnameObfuscator) ReversibleReport() []ReversibleReplacement {
	return reversibleReportFor(o.ReplacementTracker)
}

var _ ReportingObfuscator = (*hostnameObfuscator)(nil)
