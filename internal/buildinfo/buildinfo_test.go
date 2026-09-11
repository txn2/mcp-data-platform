package buildinfo

import "testing"

// TestDefaultsNameAnUnstampedBuild pins the values a build without ldflags
// reports, which is what a test binary and `go run` are. The release
// build overwrites all three; a default that changed shape would change
// what --version, the admin system route and the outbound User-Agent
// report for a developer build.
func TestDefaultsNameAnUnstampedBuild(t *testing.T) {
	if Version != "dev" {
		t.Errorf("Version = %q; want \"dev\" for an unstamped build", Version)
	}
	if Commit != "none" {
		t.Errorf("Commit = %q; want \"none\"", Commit)
	}
	if Date != "unknown" {
		t.Errorf("Date = %q; want \"unknown\"", Date)
	}
}
