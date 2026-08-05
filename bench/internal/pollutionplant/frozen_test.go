package pollutionplant

import (
	"encoding/json"
	"os"
	"testing"
)

// The RQ1 arms were planted from the imperative string. If it ever renders
// differently, those 432 episodes stop being comparable with anything run
// afterwards, and nothing else in the suite would notice: the archive holds
// the text as delivered, so it is the only witness.
func TestImperativeStringMatchesTheArchivedPlant(t *testing.T) {
	raw, err := os.ReadFile("../../results/knowledge-pollution/rq1-warehouse/checkable-wrong-haiku/planted.json")
	if err != nil {
		t.Skipf("archive not present: %v", err)
	}
	var planted struct {
		TreatmentID string `json:"treatment_id"`
		Text        string `json:"text"`
		Needle      string `json:"needle"`
	}
	if err := json.Unmarshal(raw, &planted); err != nil {
		t.Fatalf("parse archive: %v", err)
	}
	tr, err := TreatmentByID(planted.TreatmentID)
	if err != nil {
		t.Fatalf("the archived treatment id %q no longer resolves: %v", planted.TreatmentID, err)
	}
	if tr.Text != planted.Text {
		t.Errorf("the imperative string changed since the RQ1 arms ran.\n archived: %q\n current:  %q", planted.Text, tr.Text)
	}
	if tr.Needle != planted.Needle {
		t.Errorf("needle changed: archived %q, current %q", planted.Needle, tr.Needle)
	}
	if tr.Directive != DirectiveImperative {
		t.Errorf("the archived plant resolves to directive %q, want imperative", tr.Directive)
	}
}
