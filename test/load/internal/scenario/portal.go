package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/test/load/internal/harness"
	"github.com/txn2/mcp-data-platform/test/load/internal/mcpc"
	"github.com/txn2/mcp-data-platform/test/load/internal/report"
)

// portalRead exercises the portal REST read surface: authenticated list/read
// endpoints (with an admin Bearer key) and, when a public share can be seeded,
// the unauthenticated public viewer path. The endpoint mix is fixed in Setup so
// workers can rotate through it without recomputing which paths are live.
type portalRead struct {
	endpoints   []portalEndpoint
	publicToken string
}

type portalEndpoint struct {
	op   string
	path string
	anon bool // hit with the anonymous client (public viewer path)
}

func (*portalRead) Name() string { return "portal-read" }
func (*portalRead) Description() string {
	return "portal REST list/read endpoints plus the public viewer path"
}

func (*portalRead) Defaults() harness.RunDefaults {
	return harness.RunDefaults{Concurrency: 16, Duration: 30 * time.Second, Warmup: 5 * time.Second}
}

// authedReadEndpoints are the always-available authenticated list/read routes.
var authedReadEndpoints = []portalEndpoint{
	{op: "portal_me", path: "/api/v1/portal/me"},
	{op: "portal_assets_list", path: "/api/v1/portal/assets"},
	{op: "portal_collections_list", path: "/api/v1/portal/collections"},
	{op: "portal_shared_with_me", path: "/api/v1/portal/shared-with-me"},
}

// Setup confirms the portal is up (authenticated /me returns 200) and best-effort
// seeds a public share so the public viewer path can be exercised. If seeding
// fails, the public path is excluded and a warning is logged (never silently
// dropped) so the operator knows the published mix.
func (s *portalRead) Setup(ctx context.Context, env *harness.Env) error {
	code, _, err := s.get(ctx, env.HTTP, env.Target.URL("/api/v1/portal/me"))
	if err != nil {
		return fmt.Errorf("portal-read setup: GET /me: %w", err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("portal-read setup: GET /me returned %d; is portal enabled with a database?", code)
	}

	s.endpoints = append([]portalEndpoint(nil), authedReadEndpoints...)

	token, err := s.seedPublicShare(ctx, env)
	if err != nil || token == "" {
		env.Log.Warn("portal-read: public viewer path excluded (could not seed a share)", "error", err)
		return nil
	}
	s.publicToken = token
	s.endpoints = append(s.endpoints, portalEndpoint{
		op:   "portal_public_view",
		path: "/portal/view/" + token,
		anon: true,
	})
	env.Log.Info("portal-read: public viewer path seeded", "token_len", len(token))
	return nil
}

func (*portalRead) Teardown(context.Context, *harness.Env) {}

func (s *portalRead) NewWorker(_ context.Context, env *harness.Env, id int) (harness.Worker, error) {
	return &portalWorker{sc: s, env: env}, nil
}

func (s *portalRead) Assess(_ *harness.Env, rep *report.Report) []report.Assertion {
	out := []report.Assertion{errorRateAssertion(rep, "portal_me", 0.02)}
	if s.publicToken != "" {
		out = append(out, errorRateAssertion(rep, "portal_public_view", 0.02))
	}
	return out
}

// seedPublicShare finds or creates an owned asset and creates a public share on
// it, returning the share token parsed from the share URL.
func (s *portalRead) seedPublicShare(ctx context.Context, env *harness.Env) (string, error) {
	assetID, err := s.firstAssetID(ctx, env)
	if err != nil {
		return "", err
	}
	if assetID == "" {
		if assetID, err = s.createAsset(ctx, env); err != nil {
			return "", err
		}
	}
	if assetID == "" {
		return "", errors.New("no asset available to share")
	}
	return s.createPublicShare(ctx, env, assetID)
}

// firstAssetID returns the id of the first listed asset, or "" if none.
func (s *portalRead) firstAssetID(ctx context.Context, env *harness.Env) (string, error) {
	_, body, err := s.get(ctx, env.HTTP, env.Target.URL("/api/v1/portal/assets"))
	if err != nil {
		return "", err
	}
	// The list endpoint returns {"data":[{"id":...}], "total":N, ...}.
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		//nolint:nilerr // an unrecognized list shape is treated as "no assets" so the caller falls through to creating one; the decode error is not a hard failure
		return "", nil
	}
	if len(resp.Data) > 0 {
		return resp.Data[0].ID, nil
	}
	return "", nil
}

// createAsset creates a markdown artifact via the save_asset MCP tool and
// returns the new asset id by re-listing.
func (s *portalRead) createAsset(ctx context.Context, env *harness.Env) (string, error) {
	sess, err := env.MCP.Connect(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = sess.Close() }()
	res := mcpc.Call(ctx, sess, "save_asset", map[string]any{
		"name":         "loadgen-portal-seed",
		"content":      "# loadgen seed\n\nSeed artifact for the portal-read public viewer path.",
		"content_type": "text/markdown",
		"description":  "Created by the load harness to seed a public share.",
	})
	if err := res.Err(); err != nil {
		return "", err
	}
	return s.firstAssetID(ctx, env)
}

// createPublicShare posts an empty (public link) share and extracts the token.
func (s *portalRead) createPublicShare(ctx context.Context, env *harness.Env, assetID string) (string, error) {
	url := env.Target.URL("/api/v1/portal/assets/" + assetID + "/shares")
	code, body, err := s.postJSON(ctx, env.HTTP, url, map[string]any{})
	if err != nil {
		return "", err
	}
	if code < 200 || code >= 300 {
		return "", fmt.Errorf("share create returned %d", code)
	}
	var resp struct {
		ShareURL string `json:"share_url"`
		Share    struct {
			Token string `json:"token"`
		} `json:"share"`
	}
	_ = json.Unmarshal(body, &resp)
	if resp.Share.Token != "" {
		return resp.Share.Token, nil
	}
	if i := strings.LastIndex(resp.ShareURL, "/portal/view/"); i >= 0 {
		return resp.ShareURL[i+len("/portal/view/"):], nil
	}
	return "", errors.New("no token in share response")
}

// --- HTTP helpers ---

func (*portalRead) get(ctx context.Context, client *http.Client, url string) (status int, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return 0, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}

func (*portalRead) postJSON(ctx context.Context, client *http.Client, url string, payload any) (status int, body []byte, err error) {
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}

// portalWorker rotates through the scenario's endpoint mix, one per iteration.
type portalWorker struct {
	sc  *portalRead
	env *harness.Env
	n   int
}

func (w *portalWorker) Iterate(ctx context.Context) {
	eps := w.sc.endpoints
	if len(eps) == 0 {
		return
	}
	ep := eps[w.n%len(eps)]
	w.n++
	client := w.env.HTTP
	if ep.anon {
		client = w.env.Anon
	}
	_ = w.env.Timed(ep.op, func() error {
		code, _, err := w.sc.get(ctx, client, w.env.Target.URL(ep.path))
		if err != nil {
			return err
		}
		if code < 200 || code >= 300 {
			return fmt.Errorf("status %d", code)
		}
		return nil
	})
}

func (w *portalWorker) Close() {}
