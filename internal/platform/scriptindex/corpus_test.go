package scriptindex

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestCorpus_IsTheCardThenTheSource(t *testing.T) {
	sc := &script.Script{Name: "daily", Description: "Daily sales.", Enabled: true, Status: script.StatusActive}
	assert.Equal(t, script.IndexText(sc), Corpus(sc), "no source, the card alone")
	sc.Source = "# churn counts a customer gone 90 days\n"
	assert.Equal(t, script.IndexText(sc)+"\n\n"+sc.Source, Corpus(sc))
}

func TestChunks_TheWholeSourceReachesTheModelWithinTheBudget(t *testing.T) {
	var src strings.Builder
	_, _ = src.WriteString("load(\"lib:helpers@1\", \"fmt\")\n\n")
	for i := range 40 {
		_, _ = fmt.Fprintf(&src, "# step %d explains why the churn window is ninety days\n", i)
		_, _ = fmt.Fprintf(&src, "def step_%d():\n    return platform.query(\"SELECT * FROM sales.orders_%d\", connection=\"warehouse\")\n\n", i, i)
	}
	sc := &script.Script{Name: "churn", DisplayName: "Churn report", Description: "Weekly churn.", Enabled: true, Status: script.StatusActive, Source: src.String()}
	const budget = 400
	chunks := Chunks(sc, budget)
	require.Greater(t, len(chunks), 2)
	assert.Equal(t, script.IndexText(sc), chunks[0], "the card is the first chunk")
	var joined strings.Builder
	for i, c := range chunks {
		assert.LessOrEqual(t, len(c), budget, "chunk %d is over the input", i)
		if i > 0 {
			require.True(t, strings.HasPrefix(c, "Churn report\n"), "a source chunk carries the title")
			_, _ = joined.WriteString(strings.TrimPrefix(c, "Churn report\n"))
		}
	}
	assert.Equal(t, sc.Source, joined.String(), "every byte of the source is in some chunk")
	assert.Contains(t, chunks[2], "# step", "a comment opens the block with the code it explains")
}

func TestChunks_SmallAndUnusableBudgets(t *testing.T) {
	sc := &script.Script{Name: "tiny", Enabled: true, Status: script.StatusActive}
	assert.Equal(t, []string{script.IndexText(sc)}, Chunks(sc, 6000), "no source, one chunk")
	sc.Source = "def main():\n    pass\n"
	assert.Equal(t, []string{script.IndexText(sc), "tiny\n" + sc.Source}, Chunks(sc, 6000))
	assert.Equal(t, []string{Corpus(sc)}, Chunks(sc, 8), "a budget too small to split is not split")

	long := &script.Script{Name: strings.Repeat("n", 300), Enabled: true, Status: script.StatusActive, Source: strings.Repeat("x = 1\n", 100)}
	for _, c := range Chunks(long, 200) {
		assert.LessOrEqual(t, len(c), 200, "a title too long to head each chunk is left off")
	}
}

func TestStarlarkBlocks_IsLossless(t *testing.T) {
	src := "load(\"lib:a@1\", \"x\")\n# not after a blank line\nx = 1\n\n# explains main\ndef main():\n    # indented comment\n    pass\n"
	blocks := starlarkBlocks(src)
	assert.Equal(t, src, strings.Join(blocks, ""))
	assert.Equal(t, []string{"load(\"lib:a@1\", \"x\")\n# not after a blank line\nx = 1\n\n", "# explains main\ndef main():\n    # indented comment\n    pass\n"}, blocks)
}
