package instructions

import "strings"

// PurposeNote returns the purpose section of the agent instructions (#1317):
// what the `purpose` argument on a data-access tool is for, and what a good one
// says. The platform advertises the argument on each gated tool's schema with a
// one-line description; this note is where the model learns the reasoning behind
// it, which is what makes the sentence it writes worth recording.
//
// required distinguishes the two deployments: with purpose.require on, a gated
// call that states none is refused and the note says so, so the model treats it
// as part of the call rather than as an optional courtesy. With require off the
// same guidance stands without the threat of a refusal, which would be a lie.
//
// The caller appends it as a runtime note only when the purpose argument is
// enabled.
func PurposeNote(required bool) string {
	lines := []string{
		"Stating why you are calling:",
		"Data-access tools take a `purpose` argument. Write one sentence naming the wider task you " +
			"are working on and why this call serves it — the question behind the query, not a " +
			"restatement of the arguments. It is recorded with the call and read later by the people " +
			"who own the data, so \"Checking whether Q3 revenue fell in the western region for the " +
			"board deck\" is useful and \"Running a SQL query\" is not.",
		"- Do not repeat argument values in it, and never put personal data, credentials, or secrets in it.",
		"- Restate it when your task changes rather than carrying the first one through an unrelated question.",
	}
	if required {
		lines = append(lines,
			"- A call to one of these tools without a purpose is refused (PURPOSE_REQUIRED); retry the "+
				"same call with one rather than looking for another tool.")
	}
	return strings.Join(lines, "\n")
}
