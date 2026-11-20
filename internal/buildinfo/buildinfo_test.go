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

func TestBuildIsTheVersionAndTheCommit(t *testing.T) {
	prevV, prevC := Version, Commit
	t.Cleanup(func() { Version, Commit = prevV, prevC })
	cases := []struct{ version, commit, want string }{
		{"v1.166.2", "8b91e56", "v1.166.2+8b91e56"},
		{"dev", "8b91e56", "dev+8b91e56"},
		{"dev", "none", "dev"},
		{"v1.166.2", "", "v1.166.2"},
	}
	for _, c := range cases {
		Version, Commit = c.version, c.commit
		if got := Build(); got != c.want {
			t.Errorf("Build() with %q, %q = %q, want %q", c.version, c.commit, got, c.want)
		}
	}
}
