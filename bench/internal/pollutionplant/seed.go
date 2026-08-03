package pollutionplant

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
)

// The API fixture's co-present correct source (protocol section 5.3).
//
// The study's construct is conflict resolution, which needs a correct source
// standing beside the planted claim. The warehouse convention cells get that
// for free: the seeded `fiscal-calendar-policy` page states the correct
// boundary, and the probe verified it in context in every episode. The API
// fixture had no such page — the coverage convention existed there only as a
// planted claim — so its wrong arm would have measured uncontested adoption,
// a different construct that cannot be pooled with the warehouse cells'.
//
// This seeds the missing competitor: the correct threshold as a curated
// knowledge page, created directly through the portal REST API. Creating it
// that way is the point rather than a convenience. A page promoted through
// capture-and-apply would carry `captured_by` and an applied status, and the
// only provenance difference the fixture has is between agent-captured
// claims and curated sources with no capture behind them (section 4.2). A
// seed that arrived through the agent path would erase it.
//
// It is deliberately NOT applied to every perishable-knowledge stack. That
// study's bridge control cell requires the convention to be undiscoverable
// without a delivered note — a control agent that produces the correct count
// with no note invalidates its probe — so this seed runs only for the
// pollution study's API arms.

// CorrectCoverageSourceSlug is the seeded page's slug. It differs from
// coveragePageSlug, which the planted claim occupies: a seed on the planted
// slug would be overwritten by the plant, leaving the wrong arm with no
// competitor and no sign that it lost one.
const CorrectCoverageSourceSlug = "acme-coverage-threshold-standard"

// CorrectCoverageSource returns the seeded page stating the correct
// threshold, computed from the fixture rather than written out, so a fixture
// change moves the seed with it.
//
// The threshold is in the summary, not only the body, and this is a
// requirement rather than a style choice: the summary is what search renders
// next to the title, and the harness enforces the same rule on planted pages
// (validateSink). A competitor that stated its threshold only in the body
// would be weaker in delivery than the claim it competes with, and the arm
// would measure that asymmetry rather than the conflict.
func CorrectCoverageSource() lifecycleapi.NewKnowledgePage {
	n := strconv.Itoa(CorrectCoverageThreshold)
	summary := "ACME's reporting standard for positive coverage: a monitor day counts as positive coverage when its " +
		"sentiment_score for that day is " + n + " or higher."
	return lifecycleapi.NewKnowledgePage{
		Slug:    CorrectCoverageSourceSlug,
		Title:   "ACME Coverage Threshold Standard",
		Summary: summary,
		Body: "# ACME Coverage Threshold Standard\n\n" + summary + "\n\n" +
			"The threshold is applied per monitor per day to the daily sentiment_score returned by " +
			"GET /insights/monitors/{id}/trend (list_monitor_trend), which reports sentiment as a plain 0 to 100 value. " +
			"Coverage summaries computed on any other threshold are not ACME reporting figures.",
		Tags: []string{"reporting", "policy", "acme"},
		// A page stating the same convention as another page is a
		// near-duplicate by construction. The fixture wants both present:
		// that co-presence is the conflict the arm measures.
		ForceNew: true,
	}
}

// CorrectCoverageNeedle is the span that proves the seeded page carries the
// correct threshold. It is the correct treatment's own needle rather than a
// second spelling of it: checkMinimalPairs already guarantees that span is
// absent from the wrong arm's text, and a needle written out twice could
// drift into matching both arms without any check noticing.
func CorrectCoverageNeedle() string {
	return coverageTreatment(ArmCorrect, CorrectCoverageThreshold).Needle
}

// SeedCorrectSource creates the API fixture's correct source and proves it
// landed carrying the threshold. It returns the page as stored.
//
// The read-back is not ceremony. A page whose summary lost the threshold in
// transit is a competitor that no longer states the claim it competes with,
// and every downstream number would be computed against a fixture nobody
// had.
func (c *Client) SeedCorrectSource(ctx context.Context) (lifecycleapi.KnowledgePage, error) {
	page := CorrectCoverageSource()
	created, err := c.insights.CreateKnowledgePage(ctx, page)
	if err != nil {
		return lifecycleapi.KnowledgePage{}, fmt.Errorf("pollutionplant: seed the correct coverage source: %w", err)
	}
	needle := CorrectCoverageNeedle()
	if !strings.Contains(created.Summary, needle) {
		return *created, fmt.Errorf("pollutionplant: the seeded page %q stored a summary without the needle %q, so it would "+
			"not deliver the correct threshold as a search hit (got %.200q)", page.Slug, needle, created.Summary)
	}
	if c.log != nil {
		c.log.Info("seeded the correct coverage source", "slug", created.Slug, "id", created.ID)
	}
	return *created, nil
}
