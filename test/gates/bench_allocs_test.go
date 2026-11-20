package gates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/bench-allocs.py holds the hot paths' allocations per call to a
// checked-in baseline (#2716).

const benchAllocsScript = "scripts/bench-allocs.py"

// allocsRepo lays out a repository with the script, a baseline and a package
// declaring two benchmarks, and a fake go that prints threeRuns as a run's
// output.
func allocsRepo(t *testing.T, baseline string) (root string, env []string) {
	t.Helper()
	root = newGitRepo(t, benchAllocsScript, map[string]string{
		"scripts/bench-allocs.tsv": "# head\n" + baseline,
		"internal/hot/hot_test.go": "package hot\n\nimport \"testing\"\n\nfunc BenchmarkA(b *testing.B) {}\n\nfunc BenchmarkB(b *testing.B) {}\n",
	})
	writeFile(t, root, "fake/out.txt", threeRuns)
	writeFile(t, root, "fake/bin/go", "#!/bin/sh\ncat \"$FAKE/out.txt\"\n")
	require(t, os.Chmod(filepath.Join(root, "fake", "bin", "go"), 0o700)) // #nosec G302 -- the fake must be executable
	fake := filepath.Join(root, "fake")
	return root, []string{"FAKE=" + fake, "PATH=" + filepath.Join(fake, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")}
}

const threeRuns = "BenchmarkA-18  200  100 ns/op  64 B/op  4 allocs/op\n" +
	"BenchmarkA-18  200  100 ns/op  64 B/op  3 allocs/op\n" +
	"BenchmarkA-18  200  100 ns/op  64 B/op  4 allocs/op\n" +
	"BenchmarkB-18  200  100 ns/op  0 B/op  0 allocs/op\n"

func TestBenchAllocs_WithinTheBaselinePasses(t *testing.T) {
	root, env := allocsRepo(t, "./internal/hot\tBenchmarkA\t3\n./internal/hot\tBenchmarkB\t0\n")
	out, ok := runScript(t, root, []string{benchAllocsScript, "check"}, env)
	if !ok || !strings.Contains(out, "every benchmark is within its baseline") {
		t.Fatalf("want a pass, the lowest of three runs read (3): ok=%t\n%s", ok, out)
	}
}

func TestBenchAllocs_OverTheBaselineFailsNamingIt(t *testing.T) {
	root, env := allocsRepo(t, "./internal/hot\tBenchmarkA\t2\n./internal/hot\tBenchmarkB\t0\n")
	out, ok := runScript(t, root, []string{benchAllocsScript, "check"}, env)
	if ok || !strings.Contains(out, "[FAIL] BenchmarkA: 3 allocs/op, over its baseline of 2") {
		t.Fatalf("want a failure naming BenchmarkA: ok=%t\n%s", ok, out)
	}
}

func TestBenchAllocs_UnderTheBaselineSaysSo(t *testing.T) {
	root, env := allocsRepo(t, "./internal/hot\tBenchmarkA\t9\n./internal/hot\tBenchmarkB\t0\n")
	out, ok := runScript(t, root, []string{benchAllocsScript, "check"}, env)
	if !ok || !strings.Contains(out, "BenchmarkA: 3 allocs/op, under its baseline of 9") {
		t.Fatalf("want a pass that says the baseline can be lowered: ok=%t\n%s", ok, out)
	}
}

func TestBenchAllocs_ABenchmarkWithNoRowFails(t *testing.T) {
	root, env := allocsRepo(t, "./internal/hot\tBenchmarkA\t3\n")
	out, ok := runScript(t, root, []string{benchAllocsScript, "check"}, env)
	if ok || !strings.Contains(out, "[FAIL] BenchmarkB: ./internal/hot declares it and scripts/bench-allocs.tsv has no baseline") {
		t.Fatalf("want a failure for the benchmark with no row: ok=%t\n%s", ok, out)
	}
}

func TestBenchAllocs_ARowWhoseBenchmarkIsGoneFails(t *testing.T) {
	root, env := allocsRepo(t, "./internal/hot\tBenchmarkA\t3\n./internal/hot\tBenchmarkB\t0\n./internal/hot\tBenchmarkGone\t1\n")
	out, ok := runScript(t, root, []string{benchAllocsScript, "check"}, env)
	if ok || !strings.Contains(out, "[FAIL] BenchmarkGone: named in the baseline and not run") {
		t.Fatalf("want a failure for the row with no benchmark: ok=%t\n%s", ok, out)
	}
}

func TestBenchAllocs_UpdateWritesWhatItMeasured(t *testing.T) {
	root, env := allocsRepo(t, "./internal/hot\tBenchmarkA\t0\n")
	if out, ok := runScript(t, root, []string{benchAllocsScript, "update"}, env); !ok {
		t.Fatalf("update failed: %s", out)
	}
	got, err := os.ReadFile(filepath.Join(root, "scripts", "bench-allocs.tsv")) // #nosec G304 -- under the test's temp root
	require(t, err)
	if want := "# head\n./internal/hot\tBenchmarkA\t3\n./internal/hot\tBenchmarkB\t0\n"; string(got) != want {
		t.Errorf("baseline = %q, want %q", got, want)
	}
}
