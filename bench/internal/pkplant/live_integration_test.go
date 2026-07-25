package pkplant

// Live integration: plant every frozen seed into a running platform, in
// both delivery arms, and require each one to come back through the same
// search the agent will use. Skipped unless PK_LIVE_URL is set, so the
// module's own test run stays hermetic.
//
// This is the test that would have caught the failure mode nothing
// downstream can see: a belief that is stored but never delivered turns a
// knowledge cell into a no-knowledge control silently.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/pkseed"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

func TestPlantReachesTheAgent(t *testing.T) {
	base := os.Getenv("PK_LIVE_URL")
	if base == "" {
		t.Skip("set PK_LIVE_URL (and PK_LIVE_KEY) to run against a live pk stack")
	}
	tgt := target.Target{BaseURL: base, Credential: os.Getenv("PK_LIVE_KEY")}
	client := New(tgt, 150, lifecycleapi.New(base, tgt.HTTPClient(30*time.Second)), 30*time.Second)
	ctx := context.Background()

	probes := map[string]string{
		"perishable-absent":    "What is the volume and sentiment trend for ACME's listening monitors?",
		"perishable-present":   "What listening monitors does ACME have?",
		"durable-granularity":  "How do I get weekly impressions for an ACME owned profile?",
		"eternal-unique-reach": "How many distinct accounts did an ACME profile reach over a month?",
	}
	arms := map[string]pkseed.Metadata{
		"bare": {},
		"enriched": {
			Enriched: true, AsOf: pkseed.CaptureDate(),
			Now: pkseed.CaptureDate().AddDate(0, 0, 24), RecheckCalls: 1,
		},
	}
	seq := 0
	for _, s := range pkseed.Seeds() {
		for armName, meta := range arms {
			seq++
			t.Run(s.ID+"/"+armName, func(t *testing.T) {
				got, err := client.Plant(ctx, Request{
					Seed: s, Metadata: meta, Seq: seq, Probe: probes[s.BeliefID],
				})
				if err != nil {
					t.Fatalf("plant: %v", err)
				}
				if !got.Probed {
					t.Error("plant reported no reachability check")
				}
				if got.Text != pkseed.Delivered(s, meta) {
					t.Error("the stored text is not the delivered text")
				}
				if (armName == "enriched") != (got.Text != s.Text) {
					t.Errorf("arm %s delivered the wrong text shape", armName)
				}
			})
		}
	}
}
