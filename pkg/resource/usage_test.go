package resource

import "testing"

// The last-read column is what the admin table's never-read flag and its
// Recently-read ordering are built on, so which surfaces move it is a
// behavioral rule rather than an implementation detail: every door that serves
// the bytes to somebody using the file stamps it, and the portal drawing its
// own library does not (#1471).
func TestStampsLastRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		surface string
		want    bool
	}{
		{SurfaceMCPRead, true},
		{SurfaceFetch, true},
		{SurfaceDownload, true},
		{SurfacePreview, false},
		// An unrecognized surface stamps: the exemption is named, so a new door
		// added without a decision behaves like the three that came before it
		// rather than silently going uncounted.
		{"some_new_door", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.surface, func(t *testing.T) {
			t.Parallel()
			if got := StampsLastRead(tt.surface); got != tt.want {
				t.Errorf("StampsLastRead(%q) = %v, want %v", tt.surface, got, tt.want)
			}
		})
	}
}
