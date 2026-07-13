package lifecycle

import (
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/grade"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// abstainMarkers are the phrases that count as an explicit abstention. An
// abstain episode asks about a fact that was never taught; a correct outcome is
// the agent stating it cannot answer, so the grader requires one of these
// markers on the FINAL ANSWER line. Any concrete number or entity instead is a
// fabrication. The protocol prompts instruct the agent to answer exactly
// "INSUFFICIENT INFORMATION" when it cannot ground the answer, so the canonical
// marker is first; the rest tolerate natural phrasings.
var abstainMarkers = []string{
	"insufficient information",
	"insufficient data",
	"not enough information",
	"no information",
	"cannot determine",
	"cannot be determined",
	"can't determine",
	"unable to determine",
	"do not know",
	"don't know",
	"not able to answer",
	"cannot answer",
	"can't answer",
	"unknown",
}

// gradeRecall scores a recall/transfer answer with the deterministic graders,
// reusing the task graders (numeric tolerance, entity alias) so a lifecycle
// recall is graded exactly as an S1-S3 question. exec_sql is rejected at load
// time, so only numeric and entity reach here.
func gradeRecall(finalAnswer string, g task.Grading) bool {
	final := grade.ExtractFinal(finalAnswer)
	switch g.Kind {
	case task.GradeNumeric:
		_, _, correct := grade.Numeric(final, *g.Value, g.AbsTolerance)
		return correct
	case task.GradeEntity:
		_, correct := grade.Entity(final, g.Aliases, g.WrongAliases)
		return correct
	default:
		return false
	}
}

// gradedValue returns the numeric value the grader parsed from the answer and
// whether one was present; used to confirm a post-update recall does not return
// the superseded value.
func gradedNumeric(finalAnswer string, g task.Grading) (float64, bool) {
	if g.Kind != task.GradeNumeric || g.Value == nil {
		return 0, false
	}
	got, ok, _ := grade.Numeric(grade.ExtractFinal(finalAnswer), *g.Value, g.AbsTolerance)
	return got, ok
}

// abstains reports whether the FINAL ANSWER is an explicit abstention rather
// than a fabricated concrete answer.
func abstains(finalAnswer string) bool {
	line := strings.ToLower(firstFinalLine(finalAnswer))
	for _, marker := range abstainMarkers {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

// firstFinalLine returns the first line after the FINAL ANSWER marker, matching
// the convention the deterministic graders score.
func firstFinalLine(finalAnswer string) string {
	final := grade.ExtractFinal(finalAnswer)
	line, _, _ := strings.Cut(final, "\n")
	return strings.TrimSpace(line)
}
