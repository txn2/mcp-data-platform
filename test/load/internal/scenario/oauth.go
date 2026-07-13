package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/test/load/internal/harness"
	"github.com/txn2/mcp-data-platform/test/load/internal/report"
)

// oauthRegister drives the OAuth dynamic-client-registration endpoint
// (POST /register). This is the headless-drivable, bcrypt-bound path on the
// OAuth server: registration runs bcrypt.GenerateFromPassword at DefaultCost to
// hash the issued client secret, and it is governed by the SAME oauth.rate_limit
// limiter as the token endpoint.
//
// The platform's token endpoint itself is NOT headless-drivable for a load test:
// it supports only authorization_code and refresh_token grants (no
// client_credentials), and authorization_code requires a browser round-trip
// through an upstream OIDC IdP. /register exercises the same bcrypt cost and the
// same limiter without needing a stubbed IdP or seeded refresh tokens, so it is
// the representative OAuth throughput measurement. Run it once with
// oauth.rate_limit.enabled: true (expect 429s; the limiter engages) and once
// with it false (measure the raw bcrypt-bound path).
type oauthRegister struct {
	ok       atomic.Int64 // 2xx registrations
	tooMany  atomic.Int64 // 429 rate-limited
	otherErr atomic.Int64 // transport or other non-2xx
}

func (*oauthRegister) Name() string { return "oauth-token" }
func (*oauthRegister) Description() string {
	return "OAuth DCR (/register) load: the bcrypt-bound, rate-limited path"
}

func (*oauthRegister) Defaults() harness.RunDefaults {
	return harness.RunDefaults{Concurrency: 8, Duration: 30 * time.Second, Warmup: 3 * time.Second}
}

// Setup verifies the register endpoint is present and DCR is enabled by probing
// once; a 404/405 means OAuth/DCR is off in the target config.
func (s *oauthRegister) Setup(ctx context.Context, env *harness.Env) error {
	code, err := s.register(ctx, env, "loadgen-probe")
	if err != nil {
		return fmt.Errorf("oauth-token setup probe: %w", err)
	}
	if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
		return fmt.Errorf("oauth register endpoint returned %d; enable oauth + oauth.dcr in the target config", code)
	}
	return nil
}

func (*oauthRegister) Teardown(context.Context, *harness.Env) {}

// ResetForMeasure zeroes the 2xx/429/other tallies at the warmup→measured
// boundary so they count only the measured window (harness.MeasuredResetter).
func (s *oauthRegister) ResetForMeasure() {
	s.ok.Store(0)
	s.tooMany.Store(0)
	s.otherErr.Store(0)
}

func (s *oauthRegister) NewWorker(_ context.Context, env *harness.Env, id int) (harness.Worker, error) {
	return &oauthWorker{sc: s, env: env, id: id}, nil
}

func (s *oauthRegister) Assess(_ *harness.Env, rep *report.Report) []report.Assertion {
	reach := report.Assertion{
		Name:    "oauth-register-reachable",
		Passed:  s.ok.Load()+s.tooMany.Load() > 0,
		Message: fmt.Sprintf("2xx=%d 429=%d other=%d", s.ok.Load(), s.tooMany.Load(), s.otherErr.Load()),
	}
	return []report.Assertion{reach, throughputAssertion(rep, "oauth_register")}
}

// register POSTs a DCR request and returns the HTTP status code. It uses the
// anonymous client (registration is unauthenticated, gated only by the
// configured redirect-URI patterns).
func (s *oauthRegister) register(ctx context.Context, env *harness.Env, name string) (int, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":   name,
		"redirect_uris": []string{"http://localhost:12345/callback"},
	})
	url := env.Target.URL("/register")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.Anon.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

type oauthWorker struct {
	sc  *oauthRegister
	env *harness.Env
	id  int
	n   int
}

func (w *oauthWorker) Iterate(ctx context.Context) {
	w.n++
	name := fmt.Sprintf("loadgen-w%d-%d", w.id, w.n)
	_ = w.env.Timed("oauth_register", func() error {
		code, err := w.sc.register(ctx, w.env, name)
		switch {
		case err != nil:
			w.sc.otherErr.Add(1)
			return err
		case code == http.StatusTooManyRequests:
			w.sc.tooMany.Add(1)
			return errors.New("rate limited (429)")
		case code >= 200 && code < 300:
			w.sc.ok.Add(1)
			return nil
		default:
			w.sc.otherErr.Add(1)
			return fmt.Errorf("unexpected status %d", code)
		}
	})
}

func (w *oauthWorker) Close() {}
