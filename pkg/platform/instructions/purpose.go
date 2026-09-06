package instructions

import "strings"

// PurposeNote returns the purpose section of the agent instructions (#1317):
// what the `purpose` argument on a data-access tool is for, and what a good one
// says. The platform advertises the argument on each gated tool's schema with a
// one-line description; this note is where the model learns the reasoning behind
// it, which is what makes the sentence it writes worth recording.
//
// gated and kinds are the boundary the note names instead of saying
// "data-access tools" (#1640): the gated set is configurable, so a model reading
// a category name cannot tell which of the tools in front of it is in it, and
// the platform's own set is the only place that boundary is written down.
//
// gated holds the tools the caller reaches that the set names, and kinds holds
// the connection kinds it gates wholesale. They are separate because listing a
// kind's tools one by one would bury the boundary: kind:mcp covers every tool an
// upstream MCP server proxies, which on a deployment with two gateway
// connections is more names than the platform's own data-access surface has, and
// they change when the upstream does. Both are already filtered to this caller's
// persona, and both being empty means the caller reaches no gated tool and
// should be given no note at all.
//
// required distinguishes the two deployments: with purpose.require on, a gated
// call that states none is refused and the note says so, so the model treats it
// as part of the call rather than as an optional courtesy. With require off the
// same guidance stands without the threat of a refusal, which would be a lie.
//
// The caller appends it as a runtime note only when the purpose argument is
// enabled.
func PurposeNote(required bool, gated, kinds []string) string {
	lines := []string{
		"Stating why you are calling:",
		"These tools take a `purpose` argument: " + purposeScope(gated, kinds) + ". Write one " +
			"sentence naming the wider task you are working on and why this call serves it — the " +
			"question behind the query, not a restatement of the arguments. It is recorded with the " +
			"call and read later by the people who own the data, so \"Checking whether Q3 revenue " +
			"fell in the western region for the board deck\" is useful and \"Running a SQL query\" " +
			"is not.",
		"- Do not repeat argument values in it, and never put personal data, credentials, or secrets in it.",
		"- Restate it when your task changes rather than carrying the first one through an unrelated question.",
	}
	if required {
		lines = append(lines,
			"- A call to one of these tools without a purpose is refused (PURPOSE_REQUIRED); retry the "+
				"same call with one rather than looking for another tool.")
	}
	lines = append(lines,
		"- The platform asks for one only on those. Stating a purpose on another tool is accepted "+
			"and recorded rather than refused, so a refusal naming `purpose` is never a reason to "+
			"abandon a call.")
	return strings.Join(lines, "\n")
}

// purposeScope renders the gated set as one phrase: the tools the configured set
// names, then a clause per kind it gates wholesale. A kind is written as the
// connection kind a caller sees in list_connections, so the sentence stays true
// after the upstream adds a tool and points at something the caller can look up.
func purposeScope(gated, kinds []string) string {
	parts := make([]string, 0, len(kinds)+1)
	if len(gated) > 0 {
		parts = append(parts, strings.Join(gated, ", "))
	}
	for _, kind := range kinds {
		parts = append(parts, "every tool served by a connection of kind `"+kind+"`")
	}
	return strings.Join(parts, ", plus ")
}
