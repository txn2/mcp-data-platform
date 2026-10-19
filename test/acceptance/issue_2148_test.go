//go:build integration

package acceptance

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Acceptance for #2148: storage capacity in the admin UI. The dev stack's
// SeaweedFS cluster is read for its disks and a bucket on it shows the
// cluster's free space; a spool volume being filled walks from warning to
// critical and resolves when it is emptied, each step posted once to the
// Mattermost fixture; a synthetic 30-day history projects the fill date the
// line does; and a budget set through the settings survives a restart and
// overrides MCP_PLATFORM_STORAGE_BUDGETS.
//
// Wire forms: the settings route takes one JSON object; every field is sent
// in the form its schema gives (numbers as JSON numbers, budgets as an object
// of bucket to bytes, seaweedfs as an object, channels as a list), and a field
// left out keeps what is stored, which TestIssue2148_SettingsKeepWhatAWriteLeavesOut
// sends both ways.

const issue2148Path = "/api/v1/admin/settings/storage"

// issue2148Store is one store of the storage report, by key.
func issue2148Store(t *testing.T, c *client, key string) map[string]any {
	t.Helper()
	status, rep := c.rest(http.MethodGet, "/api/v1/admin/storage", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/admin/storage: %d %v", status, rep)
	}
	stores, _ := rep["stores"].([]any)
	for _, s := range stores {
		st, _ := s.(map[string]any)
		if st["store"] == key {
			return st
		}
	}
	return nil
}

// issue2148Channel creates a Mattermost channel that repeats nothing within
// an hour, so each step of a condition is one post.
func issue2148Channel(t *testing.T, c *client, name string) mattermostEnv {
	t.Helper()
	env := readMattermostEnv(t)
	putChannel(t, c, name, map[string]any{
		"kind": "mattermost", "connection": "mattermost-dev", "target": env.channelID, "repeat_after": "1h",
	})
	t.Cleanup(func() { deleteChannel(t, c, name) })
	return env
}

// issue2148Count is how many of the fixture's posts start with prefix.
func issue2148Count(t *testing.T, env mattermostEnv, contains ...string) int {
	t.Helper()
	n := 0
	for _, m := range mattermostPosts(t, env) {
		all := true
		for _, s := range contains {
			if !strings.Contains(m, s) {
				all = false
			}
		}
		if all {
			n++
		}
	}
	return n
}

// TestIssue2148_TheBucketShowsTheClustersFreeSpace: with the dev settings
// naming the SeaweedFS master and its volume server, the report reads the
// cluster's disks, and the portal bucket, which is on that cluster and has no
// budget, has the cluster's free space as its own.
func TestIssue2148_TheBucketShowsTheClustersFreeSpace(t *testing.T) {
	c := connect(t)
	cluster := issue2148Store(t, c, "seaweedfs")
	if cluster == nil {
		t.Fatal("the report has no SeaweedFS store; dev/start.sh names the cluster in the storage settings")
	}
	total, _ := cluster["total_bytes"].(float64)
	free, _ := cluster["free_bytes"].(float64)
	if total <= 0 || free <= 0 || free > total {
		t.Fatalf("the cluster reads total %v free %v", total, free)
	}
	bucket := issue2148Store(t, c, "bucket:portal-assets")
	if bucket == nil {
		t.Fatal("the report has no store for the portal-assets bucket")
	}
	if bucket["capacity_from"] != "seaweedfs" {
		t.Fatalf("portal-assets takes its capacity from %v, want the cluster: %v", bucket["capacity_from"], bucket)
	}
	bucketFree, _ := bucket["free_bytes"].(float64)
	// The two figures are read moments apart from a live cluster.
	if math.Abs(bucketFree-free) > 64<<20 {
		t.Fatalf("portal-assets has %v bytes free, the cluster %v", bucketFree, free)
	}
}

// TestIssue2148_SettingsKeepWhatAWriteLeavesOut: a write naming one field
// keeps every other, and a write naming a channel that does not exist is
// refused.
func TestIssue2148_SettingsKeepWhatAWriteLeavesOut(t *testing.T) {
	c := connect(t)
	putSettings(t, c, issue2148Path, map[string]any{
		"database_limit_bytes": 50 << 30, "history_days": 90,
		"used_percent": map[string]any{"warning": 70, "critical": 85},
	})
	_, got := c.rest(http.MethodPut, issue2148Path, jsonBody(t, map[string]any{"history_days": 120}))
	if got["database_limit_bytes"] != float64(50<<30) || got["history_days"] != float64(120) {
		t.Fatalf("a write of one field did not keep the rest: %v", got)
	}
	used, _ := got["used_percent"].(map[string]any)
	if used["warning"] != float64(70) {
		t.Fatalf("used_percent was not kept: %v", got)
	}
	status, body := c.rest(http.MethodPut, issue2148Path, jsonBody(t, map[string]any{"channels": []string{"acc-2148-no-such-channel"}}))
	if status != http.StatusBadRequest {
		t.Fatalf("a missing channel was accepted: %d %v", status, body)
	}
	status, body = c.rest(http.MethodPut, issue2148Path, jsonBody(t, map[string]any{"history_days": "120"}))
	if status != http.StatusBadRequest {
		t.Fatalf("history_days as a string was accepted: %d %v", status, body)
	}
}

// issue2148RAMDisk makes a 32 MiB volume and returns its mount point.
func issue2148RAMDisk(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Fatalf("the spool volume criterion fills a RAM disk made with hdiutil, on darwin; this is %s", runtime.GOOS)
	}
	out, err := exec.Command("hdiutil", "attach", "-nomount", "ram://65536").Output()
	if err != nil {
		t.Fatalf("making a RAM disk: %v", err)
	}
	dev := strings.Fields(string(out))[0]
	name := "acc2148" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	if b, err := exec.Command("diskutil", "erasevolume", "HFS+", name, dev).CombinedOutput(); err != nil {
		_ = exec.Command("hdiutil", "detach", dev).Run()
		t.Fatalf("formatting the RAM disk: %v %s", err, b)
	}
	t.Cleanup(func() { _ = exec.Command("hdiutil", "detach", "-force", dev).Run() })
	return "/Volumes/" + name
}

// issue2148FillTo writes a file on the volume until it is pct used.
func issue2148FillTo(t *testing.T, mount, file string, pct float64) {
	t.Helper()
	var st syscall.Statfs_t
	if err := syscall.Statfs(mount, &st); err != nil {
		t.Fatal(err)
	}
	total := float64(st.Blocks) * float64(st.Bsize)
	free := float64(st.Bavail) * float64(st.Bsize)
	need := int64(pct/100*total - (total - free))
	if need <= 0 {
		return
	}
	f, err := os.OpenFile(filepath.Join(mount, file), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // written below
	if _, err := io.CopyN(f, zeros{}, need); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// TestIssue2148_FillingTheSpoolVolumeWalksWarningToCritical: a replica's spool
// is on a 32 MiB volume. Filling it past 75% posts a warning, past 90% a
// critical, and emptying it a resolution, each once, under the default
// thresholds.
func TestIssue2148_FillingTheSpoolVolumeWalksWarningToCritical(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	channel := "acc-2148-" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	env := issue2148Channel(t, c, channel)
	putSettings(t, c, issue2148Path, map[string]any{
		"channels":     []string{channel},
		"used_percent": map[string]any{"warning": 75, "critical": 90},
		"days_left":    map[string]any{"warning": 0, "critical": 0},
	})
	mount := issue2148RAMDisk(t)
	store := issue2121NewStore(t)
	r := issue2121Start(t, filepath.Join(mount, "spool"), store, "MCP_PLATFORM_STORAGE_HISTORY_INTERVAL=2s")
	name := "Webhook spool on " + r.name

	waitFor(t, "the spool volume as a store", func() (bool, string) {
		st := issue2148Store(t, c, "spool:"+r.name)
		return st != nil && st["total_bytes"] != nil, fmt.Sprintf("%v", st)
	})

	issue2148FillTo(t, mount, "fill", 80)
	waitFor(t, "the warning", func() (bool, string) {
		n := issue2148Count(t, env, "WARNING: "+name)
		return n == 1, fmt.Sprintf("%d warning posts", n)
	})
	issue2148FillTo(t, mount, "fill", 93)
	waitFor(t, "the critical", func() (bool, string) {
		n := issue2148Count(t, env, "CRITICAL: "+name)
		return n == 1, fmt.Sprintf("%d critical posts", n)
	})
	if err := os.Remove(filepath.Join(mount, "fill")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the resolution", func() (bool, string) {
		n := issue2148Count(t, env, "Resolved: "+name)
		return n >= 1, fmt.Sprintf("%d resolved posts", n)
	})
	for _, level := range []string{"WARNING: ", "CRITICAL: "} {
		if n := issue2148Count(t, env, level+name); n != 1 {
			t.Fatalf("%s%s was posted %d times; each step posts once", level, name, n)
		}
	}
}

// TestIssue2148_ASyntheticHistoryProjectsTheLinearDate: thirty days of samples
// growing 1 GiB a day toward a 100 GiB budget project the fill date the line
// gives, within one sample interval.
func TestIssue2148_ASyntheticHistoryProjectsTheLinearDate(t *testing.T) {
	c := connect(t)
	db := devDB(t)
	bucket := "acc2148-synth-" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	store := "bucket:" + bucket
	const gib = int64(1) << 30
	const interval = 15 * time.Minute
	now := time.Now().UTC().Truncate(time.Minute)
	first := now.Add(-30 * 24 * time.Hour)
	for at := first; at.Before(now); at = at.Add(interval) {
		used := 20*gib + int64(float64(gib)*at.Sub(first).Hours()/24)
		if _, err := db.Exec(`INSERT INTO storage_samples (store, sampled_at, used_bytes, total_bytes) VALUES ($1, $2, $3, $4)`,
			store, at, used, 100*gib); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM storage_samples WHERE store = $1`, store)
		_, _ = db.Exec(`DELETE FROM storage_usage WHERE bucket = $1`, bucket)
	})
	putSettings(t, c, issue2148Path, map[string]any{"budgets": map[string]any{bucket: 100 * gib}})
	current := 20*gib + int64(float64(gib)*time.Now().UTC().Sub(first).Hours()/24)

	var st map[string]any
	waitFor(t, "the synthetic bucket's projection", func() (bool, string) {
		// The capacity scan clears usage rows of buckets the deployment does
		// not use, so the row is written right before each read.
		if _, err := db.Exec(`INSERT INTO storage_usage (bucket, purpose, bytes, objects, updated_at) VALUES ($1, 'resources', $2, 1, NOW())
			ON CONFLICT (bucket, purpose) DO UPDATE SET bytes = EXCLUDED.bytes`, bucket, current); err != nil {
			t.Fatal(err)
		}
		st = issue2148Store(t, c, store)
		return st != nil && st["exhaustion_at"] != nil && st["used_bytes"] == float64(current), fmt.Sprintf("%v", st)
	})
	at, err := time.Parse(time.RFC3339Nano, st["exhaustion_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Now().UTC().Add(time.Duration(float64(100*gib-current) / float64(gib) * 24 * float64(time.Hour)))
	if d := at.Sub(want); d > interval || d < -interval {
		t.Fatalf("projected %s, the line gives %s: %v apart, more than one sample interval", at, want, d)
	}
	if st["rate_used"] != "30d" && st["rate_used"] != "7d" {
		t.Fatalf("the projection does not say which rate it used: %v", st)
	}
}

// issue2148Gauge reads one labeled series off a replica's metrics.
func issue2148Gauge(r *issue2121Replica, series string) (float64, bool) {
	res, err := http.Get(r.metrics) // #nosec G107 -- the replica this test started
	if err != nil {
		return 0, false
	}
	defer res.Body.Close() //nolint:errcheck // read below
	raw, _ := io.ReadAll(res.Body)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, series+" ") {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, series)), 64)
			return v, err == nil
		}
	}
	return 0, false
}

// TestIssue2148_BudgetsSurviveARestartAndOverrideTheEnvironment: a budget
// written through the settings is read by a process started after it, and
// wins over MCP_PLATFORM_STORAGE_BUDGETS for its bucket, in the report and in
// the storage_bucket_budget_bytes gauge.
func TestIssue2148_BudgetsSurviveARestartAndOverrideTheEnvironment(t *testing.T) {
	c := connectFor(t, 5*time.Minute)
	const gib = int64(1) << 30
	putSettings(t, c, issue2148Path, map[string]any{"budgets": map[string]any{"portal-assets": 3 * gib}})
	root, _ := filepath.Abs("../..")
	r := issue2121Start(t, filepath.Join(root, "build", "2148", "spool-"+strconv.FormatInt(time.Now().UnixNano(), 36)),
		issue2121NewStore(t), "MCP_PLATFORM_STORAGE_BUDGETS=portal-assets=1GiB,managed-resources=2GiB", "MCP_PLATFORM_CAPACITY_INTERVAL=2s")

	waitFor(t, "the gauge with the settings' budget", func() (bool, string) {
		v, ok := issue2148Gauge(r, `storage_bucket_budget_bytes{bucket="portal-assets"}`)
		return ok && v == float64(3*gib), fmt.Sprintf("%v %v", v, ok)
	})
	if v, ok := issue2148Gauge(r, `storage_bucket_budget_bytes{bucket="managed-resources"}`); !ok || v != float64(2*gib) {
		t.Fatalf("a bucket the settings do not name keeps the environment's budget: %v %v", v, ok)
	}
	req, _ := http.NewRequest(http.MethodGet, r.base+"/api/v1/admin/storage", http.NoBody)
	req.Header.Set("X-API-Key", devAPIKey())
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the replica this test started
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"store":"bucket:portal-assets"`) ||
		!strings.Contains(string(body), fmt.Sprintf(`"total_bytes":%d`, 3*gib)) {
		t.Fatalf("the new process's report does not carry the settings' budget: %d %s", res.StatusCode, body)
	}
}
