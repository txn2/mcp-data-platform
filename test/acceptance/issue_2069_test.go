//go:build integration

package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// Issue #2069: an HTML asset or a deck carries video clips from the hosts a
// deployment lists, and a deployment stores video-sized files without
// configuration. What is held here, through the surfaces a reader, an
// administrator and an agent meet:
//
//   - a deck with a Wistia embed and an MP4 from a listed CDN plays both in
//     the portal and on a public share signed out, and its YouTube and Vimeo
//     embeds draw as cards whose button opens the video in a new tab (their
//     players do not run in the asset frame's opaque origin, #2396);
//   - with a host off the list its frame or clip is blocked on both surfaces;
//   - an entry that would end a policy directive is refused and never reaches
//     a share page's header;
//   - a 900 MB upload succeeds on a deployment with no max_upload_bytes set,
//     and downloading it does not hold it in memory;
//   - a CSV past the registration ceiling is refused naming the ceiling,
//     without being read;
//   - the deck's tile and PDF export complete.
//
// Wire forms: save_asset's `content` is a string (an HTML document) and
// `references` a JSON array of strings; `name`, `content_type` and `purpose`
// are typed string. The admin route takes {"hosts": [...]}, sent as a JSON
// array of strings and as null (which turns embeds off). manage_table's
// `action`, `reference`, `connection` and `table_name` are strings.

const issue2069Purpose = "Acceptance for #2069: video embeds and a 1 GiB upload default."

// issue2069CDN is the public CDN the criteria's clip is served from.
const issue2069CDN = "interactive-examples.mdn.mozilla.net"

// issue2069Deck is a two-slide deck: a Wistia player and a CDN clip, then the
// YouTube and Vimeo players.
const issue2069Deck = `<!DOCTYPE html><html><head><title>Clips</title>
<link rel="stylesheet" href="/portal/vendor/reveal/reveal.css"><script src="/portal/vendor/reveal/reveal.js"></script></head>
<body><div class="reveal"><div class="slides">
<section><h2>Clips</h2>
<iframe id="wistia" src="https://fast.wistia.net/embed/iframe/26sk4lmiix" width="480" height="270" allow="autoplay; fullscreen" allowfullscreen></iframe>
<video id="cdn" controls muted playsinline poster="https://` + issue2069CDN + `/media/examples/flower.jpg" src="https://` + issue2069CDN + `/media/cc0-videos/flower.mp4" width="480"></video>
<iframe width="480" height="270" src="https://www.youtube-nocookie.com/embed/M7lc1UVf-VE" title="Embedded web player" allowfullscreen></iframe>
<iframe src="https://player.vimeo.com/video/76979871" width="480" height="270"></iframe>
</section></div></div><script>Reveal.initialize({ hash: false });</script></body></html>`

// saveEmbedDeck saves an HTML asset declaring refs and returns its id.
func saveEmbedDeck(t *testing.T, c *client, name, html string, refs []string, purpose string) string {
	t.Helper()
	args := map[string]any{
		"name": fmt.Sprintf("%s %d", name, time.Now().UnixNano()), "content": html,
		"content_type": "text/html", "purpose": purpose,
	}
	if len(refs) > 0 {
		args["references"] = refs
	}
	saved := c.call("save_asset", args)
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id, "purpose": purpose})
	})
	return id
}

// embedCase is one asset the embeds browser suite opens; its JSON names are
// the suite's (ui/e2e/embeds/embeds.spec.ts).
type embedCase struct {
	Name        string       `json:"name"`
	Asset       string       `json:"asset"`
	PublicShare string       `json:"publicShare,omitempty"`
	Videos      []embedVideo `json:"videos,omitempty"`
	Frames      []embedFrame `json:"frames,omitempty"`
	Cards       []embedCard  `json:"cards,omitempty"`
}

type embedVideo struct {
	Selector string `json:"selector"`
	Plays    bool   `json:"plays"`
	Seeks    bool   `json:"seeks,omitempty"`
}

type embedFrame struct {
	Host    string `json:"host"`
	Allowed bool   `json:"allowed"`
}

type embedCard struct {
	Video string `json:"video"`
	Opens string `json:"opens"`
}

// runEmbedSuite opens every case in a real browser on the asset page signed
// in and on its public share signed out.
func runEmbedSuite(t *testing.T, cases []embedCase) {
	t.Helper()
	raw, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/embeds/playwright.config.ts")
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(), "EMBED_BASE_URL="+baseURL(), "EMBED_CASES="+string(raw),
		"EMBED_SIGN_IN_EMAIL="+issue2373SignInEmail, "EMBED_SIGN_IN_PASSWORD="+issue2373SignInPassword)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright:\n%s", out)
	if err != nil {
		t.Fatalf("an embed did not behave where it is read: %v", err)
	}
}

// issue2069SetHosts writes the administrator's list and restores the one in
// force when the test ends.
func issue2069SetHosts(t *testing.T, c *client, hosts any) map[string]any {
	t.Helper()
	status, before := c.rest(http.MethodGet, "/api/v1/admin/settings/embed-hosts", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the embed hosts: %d %v", status, before)
	}
	t.Cleanup(func() {
		_, _ = c.rest(http.MethodPut, "/api/v1/admin/settings/embed-hosts", jsonBody(t, map[string]any{"hosts": before["hosts"]}))
	})
	status, out := c.rest(http.MethodPut, "/api/v1/admin/settings/embed-hosts", jsonBody(t, map[string]any{"hosts": hosts}))
	if status != http.StatusOK {
		t.Fatalf("writing the embed hosts: %d %v", status, out)
	}
	// Each replica reads the list again within fifteen seconds.
	time.Sleep(16 * time.Second)
	return out
}

var issue2069Listed = []string{
	"fast.wistia.net", "fast.wistia.com", "www.youtube-nocookie.com", "www.youtube.com", "player.vimeo.com", issue2069CDN,
}

// TestIssue2069_ADeckPlaysListedClipsAndCardsYouTubeAndVimeoOnBothSurfaces:
// the Wistia frame loads and the CDN clip plays on the asset page and a
// public share; the YouTube and Vimeo players are cards that open the video.
func TestIssue2069_ADeckPlaysListedClipsAndCardsYouTubeAndVimeoOnBothSurfaces(t *testing.T) {
	c := connect(t)
	issue2069SetHosts(t, c, issue2069Listed)
	deck := saveEmbedDeck(t, c, "Acceptance 2069 deck", issue2069Deck, nil, issue2069Purpose)
	runEmbedSuite(t, []embedCase{{
		Name: "a deck of listed clips", Asset: deck, PublicShare: issue2373Share(t, c, deck, "public"),
		Videos: []embedVideo{{Selector: "#cdn", Plays: true}},
		Frames: []embedFrame{{Host: "fast.wistia.net", Allowed: true}},
		Cards: []embedCard{
			{Video: "youtube:M7lc1UVf-VE", Opens: "https://www.youtube.com/watch?v=M7lc1UVf-VE"},
			{Video: "vimeo:76979871", Opens: "https://vimeo.com/76979871"},
		},
	}})
}

// TestIssue2069_AHostOffTheListIsBlockedOnBothSurfaces: with the CDN and
// Wistia off the list, the clip and the frame are refused by the policy on the
// asset page and on the public share alike.
func TestIssue2069_AHostOffTheListIsBlockedOnBothSurfaces(t *testing.T) {
	c := connect(t)
	issue2069SetHosts(t, c, []string{"www.youtube-nocookie.com"})
	deck := saveEmbedDeck(t, c, "Acceptance 2069 blocked", issue2069Deck, nil, issue2069Purpose)
	runEmbedSuite(t, []embedCase{{
		Name: "a deck of unlisted clips", Asset: deck, PublicShare: issue2373Share(t, c, deck, "public"),
		Videos: []embedVideo{{Selector: "#cdn", Plays: false}},
		Frames: []embedFrame{{Host: "fast.wistia.net", Allowed: false}},
	}})
}

// TestIssue2069_AnEntryThatWouldBreakThePolicyIsRefused: the admin route
// refuses an entry that would end a directive, names it, stores nothing, and
// the share page's header never carries it; null turns embeds off.
func TestIssue2069_AnEntryThatWouldBreakThePolicyIsRefused(t *testing.T) {
	c := connect(t)
	status, before := c.rest(http.MethodGet, "/api/v1/admin/settings/embed-hosts", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the embed hosts: %d", status)
	}
	bad := "cdn.example.com; script-src *"
	status, out := c.rest(http.MethodPut, "/api/v1/admin/settings/embed-hosts", jsonBody(t, map[string]any{"hosts": []string{bad}}))
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out), bad) {
		t.Errorf("an injecting entry answered %d %v, want 400 naming it", status, out)
	}
	_, after := c.rest(http.MethodGet, "/api/v1/admin/settings/embed-hosts", http.NoBody)
	if fmt.Sprint(after["hosts"]) != fmt.Sprint(before["hosts"]) {
		t.Errorf("a refused write changed the list: %v -> %v", before["hosts"], after["hosts"])
	}

	off := issue2069SetHosts(t, c, nil)
	if hosts, ok := off["hosts"].([]any); !ok || len(hosts) != 0 {
		t.Errorf("hosts: null answered %v, want []", off["hosts"])
	}
	deck := saveEmbedDeck(t, c, "Acceptance 2069 policy", "<p>policy</p>", nil, issue2069Purpose)
	share := issue2373Share(t, c, deck, "public")
	res, err := http.Get(baseURL() + share) //nolint:noctx // a loopback read
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"frame-src blob: data: 'self';", "media-src 'self' blob: data:;"} {
		if !strings.Contains(csp, want) {
			t.Errorf("with embeds off the share page's policy does not carry %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "script-src *") {
		t.Errorf("the refused entry reached the header: %s", csp)
	}
}

// TestIssue2069_TheDeckTileAndPDFComplete: a deck with clips gets a tile in
// both schemes and exports to PDF.
func TestIssue2069_TheDeckTileAndPDFComplete(t *testing.T) {
	c := connect(t)
	deck := saveEmbedDeck(t, c, "Acceptance 2069 tile", issue2069Deck, nil, issue2069Purpose)
	for _, variant := range []string{"light", "dark"} {
		if ink := issue2373Ink(issue2068Tile(t, c, deck, variant)); ink < 0.005 {
			t.Errorf("the %s tile is blank (%.4f)", variant, ink)
		}
	}
	status, contentType, pdf := issue1983Get(t, "/api/v1/portal/assets/"+deck+"/pdf", c.apiKey)
	if status != http.StatusOK || contentType != "application/pdf" || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Errorf("the deck's PDF export answered %d %s with %d bytes", status, contentType, len(pdf))
	}
}

// TestIssue2069_ACSVPastTheRegistrationCeilingIsRefusedNamingIt: a CSV
// replaced in storage with one past the ceiling (the dev stack's 250 MB, its
// upload ceiling, #1634) is refused naming the ceiling, from its size alone.
func TestIssue2069_ACSVPastTheRegistrationCeilingIsRefusedNamingIt(t *testing.T) {
	c := connect(t)
	stamp := issue1833Stamp()
	id, _ := issue2395Upload(t, c, "past-ceiling.csv", "text/csv", []byte("store_id,units\n1,10\n"))
	status, res := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the resource: %d", status)
	}
	key, _ := res["s3_key"].(string)
	big := bytes.Repeat([]byte("1,10\n"), (260<<20)/5)
	issue2069PutResource(t, key, append([]byte("store_id,units\n"), big...))
	issue1833Refused(t, c, "mcp:resource:"+id, "acc_2069_"+stamp, "250 MB", "Parquet file registers at any size")
}

// issue2069Config is a deployment that sets no upload ceiling: the dev stack's
// database and managed-resource store, nothing else.
const issue2069Config = `
server:
  name: acceptance-2069
  transport: http
  address: "%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2069-key"
        name: "acceptance-2069"
        roles: ["admin"]
personas:
  admin:
    display_name: Admin
    roles: ["admin"]
    tools:
      allow: ["*"]
    connections:
      allow: ["*"]
admin:
  persona: admin
database:
  dsn: "postgres://platform:platform_secret@localhost:%s/mcp_platform?sslmode=disable"
  max_open_conns: 5
toolkits:
  s3:
    instances:
      dev-resources:
        enabled: true
        endpoint: "https://localhost:%s"
        region: "us-east-1"
        access_key_id: "dev-access-key"
        secret_access_key: "dev-secret-key"
        use_path_style: true
    default: dev-resources
resources:
  managed:
    enabled: true
    s3_connection: dev-resources
    s3_bucket: managed-resources
thumbnails:
  enabled: false
`

// TestIssue2069_A900MBUploadSucceedsWithNoCeilingSet: on a deployment that
// configures no max_upload_bytes, a 900 MB file uploads, downloads whole, and
// the download does not raise the process's memory by the file's size.
func TestIssue2069_A900MBUploadSucceedsWithNoCeilingSet(t *testing.T) {
	tlsPort := os.Getenv("DEV_S3_TLS_PORT")
	if tlsPort == "" {
		tlsPort = "9443"
	}
	root, _ := filepath.Abs("../..")
	t.Setenv("AWS_CA_BUNDLE", filepath.Join(root, "dev", ".tls", "seaweedfs", "public.crt"))
	// The address stays a %s for startPlatform to fill.
	p := startPlatform(t, fmt.Sprintf(issue2069Config, "%s", devPostgresPort(), tlsPort))
	p.serving(t, 2*time.Minute)

	c := connectAt(t, p.base, "acceptance-2069-key")
	status, me := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if status == http.StatusOK && me["max_upload_bytes"] != float64(1<<30) {
		t.Errorf("/api/v1/portal/me reports max_upload_bytes %v, want the 1 GiB default", me["max_upload_bytes"])
	}

	const size = 900 << 20
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	id, _ := issue2069UploadTo(t, c, p.base, data)
	pid := fmt.Sprint(p.cmd.Process.Pid)
	var got []byte
	growth := issue2395PeakGrowth(pid, func() {
		req, _ := http.NewRequestWithContext(c.ctx, http.MethodGet, p.base+"/api/v1/resources/"+id+"/content", http.NoBody)
		req.Header.Set("Authorization", "Bearer acceptance-2069-key")
		res, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
		if err != nil {
			t.Fatalf("downloading: %v", err)
		}
		defer res.Body.Close() //nolint:errcheck // best-effort close
		got, _ = io.ReadAll(res.Body)
	})
	if !bytes.Equal(got, data) {
		t.Fatalf("the download returned %d bytes, want the %d uploaded", len(got), size)
	}
	t.Logf("download: resident memory rose %d MiB for a %d MiB file", growth>>20, size>>20)
	if growth > size/2 {
		t.Errorf("downloading a %d MiB file raised the platform's memory by %d MiB", size>>20, growth>>20)
	}
}

// issue2069UploadTo uploads data as a resource on the platform at base.
func issue2069UploadTo(t *testing.T, c *client, base string, data []byte) (string, string) {
	t.Helper()
	saved := os.Getenv("MCP_BASE_URL")
	t.Setenv("MCP_BASE_URL", base)
	defer func() { t.Setenv("MCP_BASE_URL", saved) }()
	return issue2395Upload(t, c, "large-video.bin", "application/octet-stream", data)
}

// issue2069PutResource replaces a managed resource's object in place, in the
// dev stack's resource store (its own SeaweedFS, served over TLS), the way a
// vendor drop replaces a file without touching its record.
func issue2069PutResource(t *testing.T, key string, body []byte) {
	t.Helper()
	port := os.Getenv("DEV_S3_TLS_PORT")
	if port == "" {
		port = "9443"
	}
	root, _ := filepath.Abs("../..")
	t.Setenv("AWS_CA_BUNDLE", filepath.Join(root, "dev", ".tls", "seaweedfs", "public.crt"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s3, err := s3client.New(ctx, &s3client.Config{
		Region: "us-east-1", Endpoint: "https://localhost:" + port, UsePathStyle: true,
		AccessKeyID: "dev-access-key", SecretAccessKey: "dev-secret-key", Timeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s3.PutObjectStream(ctx, &s3client.PutObjectStreamInput{
		Bucket: "managed-resources", Key: key, Body: bytes.NewReader(body), ContentType: "text/csv",
	}); err != nil {
		t.Fatalf("overwriting s3://managed-resources/%s: %v", key, err)
	}
}
