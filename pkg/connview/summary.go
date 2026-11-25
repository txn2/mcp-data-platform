package connview

import (
	"strings"
)

// Forms of the list_connections answer (#2813).
const (
	// FormSummary is every connection in a few fields: what it is called, its
	// kind, the first sentence of its description and whether it is
	// read-only, with the tools that take each kind under kind_tools.
	FormSummary = "summary"
	// FormDetail is one connection as the deployment describes it in full.
	FormDetail = "detail"
)

// summaryDescriptionLimit bounds the sentence a summary keeps of a
// description, so a deployment whose connections carry pages of usage rules
// still answers in a size every client reads.
const summaryDescriptionLimit = 200

// SummaryNote is what a summary says it is, and how to get the rest.
const SummaryNote = "Each connection is summarized: its description is the first sentence, and kind_tools names " +
	"the tools that take each kind. Call list_connections with connection set to a name for that connection's " +
	"full description and the knowledge pages that document it."

// Summarize reduces a full view to the summary form (#2813). In production a
// deployment's 56 to 68 connections came back as 39 to 88 KB, most of it
// per-connection usage rules needed only for the connection about to be used,
// and one client's result limit cut it off. toolsForKind names the tools that
// take each kind; nil names none.
func Summarize(out Output, toolsForKind func(kind string) []string) Output {
	entries := make([]Entry, len(out.Connections))
	for i, e := range out.Connections {
		e.Description = firstSentence(e.Description)
		e.KnowledgePages = nil
		entries[i] = e
	}
	out.Connections = entries
	out.Form = FormSummary
	out.Note = SummaryNote
	out.KindTools = kindTools(entries, toolsForKind)
	return out
}

// kindTools names the tools for each kind among entries, nil when none does.
func kindTools(entries []Entry, toolsForKind func(kind string) []string) map[string][]string {
	if toolsForKind == nil {
		return nil
	}
	out := map[string][]string{}
	for _, e := range entries {
		if _, done := out[e.Kind]; done {
			continue
		}
		if tools := toolsForKind(e.Kind); len(tools) > 0 {
			out[e.Kind] = tools
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Detail narrows a full view to the one connection named, by its name or the
// name a call binds it by. ok is false when the caller has no such connection,
// which is answered as one that does not exist whether it is hidden or absent.
func Detail(out Output, name string, toolsForKind func(kind string) []string) (Output, bool) {
	for _, e := range out.Connections {
		if e.Name != name && e.Connection != name {
			continue
		}
		return Output{
			Connections: []Entry{e},
			Count:       1,
			CallLimits:  callLimits([]Entry{e}),
			Form:        FormDetail,
			KindTools:   kindTools([]Entry{e}, toolsForKind),
		}, true
	}
	return Output{}, false
}

// Names lists the connections of a view by name, for a refusal naming the
// ones that exist.
func Names(out Output) []string {
	names := make([]string, 0, len(out.Connections))
	for _, e := range out.Connections {
		names = append(names, e.Name)
	}
	return names
}

// firstSentence is a description's first sentence, bounded.
func firstSentence(desc string) string {
	desc = strings.TrimSpace(desc)
	if i := strings.Index(desc, ". "); i >= 0 {
		desc = desc[:i+1]
	}
	if i := strings.IndexByte(desc, '\n'); i >= 0 {
		desc = strings.TrimSpace(desc[:i])
	}
	if r := []rune(desc); len(r) > summaryDescriptionLimit {
		desc = string(r[:summaryDescriptionLimit]) + "..."
	}
	return desc
}
