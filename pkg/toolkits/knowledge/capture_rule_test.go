package knowledge

import (
	"regexp"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Knowledge is read by personas that cannot query every source, so the
// platform's own guidance never asks an agent to record a query's answer, and
// both the capture and the review guidance state the rule with its reason
// (#2128).
func TestGuidanceKeepsResultsAtTheirSource(t *testing.T) {
	if strings.Contains(toolkit.CaptureRoute, "what the query answers") {
		t.Errorf("the capture route asks for the answer: %q", toolkit.CaptureRoute)
	}
	if !strings.Contains(toolkit.CaptureRoute, "not the values it returned") {
		t.Errorf("the capture route does not exclude the values: %q", toolkit.CaptureRoute)
	}
	for name, text := range map[string]string{"capture": knowledgeCapturePrompt, "review": knowledgeApplyPrompt} {
		if !strings.Contains(text, "cannot query") {
			t.Errorf("the %s guidance does not say knowledge is wider-read than its sources", name)
		}
	}
	if !strings.Contains(knowledgeApplyPrompt, "**Strip data.**") {
		t.Error("the review guidance has no step that strips data before applying")
	}
	if !strings.Contains(knowledgeCapturePrompt, "Query results, metrics, aggregates") {
		t.Error("When NOT to Capture does not exclude query results")
	}
	// No example in the capture guidance is a derived statistic.
	stat := regexp.MustCompile(`\d+(\.\d+)?%`)
	for line := range strings.SplitSeq(knowledgeCapturePrompt, "\n") {
		if strings.Contains(line, "e.g.") && stat.MatchString(line) {
			t.Errorf("a capture example is a derived statistic: %q", line)
		}
	}
}
