package notification

import "testing"

func TestDefaultPrefs(t *testing.T) {
	p := DefaultPrefs("a@b.io")
	if p.Mode != ModeImmediate || !p.SharesEnabled || !p.CommentsEnabled {
		t.Errorf("defaults must be immediate-on: %+v", p)
	}
	if p.Email != "a@b.io" {
		t.Errorf("email not set: %+v", p)
	}
}

// TestPrefsUpdate_Apply pins the partial-write rule every store relies on: a
// set field overwrites, an unset one leaves the current value alone. A store
// that reimplemented this could silently reset the categories a user did not
// mention in their request.
func TestPrefsUpdate_Apply(t *testing.T) {
	mode := ModeDaily
	off := false

	partial := DefaultPrefs("a@b.io")
	PrefsUpdate{Mode: &mode, CommentsEnabled: &off}.Apply(&partial)
	if partial.Mode != ModeDaily {
		t.Errorf("Mode = %q; want %q", partial.Mode, ModeDaily)
	}
	if partial.CommentsEnabled {
		t.Error("CommentsEnabled must be turned off")
	}
	if !partial.SharesEnabled || !partial.MentionsEnabled {
		t.Errorf("unmentioned categories must keep their value: %+v", partial)
	}

	untouched := DefaultPrefs("a@b.io")
	PrefsUpdate{}.Apply(&untouched)
	if untouched != DefaultPrefs("a@b.io") {
		t.Errorf("empty update changed prefs: %+v", untouched)
	}

	all := DefaultPrefs("a@b.io")
	PrefsUpdate{Mode: &mode, SharesEnabled: &off, CommentsEnabled: &off, MentionsEnabled: &off}.Apply(&all)
	if all.SharesEnabled || all.CommentsEnabled || all.MentionsEnabled {
		t.Errorf("every category should be off: %+v", all)
	}
}

func TestValidMode(t *testing.T) {
	for _, m := range []string{ModeOff, ModeImmediate, ModeDaily} {
		if !ValidMode(m) {
			t.Errorf("ValidMode(%q) = false", m)
		}
	}
	for _, m := range []string{"", "weekly", "IMMEDIATE"} {
		if ValidMode(m) {
			t.Errorf("ValidMode(%q) = true", m)
		}
	}
}
