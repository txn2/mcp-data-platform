package scriptincident

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestAlertRecipients_IsTheOwnerOnly pins who the alert set is: the script's
// owner, trimmed, and nobody when the script has no owner address.
func TestAlertRecipients_IsTheOwnerOnly(t *testing.T) {
	assert.Equal(t, []string{"jane@example.com"},
		Recipients(&script.Script{OwnerEmail: " jane@example.com "}))
	assert.Empty(t, Recipients(&script.Script{OwnerEmail: "  "}))
}

// TestAlertDetail_TruncatesFromTheRightEnd pins the two directions: an error
// reads from the top, a log reads from the bottom.
func TestAlertDetail_TruncatesFromTheRightEnd(t *testing.T) {
	detail := alertDetail(script.RunResult{
		Error: "E" + strings.Repeat("x", maxAlertError+50),
		Log:   strings.Repeat("y", maxAlertLog+50) + "LAST",
	})
	assert.Contains(t, detail, "Ex", "the error's first line survives")
	assert.Contains(t, detail, "LAST", "the log's last line survives")
	assert.Contains(t, detail, "[truncated]")

	assert.Empty(t, alertDetail(script.RunResult{}), "nothing to say is said with nothing")
}

// TestAlertDetail_CutsOnARuneBoundary pins that a truncated body is still text.
func TestAlertDetail_CutsOnARuneBoundary(t *testing.T) {
	detail := alertDetail(script.RunResult{Error: strings.Repeat("é", maxAlertError+10)})
	assert.True(t, isValidUTF8(detail), "half a character is not a character")
}

// isValidUTF8 reports whether s decodes cleanly.
func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestNotifyWriteIsBounded pins that the alert cannot hold a worker open on a
// wedged queue for longer than its own timeout.
func TestNotifyWriteIsBounded(t *testing.T) {
	assert.LessOrEqual(t, WriteTimeout, 10*time.Second)
}

func TestFailureLine(t *testing.T) {
	assert.Equal(t, "Error: division by zero", failureLine("Traceback (most recent call last):\n  main.star:3\nError: division by zero\n"))
	assert.Equal(t, "boom", failureLine("  boom  "))
	assert.Empty(t, failureLine(" \n "))
	long := strings.Repeat("x", maxAlertError+10)
	assert.Equal(t, long[:maxAlertError]+"\n[truncated]", failureLine(long), "a long line is cut as the alert's error is")
}
