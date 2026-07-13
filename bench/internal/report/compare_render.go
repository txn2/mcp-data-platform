package report

import (
	"fmt"
	"strings"
)

// armLegend describes each arm for the report header.
var armLegend = map[string]string{
	"a0": "baseline — raw toolkit tools only, no enrichment, no search",
	"a1": "enrichment — A0 plus semantic cross-enrichment",
	"a2": "knowledge — A1 plus search, the search-first gate, and knowledge pages",
	"a3": "lifecycle — A2 plus memory/insight capture and apply_knowledge",
}

// Markdown renders the cross-arm comparison as a documentation page in the
// tuning-and-scaling house style: manifest, arm legend, accuracy and efficiency
// matrices, the S3 trap-class breakdown, headline deltas with CIs, and caveats.
func (c *Comparison) Markdown() string {
	var b strings.Builder
	b.WriteString("# Agent-effectiveness benchmark: arm comparison\n\n")
	c.writeManifest(&b)
	c.writeLegend(&b)
	c.writeOverall(&b)
	c.writeAccuracyMatrix(&b)
	c.writeEfficiencyMatrix(&b)
	c.writeTrapBreakdown(&b)
	c.writeDeltas(&b)
	c.writeCaveats(&b)
	return b.String()
}

// writeManifest renders the run manifest and a task-set consistency check.
func (c *Comparison) writeManifest(b *strings.Builder) {
	m := c.Manifests[c.Baseline]
	b.WriteString("## Manifest\n\n")
	fmt.Fprintf(b, "- Model: `%s` (%s)\n", m.Model, m.LLMProvider)
	fmt.Fprintf(b, "- Repeats (k): %d\n", m.K)
	fmt.Fprintf(b, "- Dataset seed: %d\n", m.Seed)
	fmt.Fprintf(b, "- Task-set hash: `%s`\n", short(m.TaskSetHash))
	fmt.Fprintf(b, "- Platform: %s @ commit `%s`\n", m.PlatformVersion, short(m.GitCommit))
	fmt.Fprintf(b, "- Arms: %s\n", strings.Join(c.Arms, ", "))
	if drift := c.taskSetDrift(); drift != "" {
		fmt.Fprintf(b, "\n> WARNING: arms did not run an identical task set (%s). The comparison is not apples-to-apples.\n", drift)
	}
	b.WriteString("\n")
}

// taskSetDrift returns a non-empty description when arms differ in task-set hash
// or model (either breaks the ablation's hold-everything-else-constant premise).
func (c *Comparison) taskSetDrift() string {
	base := c.Manifests[c.Baseline]
	var issues []string
	for _, arm := range c.Arms {
		m := c.Manifests[arm]
		if m.TaskSetHash != base.TaskSetHash {
			issues = append(issues, arm+" task-set hash differs")
		}
		if m.Model != base.Model {
			issues = append(issues, fmt.Sprintf("%s model %q != baseline %q", arm, m.Model, base.Model))
		}
	}
	return strings.Join(issues, "; ")
}

// writeLegend renders the arm legend for the arms present.
func (c *Comparison) writeLegend(b *strings.Builder) {
	b.WriteString("## Arms\n\n")
	for _, arm := range c.Arms {
		fmt.Fprintf(b, "- **%s** — %s\n", arm, armLegend[arm])
	}
	b.WriteString("\n")
}

// writeOverall renders the across-all-suites accuracy per arm.
func (c *Comparison) writeOverall(b *strings.Builder) {
	b.WriteString("## Overall accuracy\n\n")
	b.WriteString("Accuracy is over graded attempts; the bracket is the 95% bootstrap CI.\n\n")
	b.WriteString("| arm | graded | accuracy (95% CI) | pass^k | median calls | median wall (s) | harness fails |\n")
	b.WriteString("| --- | ---: | --- | ---: | ---: | ---: | ---: |\n")
	for _, cell := range c.Overall {
		fmt.Fprintf(b, "| %s | %d | %s | %.0f%% | %.0f | %.1f | %d |\n",
			cell.Arm, cell.Graded, accWithCI(cell), cell.PassKRate*100,
			cell.MedianToolCalls, float64(cell.MedianWallMS)/1000, cell.HarnessFailures)
	}
	b.WriteString("\n")
}

// writeAccuracyMatrix renders suites (rows) x arms (columns) of accuracy+CI.
func (c *Comparison) writeAccuracyMatrix(b *strings.Builder) {
	b.WriteString("## Accuracy by suite\n\n")
	writeMatrixHeader(b, "suite", c.Arms)
	for _, suite := range c.Suites {
		fmt.Fprintf(b, "| %s", suite)
		for _, cell := range c.SuiteCells[suite] {
			fmt.Fprintf(b, " | %s", accWithCI(cell))
		}
		b.WriteString(" |\n")
	}
	b.WriteString("\n")
}

// writeEfficiencyMatrix renders suites x arms of median tool calls.
func (c *Comparison) writeEfficiencyMatrix(b *strings.Builder) {
	b.WriteString("## Median tool calls by suite\n\n")
	b.WriteString("Fewer is better: efficiency is a first-class benchmark axis (BIRD's VES).\n\n")
	writeMatrixHeader(b, "suite", c.Arms)
	for _, suite := range c.Suites {
		fmt.Fprintf(b, "| %s", suite)
		for _, cell := range c.SuiteCells[suite] {
			fmt.Fprintf(b, " | %.0f", cell.MedianToolCalls)
		}
		b.WriteString(" |\n")
	}
	b.WriteString("\n")
}

// writeTrapBreakdown renders trap class x arm accuracy (the S3 headline).
func (c *Comparison) writeTrapBreakdown(b *strings.Builder) {
	if len(c.TrapClasses) == 0 {
		return
	}
	b.WriteString("## S3 knowledge-trap accuracy by class\n\n")
	b.WriteString("Each trap is answerable plausibly-but-wrongly without the knowledge layer.\n\n")
	writeMatrixHeader(b, "trap class", c.Arms)
	for _, class := range c.TrapClasses {
		fmt.Fprintf(b, "| %s", class)
		for _, cell := range c.TrapCells[class] {
			fmt.Fprintf(b, " | %s", accWithCI(cell))
		}
		b.WriteString(" |\n")
	}
	b.WriteString("\n")
}

// writeDeltas renders each non-baseline arm's per-suite accuracy delta.
func (c *Comparison) writeDeltas(b *strings.Builder) {
	if len(c.Deltas) == 0 {
		return
	}
	fmt.Fprintf(b, "## Accuracy delta vs %s (the platform's effect)\n\n", c.Baseline)
	b.WriteString("Points are (arm − baseline) accuracy; the bracket is the 95% bootstrap CI on the difference.\n\n")
	b.WriteString("| suite | arm | delta (points) | 95% CI |\n")
	b.WriteString("| --- | --- | ---: | --- |\n")
	for _, d := range c.Deltas {
		fmt.Fprintf(b, "| %s | %s | %+.1f | %+.0f to %+.0f |\n",
			d.Suite, d.Arm, d.Points*100, d.CILow*100, d.CIHigh*100)
	}
	b.WriteString("\n")
}

// writeCaveats renders the standing honesty section.
func (c *Comparison) writeCaveats(b *strings.Builder) {
	b.WriteString("## Caveats\n\n")
	b.WriteString("- Results are model-dependent; the headline is arm-vs-arm on a single pinned model, never model-vs-model.\n")
	b.WriteString("- The seed dataset is small by design (fixed seed, airgapped); absolute accuracies are not real-world estimates.\n")
	b.WriteString("- Judgment-call rubric items (required caveats) are scored separately by the pinned LLM judge; see the judge calibration report for its human-agreement rate.\n")
	b.WriteString("- CIs are percentile bootstrap over graded attempts with a fixed resampling seed, so they are reproducible but do not model task-selection variance.\n")
}

// writeMatrixHeader writes a `| label | arm | arm | ... |` header row.
func writeMatrixHeader(b *strings.Builder, label string, arms []string) {
	fmt.Fprintf(b, "| %s", label)
	for _, a := range arms {
		fmt.Fprintf(b, " | %s", a)
	}
	b.WriteString(" |\n| ---")
	for range arms {
		b.WriteString(" | ---")
	}
	b.WriteString(" |\n")
}

// accWithCI formats accuracy with its bootstrap CI, e.g. "83.3% [70–93]".
func accWithCI(cell Cell) string {
	if cell.Graded == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%% [%.0f–%.0f]", cell.Accuracy*100, cell.CILow*100, cell.CIHigh*100)
}

// HumanTable renders a terse cross-arm summary for a terminal.
func (c *Comparison) HumanTable() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cross-arm comparison (baseline %s)\n", c.Baseline)
	fmt.Fprintf(&b, "%-14s", "suite")
	for _, a := range c.Arms {
		fmt.Fprintf(&b, "  %-16s", a)
	}
	b.WriteString("\n")
	for _, suite := range c.Suites {
		fmt.Fprintf(&b, "%-14s", suite)
		for _, cell := range c.SuiteCells[suite] {
			fmt.Fprintf(&b, "  %-16s", accWithCI(cell))
		}
		b.WriteString("\n")
	}
	return b.String()
}
