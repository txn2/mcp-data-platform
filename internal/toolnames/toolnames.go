// Package toolnames answers one question about a piece of written guidance:
// does it name a tool this deployment does not register?
//
// A tool rename or removal leaves stale names behind in text nothing
// recompiles -- the deployment's customized agent instructions above all, which
// every session reads. Two callers ask: the startup lint over the instructions
// in force at boot (pkg/platform), and the apply_knowledge agent_instructions
// sink, which refuses a promotion naming one (pkg/toolkits/knowledge).
package toolnames

import (
	"regexp"
	"strings"
)

// toolTokenPattern matches word-boundary snake_case tokens: a lowercase word
// followed by at least one underscore-delimited segment. It is the shape a tool
// name has, and the prefix test below is what decides whether a match is one.
var toolTokenPattern = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b`)

// floorToolPrefixes are tool-name prefixes recognized whether or not this
// deployment registers a tool carrying one, so a stale reference to a toolkit
// the deployment no longer runs at all is still reported: with the toolkit gone,
// nothing in the live inventory would put its prefix in the derived set.
//
// It is every family the platform ships tools under, including the families the
// retired names in scripts/retired-tools.txt belong to -- api_, manage_, memory_
// and save_, the four the six-prefix list this replaced was blind to.
//
// Prefixes that read as ordinary English are deliberately absent (list_, run_,
// show_): recognizing them would refuse prose over a token no one meant as a
// tool name.
var floorToolPrefixes = []string{
	"api_",
	"apply_",
	"capture_",
	"datahub_",
	"manage_",
	"memory_",
	"platform_",
	"s3_",
	"save_",
	"trino_",
}

// Unknown returns the tool-name-like tokens in text that this
// deployment does not register, de-duplicated and in first-appearance order.
//
// A token counts as tool-name-like when it is snake_case and its first
// underscore-delimited segment is one this deployment's own inventory uses, or
// one of floorToolPrefixes. Deriving most of the set from the inventory is what
// makes the check keep up with the platform: a deployment that registers
// api_discover puts "api_" in the set, so a stale api_list_endpoints is
// reported without anyone maintaining a list -- which the fixed six-prefix list
// this replaced could not do, leaving it blind to four of the fourteen names in
// scripts/retired-tools.txt.
//
// A token written as a member of something (`platform.save_state`) is not a tool
// reference and is skipped, so a rule naming a script host method is not read as
// naming a tool.
//
// registered is every tool name the deployment registers -- the toolkits' and
// the platform's own; see platform.RegisteredToolNames, since the toolkit
// registry alone omits the platform's.
func Unknown(text string, registered []string) []string {
	if text == "" {
		return nil
	}
	inv := newToolInventory(registered)

	var unknown []string
	seen := make(map[string]struct{})
	for _, at := range toolTokenPattern.FindAllStringIndex(text, -1) {
		token := text[at[0]:at[1]]
		memberOfSomething := at[0] > 0 && text[at[0]-1] == '.'
		if memberOfSomething || !inv.isStaleToolName(token) {
			continue
		}
		if _, dup := seen[token]; dup {
			continue
		}
		seen[token] = struct{}{}
		unknown = append(unknown, token)
	}
	return unknown
}

// toolInventory is what a deployment registers, indexed for the two questions a
// token asks of it: is this shaped like one of our tool names, and is it one we
// have.
type toolInventory struct {
	names    map[string]struct{}
	prefixes map[string]struct{}
}

// newToolInventory indexes the registered names, seeding the prefix set with
// the floor so a family this deployment no longer runs is still recognized.
func newToolInventory(registered []string) toolInventory {
	inv := toolInventory{
		names:    make(map[string]struct{}, len(registered)),
		prefixes: make(map[string]struct{}, len(registered)+len(floorToolPrefixes)),
	}
	for _, p := range floorToolPrefixes {
		inv.prefixes[p] = struct{}{}
	}
	for _, name := range registered {
		inv.names[name] = struct{}{}
		if p, ok := toolPrefix(name); ok {
			inv.prefixes[p] = struct{}{}
		}
	}
	return inv
}

// isStaleToolName reports whether a token reads as one of this deployment's
// tool names but is not one it registers.
func (inv toolInventory) isStaleToolName(token string) bool {
	p, ok := toolPrefix(token)
	if !ok {
		return false
	}
	if _, isPrefix := inv.prefixes[p]; !isPrefix {
		return false
	}
	_, isKnown := inv.names[token]
	return !isKnown
}

// toolPrefix returns a snake_case name's first segment including its trailing
// underscore ("api_" for "api_discover"), and false for a name with no interior
// underscore to split on.
func toolPrefix(name string) (string, bool) {
	i := strings.Index(name, "_")
	if i <= 0 || i == len(name)-1 {
		return "", false
	}
	return name[:i+1], true
}
