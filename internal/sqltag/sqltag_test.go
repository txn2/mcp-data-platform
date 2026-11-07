package sqltag

import (
	"strings"
	"testing"
)

func TestPlatformIsOneComment(t *testing.T) {
	if !strings.HasPrefix(Platform, "/*") || !strings.HasSuffix(Platform, "*/") || strings.Count(Platform, "*/") != 1 {
		t.Fatalf("%q is not one block comment", Platform)
	}
}
