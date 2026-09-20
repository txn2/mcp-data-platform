package datahub

import "testing"

// This toolkit serves one connection and lists none, so the enumeration that
// reports writability beside a connection asks the toolkit (#1805).
func TestIsReadOnly(t *testing.T) {
	if !(&Toolkit{config: Config{ReadOnly: true}}).IsReadOnly() {
		t.Error("a read-only toolkit reported that it accepts writes")
	}
	if (&Toolkit{}).IsReadOnly() {
		t.Error("a toolkit with no read_only reported that it refuses writes")
	}
}
