package scriptlayer

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	starlarkjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/scriptdate"
	"github.com/txn2/mcp-data-platform/internal/scriptxml"
)

// This file pins the shipped dialect contract against the environment a script
// actually gets.
//
// It exists because of #1414: the contract listed `sum` among the available
// built-ins and prescribed it as the fix for the most common script mistake,
// and `sum` was in neither the Starlark universe nor the platform's own
// predeclared set. A script written from the platform's own documentation was
// refused at save. Nothing failed, because nothing was checking — and the
// contract is served three ways (the tool description, `command=help`, and the
// built-in knowledge page), so one wrong name is wrong in three places.
//
// The check drives the real validator rather than inspecting a table: a name
// counts as available exactly when scriptrun.Validate resolves it.

// builtinListPattern captures the comma-separated names the contract advertises
// as the Starlark built-ins, up to the clause where the enumeration stops and
// the prose about methods begins.
var builtinListPattern = regexp.MustCompile(`(?s)The Starlark built-ins: (.*?), and the string, list and`)

// contractBuiltins are the names the contract enumerates as available.
func contractBuiltins(t *testing.T) []string {
	t.Helper()
	match := builtinListPattern.FindStringSubmatch(DialectContract)
	require.Len(t, match, 2, "the contract no longer enumerates the Starlark built-ins in the shape this test reads")

	var names []string
	for raw := range strings.SplitSeq(match[1], ",") {
		if name := strings.TrimSpace(strings.ReplaceAll(raw, "\n", " ")); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// TestDialectContract_EveryAdvertisedBuiltinResolves is the gate the missing
// `sum` walked through. Every name the contract lists has to be a name the
// validator accepts, or the platform is documenting code it will refuse.
func TestDialectContract_EveryAdvertisedBuiltinResolves(t *testing.T) {
	names := contractBuiltins(t)
	require.NotEmpty(t, names)

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			report := scriptrun.Validate("_ = " + name + "\n")
			assert.True(t, report.OK,
				"the contract advertises %q but the script environment has no such name: %+v",
				name, report.Findings)
		})
	}
}

// TestDialectContract_AdvertisesEveryPredeclaredName is the other direction: a
// global the platform adds and does not document is one no author will use.
func TestDialectContract_AdvertisesEveryPredeclaredName(t *testing.T) {
	for _, name := range scriptrun.PredeclaredNames {
		assert.Contains(t, DialectContract, name,
			"%q is in the script environment but the contract never mentions it", name)
	}
}

// TestDialectContract_DecimalGuidanceIsExecutable checks the one worked idiom
// the contract prescribes by name. It was the specific claim #1414 falsified,
// so it is asserted as code rather than as a substring.
func TestDialectContract_DecimalGuidanceIsExecutable(t *testing.T) {
	const idiom = `sum([float(r["total"]) for r in rows])`
	require.Contains(t, DialectContract, idiom, "the contract no longer prescribes this idiom")

	report := scriptrun.Validate("rows = [{\"total\": \"1.50\"}]\ntotal = " + idiom + "\n")
	assert.True(t, report.OK, "%+v", report.Findings)
}

// TestDialectContract_ExportStatesTheDocumentChoice pins the discriminator to
// the entry where the choice is actually made (#1476). The split between
// composing a whole document per run and refreshing one document's data region
// was stated only inside the platform.publish_data entry, one entry later than
// the decision: an author who had settled on export had no reason to read it,
// and wrote the scheduled dashboard that destroys its own layout edits every
// fire. Both entries have to carry it, or they drift back apart.
func TestDialectContract_ExportStatesTheDocumentChoice(t *testing.T) {
	_, rest, ok := strings.Cut(DialectContract, "  platform.export(")
	require.True(t, ok, "the contract no longer has a platform.export entry")
	entry, _, ok := strings.Cut(rest, "  platform.publish_data(")
	require.True(t, ok, "platform.export is no longer followed by platform.publish_data")

	assert.Contains(t, entry, "platform.publish_data",
		"the export entry does not name the data-region alternative, so an author who chose export never learns it exists")
	for _, want := range []string{"data region", "structure varies", "overwrites the current version"} {
		assert.Containsf(t, entry, want,
			"the export entry states no %q half of the discriminator", want)
	}
}

// moduleMemberPattern captures a `module.member` mention anywhere in the
// contract. The trailing boundary keeps `json.encode / json.decode` and
// `date.of, date.parse` both readable as mentions.
var moduleMemberPattern = regexp.MustCompile(`\b(json|xml|date)\.([a-z_]+)`)

// TestDialectContract_EveryAdvertisedModuleMemberExists is the member-level
// half of the check #1414 was missing. TestDialectContract_EveryAdvertisedBuiltinResolves
// covers the Starlark built-ins, and the validator resolves a global name, but
// Starlark resolves an attribute at run time: the contract could advertise
// `xml.parse` on a module that has only `decode` and nothing would fail until
// an author's run did.
func TestDialectContract_EveryAdvertisedModuleMemberExists(t *testing.T) {
	members := map[string]map[string]bool{
		"json": memberSet(starlarkjson.Module.Members),
		"xml":  memberSet(scriptxml.Module.Members),
		"date": memberSet(scriptdate.Module.Members),
	}

	matches := moduleMemberPattern.FindAllStringSubmatch(DialectContract, -1)
	require.NotEmpty(t, matches, "the contract no longer mentions any module member")
	seen := map[string]bool{}
	for _, m := range matches {
		module, member := m[1], m[2]
		if seen[module+"."+member] {
			continue
		}
		seen[module+"."+member] = true
		if !members[module][member] {
			t.Errorf("the contract advertises %s.%s but the %s module has no such member", module, member, module)
		}
	}
}

// TestDialectContract_AdvertisesEveryModuleMember is the other direction: a
// member the platform ships and does not document is one no author will use.
func TestDialectContract_AdvertisesEveryModuleMember(t *testing.T) {
	modules := map[string]starlark.StringDict{
		"json": starlarkjson.Module.Members,
		"xml":  scriptxml.Module.Members,
		"date": scriptdate.Module.Members,
	}
	for module, members := range modules {
		for member := range members {
			// Checked by hand rather than with assert.Contains so a
			// failure names the missing member instead of printing
			// the whole contract.
			if !strings.Contains(DialectContract, module+"."+member) {
				t.Errorf("%s.%s is in the script environment but the contract never mentions it", module, member)
			}
		}
	}
}

// memberSet reduces a module's members to the names it defines.
func memberSet(members starlark.StringDict) map[string]bool {
	out := make(map[string]bool, len(members))
	for name := range members {
		out[name] = true
	}
	return out
}

// TestDialectContract_ListsEveryReservedWord pins the reserved-word entry of
// WHAT IS NOT to the scanner (#1823): every keyword and reserved Python word the
// parser refuses as a name is in the list the help prints, and the list names
// no word the parser accepts.
func TestDialectContract_ListsEveryReservedWord(t *testing.T) {
	start := strings.Index(DialectContract, "a reserved word")
	end := strings.Index(DialectContract, "WHAT DETERMINISTIC MEANS HERE")
	require.Positive(t, start)
	require.Greater(t, end, start)
	entry := strings.Join(strings.Fields(DialectContract[start:end]), " ")
	listed := regexp.MustCompile(`an attribute: ([a-z, ]+)\.`).FindStringSubmatch(entry)
	require.Len(t, listed, 2, entry)

	var want []string
	for tok := syntax.AND; tok <= syntax.YIELD; tok++ {
		if w := tok.String(); !strings.Contains(w, " ") {
			want = append(want, w)
		}
	}
	assert.ElementsMatch(t, want, strings.Split(listed[1], ", "))
}
