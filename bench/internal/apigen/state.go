package apigen

import (
	"fmt"
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- deterministic fixture generation from a fixed seed is the point; crypto/rand would break reproducibility
	"math/rand"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/gen"
)

// Seed is the fixed RNG seed for the distractor state. The gold state is
// gen.Generate()'s world (its own fixed seed), so the study's gold surface
// serves exactly the report-1 dataset. Changing either seed changes ground
// truths; the committed task-set hash pins the current values.
const Seed = 1027

// distractorRowRange bounds the seeded row count per distractor resource:
// enough that a called distractor answers with plausible data, small
// enough that the full 356-resource state stays trivially in memory.
const (
	distractorRowsMin = 8
	distractorRowsMax = 24
)

// Row is one seeded distractor record. Values are JSON-representable
// (string, int64, bool); encoding/json sorts the keys, so dumps are
// deterministic.
type Row map[string]any

// State is the fixture world the service serves and ground truths derive
// from.
type State struct {
	// Dataset is the gold world: report 1's customers and orders.
	Dataset *gen.Dataset
	// Distractors holds seeded rows per distractor resource key, in
	// catalog resource order.
	Distractors map[string][]Row
}

// GenerateState builds the fixture state from the fixed seeds. Pure and
// deterministic: same output on every call.
func GenerateState(c *Catalog) *State {
	rng := rand.New(rand.NewSource(Seed)) // #nosec G404 -- deterministic fixture generation, not crypto
	s := &State{
		Dataset:     gen.Generate(),
		Distractors: make(map[string][]Row, len(c.Resources)),
	}
	for _, r := range c.Resources {
		s.Distractors[r.Key()] = distractorRows(rng, r)
	}
	return s
}

// statusPool is the distractor lifecycle vocabulary, weighted active-heavy.
var statusPool = []string{"active", "active", "active", "archived", "draft"}

// nameAdjectives seeds distractor display names.
var nameAdjectives = []string{"Primary", "Quarterly", "Regional", "Legacy", "Standard", "Priority", "Internal", "Seasonal", "Shared", "Provisional"}

// distractorRows seeds one resource's rows from the shared RNG stream.
// Resources are visited in catalog order, so the stream is deterministic.
func distractorRows(rng *rand.Rand, r Resource) []Row {
	n := distractorRowsMin + rng.Intn(distractorRowsMax-distractorRowsMin+1)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]Row, n)
	for i := range rows {
		row := Row{
			"id":         int64(i + 1),
			"name":       fmt.Sprintf("%s %s %d", nameAdjectives[rng.Intn(len(nameAdjectives))], r.Plural, i+1),
			"status":     statusPool[rng.Intn(len(statusPool))],
			"created_at": base.Add(time.Duration(rng.Intn(600*24)) * time.Hour).Format(time.RFC3339),
		}
		for _, f := range r.Fields {
			row[f.Name] = extraValue(rng, f)
		}
		rows[i] = row
	}
	return rows
}

// extraValue seeds one family-flavored field value by type.
func extraValue(rng *rand.Rand, f Field) any {
	switch f.Type {
	case "integer":
		return int64(rng.Intn(100000))
	case "boolean":
		return rng.Intn(2) == 0
	case "date-time":
		base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		return base.Add(time.Duration(rng.Intn(365*24)) * time.Hour).Format(time.RFC3339)
	default:
		return fmt.Sprintf("%s-%04d", f.Name, rng.Intn(10000))
	}
}
