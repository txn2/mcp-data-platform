package removedvalue

import "testing"

func TestRegisterAndLookup(t *testing.T) {
	Register("a_tool", "command", "old", "new with kind=x")
	if got, ok := Lookup("a_tool", "command", "old"); !ok || got != "new with kind=x" {
		t.Fatalf("Lookup = %q, %v", got, ok)
	}
	for _, c := range [][3]string{{"other_tool", "command", "old"}, {"a_tool", "mode", "old"}, {"a_tool", "command", "kept"}} {
		if got, ok := Lookup(c[0], c[1], c[2]); ok || got != "" {
			t.Errorf("Lookup%v = %q, %v; want nothing", c, got, ok)
		}
	}
}
