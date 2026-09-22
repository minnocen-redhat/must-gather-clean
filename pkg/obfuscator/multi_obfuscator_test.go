package obfuscator

import (
	"strings"
	"testing"

	"github.com/openshift/must-gather-clean/pkg/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
)

type splitObfuscator struct {
	tracker ReplacementTracker
}

func (d *splitObfuscator) Path(input string) string {
	s := strings.SplitN(input, " ", 3)[2]
	if d.tracker != nil {
		d.tracker.GenerateIfAbsent(input, input, 1, func() string {
			return s
		})
	}
	return s
}

func (d *splitObfuscator) Contents(input string) string {
	s := strings.SplitN(input, " ", 2)[1]
	if d.tracker != nil {
		d.tracker.GenerateIfAbsent(input, input, 1, func() string {
			return s
		})
	}
	return s
}

func (d *splitObfuscator) Report() ReplacementReport {
	return d.tracker.Report()
}

func TestMultiObfuscationContents(t *testing.T) {
	mo := NewMultiObfuscator(
		[]ReportingObfuscator{
			&splitObfuscator{},
			&splitObfuscator{},
		})

	contents := mo.Contents("this must be split twice")
	assert.Equal(t, "be split twice", contents)
}

func TestMultiObfuscationPaths(t *testing.T) {
	mo := NewMultiObfuscator(
		[]ReportingObfuscator{
			&splitObfuscator{},
			&splitObfuscator{},
		})

	contents := mo.Path("this must be split twice or more?")
	assert.Equal(t, "twice or more?", contents)
}

func TestMultiObfuscationReport(t *testing.T) {
	mo := NewMultiObfuscator(
		[]ReportingObfuscator{
			&splitObfuscator{tracker: NewSimpleTracker()},
		})

	contents := mo.Contents("this must be split once")
	assert.Equal(t, "must be split once", contents)
	assert.Equal(t, map[string]string{"this must be split once": "must be split once"}, mo.Report().AsMap())
}

func TestMultiObfuscationReportShouldOverride(t *testing.T) {
	mo := NewMultiObfuscator(
		[]ReportingObfuscator{
			&NoopObfuscator{map[string]string{"a": "b"}},
			&NoopObfuscator{map[string]string{"a": "c"}},
		})

	assert.Equal(t, map[string]string{"a": "c"}, mo.Report().AsMap())
}

func TestMultiObfuscationReportMulti(t *testing.T) {
	mo := NewMultiObfuscator(
		[]ReportingObfuscator{
			&splitObfuscator{tracker: NewSimpleTracker()},
			&splitObfuscator{tracker: NewSimpleTracker()},
			&splitObfuscator{tracker: NewSimpleTracker()},
		})

	contents := mo.Contents("this must be split thrice")
	assert.Equal(t, "split thrice", contents)
	assert.Equal(t, map[string]string{
		"be split thrice":           "split thrice",
		"must be split thrice":      "be split thrice",
		"this must be split thrice": "must be split thrice"}, mo.Report().AsMap())

	perObfuscator := mo.ReportPerObfuscator()
	var reportsAsMap []map[string]string
	for _, val := range perObfuscator {
		reportsAsMap = append(reportsAsMap, val.AsMap())
	}
	assert.Equal(t, []map[string]string{
		{"this must be split thrice": "must be split thrice"},
		{"must be split thrice": "be split thrice"},
		{"be split thrice": "split thrice"}}, reportsAsMap)
}

func TestBuildConfiguredObfuscatorMarksConsistentReplacementReversible(t *testing.T) {
	configured, err := BuildConfiguredObfuscator(schema.Obfuscate{
		Type:            schema.ObfuscateTypeIP,
		ReplacementType: schema.ObfuscateReplacementTypeConsistent,
	}, BuildOptions{TokenPrefix: "x-mgc1-test-"})
	require.NoError(t, err)
	assert.True(t, configured.Reversible)
}

func TestBuildConfiguredObfuscatorLeavesLegacyConsistentReplacementNonReversible(t *testing.T) {
	configured, err := BuildConfiguredObfuscator(schema.Obfuscate{
		Type:            schema.ObfuscateTypeIP,
		ReplacementType: schema.ObfuscateReplacementTypeConsistent,
	}, BuildOptions{})
	require.NoError(t, err)
	assert.False(t, configured.Reversible)
}

func TestMultiObfuscatorProtectsReversibleTokensAcrossStages(t *testing.T) {
	const runPrefix = "x-mgc1-0123456789abcdef01234567-"
	ipTracker := NewSimpleTrackerWithTokenPrefix(runPrefix + "o1-")
	ip, err := NewIPObfuscator(schema.ObfuscateReplacementTypeConsistent, ipTracker)
	require.NoError(t, err)
	azureTracker := NewSimpleTrackerWithTokenPrefix(runPrefix + "o2-")
	azure, err := NewAzureResourceObfuscator(schema.ObfuscateReplacementTypeConsistent, azureTracker, ptr.To(1))
	require.NoError(t, err)
	multi := NewNamedMultiObfuscator([]NamedReportingObfuscator{
		{Type: "IP", Obfuscator: ip, Reversible: true},
		{Type: "AzureResources", Obfuscator: azure, Reversible: true},
	})

	// The Azure resource name deliberately contains the human-readable part of
	// the IP token. Without cross-stage protection Azure rewrites that part.
	input := "10.20.30.40 /subscriptions/10.20.30.40/resourceGroups/ipv4-0000000001/providers/Microsoft.Compute/virtualMachines/ipv4-0000000001"
	output := multi.Contents(input)
	assert.Contains(t, output, runPrefix+"o1-x-ipv4-0000000001-x",
		"the Azure stage must not rewrite the IP token emitted by the previous stage")

}

func TestProtectedTokensInValueUsesExactIndexedTokens(t *testing.T) {
	tracker := NewSimpleTrackerWithTokenPrefix("x-mgc1-0123456789abcdef01234567-o1-")
	one := tracker.GenerateIfAbsent("one", "one", 1, func() string { return "x-ipv4-0000000001-x" })
	two := tracker.GenerateIfAbsent("two", "two", 1, func() string { return "x-resource-calm-tiger" })
	source, ok := tracker.(reversibleTokenSource)
	require.True(t, ok)

	// The unknown suffix must not be treated as part of a token, while tokens
	// adjacent to ordinary text must still be found.
	value := "prefix " + "x-mgc1-0123456789abcdef01234567-o1-" + one[len("x-mgc1-0123456789abcdef01234567-o1-"):] + "x " + two + "suffix"
	assert.ElementsMatch(t, []string{one, two}, protectedTokensInValue(value, []reversibleTokenSource{source}))
}
