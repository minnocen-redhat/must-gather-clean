package fsutil

import (
	"fmt"
	"strings"
)

// validatePrivateACLListing validates the owner-only ACL emitted by icacls.
// The first ACE is prefixed by the inspected path, while subsequent ACEs are
// indented. In both cases the account name is the final field before ":(".
func validatePrivateACLListing(path, output string, accountNames []string) error {
	ownerEntry := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Successfully processed") {
			continue
		}
		entryStart := strings.Index(line, ":(")
		if entryStart < 0 {
			continue
		}
		if !strings.Contains(line[entryStart:], "(F)") {
			return fmt.Errorf("private path %s has a non-owner ACL entry", path)
		}
		principal := strings.TrimSpace(line[:entryStart])
		matchesOwner := false
		for _, accountName := range accountNames {
			if accountName != "" && hasICACLSPrincipal(principal, accountName) {
				matchesOwner = true
				break
			}
		}
		if !matchesOwner {
			return fmt.Errorf("private path %s has a non-owner ACL entry", path)
		}
		ownerEntry = true
	}
	if !ownerEntry {
		return fmt.Errorf("private path %s has no owner full-control ACL entry", path)
	}
	return nil
}

func hasICACLSPrincipal(field, accountName string) bool {
	if strings.EqualFold(field, accountName) {
		return true
	}
	if len(field) <= len(accountName) || !strings.EqualFold(field[len(field)-len(accountName):], accountName) {
		return false
	}
	// On the first output line icacls separates the path and principal with
	// whitespace. Requiring that boundary avoids accepting another account
	// merely because its name has the current account as a suffix.
	separator := field[len(field)-len(accountName)-1]
	return separator == ' ' || separator == '\t'
}
