package apigateway_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyrender"
	"github.com/txn2/mcp-data-platform/internal/platform/connalert"
	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	apigateway "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// jwtBearerIDP is a token endpoint that verifies the RFC 7523 assertion
// against the registered public key and issues an access token, and an
// upstream that accepts only that access token. refuse flips the endpoint to
// answering invalid_grant, which is the incident.
type jwtBearerIDP struct {
	server *httptest.Server
	key    *rsa.PrivateKey

	mu     sync.Mutex
	refuse bool
}

func newJWTBearerIDP(t *testing.T) *jwtBearerIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &jwtBearerIDP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", idp.token)
	mux.HandleFunc("/things", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer issued-for-svc-integration" {
			http.Error(w, "unknown token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (i *jwtBearerIDP) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	_ = r.ParseForm()
	w.Header().Set("Content-Type", "application/json")
	i.mu.Lock()
	refuse := i.refuse
	i.mu.Unlock()
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(r.PostForm.Get("assertion"), claims,
		func(*jwt.Token) (any, error) { return &i.key.PublicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(i.server.URL+"/token"))
	if refuse || err != nil || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" ||
		claims["sub"] != "svc-integration" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"user hasn't approved this consumer"}`))
		return
	}
	// No expires_in, the shape some upstreams of this grant answer with.
	_, _ = w.Write([]byte(`{"access_token":"issued-for-svc-integration","token_type":"Bearer"}`))
}

func (i *jwtBearerIDP) setRefuse(v bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.refuse = v
}

func (i *jwtBearerIDP) keyPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(i.key)}))
}

// memAlerts is an in-memory connalert.AlertStore with the conflict rule the
// Postgres store has: one open row per connection.
type memAlerts struct {
	mu   sync.Mutex
	open map[string]connalert.Alert
}

func (m *memAlerts) Open(_ context.Context, a connalert.Alert) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, held := m.open[a.Kind+"/"+a.Name]; held {
		return false, nil
	}
	m.open[a.Kind+"/"+a.Name] = a
	return true, nil
}

func (m *memAlerts) Clear(_ context.Context, kind, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.open, kind+"/"+name)
	return nil
}

func (*memAlerts) ClaimEscalations(context.Context, time.Duration, time.Time) ([]connalert.Alert, error) {
	return nil, nil
}

func (m *memAlerts) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.open)
}

type fixedSettings struct{ s connalert.Settings }

func (f fixedSettings) Get(context.Context) (*connalert.Settings, error) { return &f.s, nil }
func (fixedSettings) Set(context.Context, connalert.Settings, string) error {
	return nil
}

type defaultPrefs struct{}

func (defaultPrefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

func (defaultPrefs) Set(_ context.Context, email string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

// mailQueue keeps what the notification substrate accepted.
type mailQueue struct {
	mu   sync.Mutex
	rows []notification.Notification
}

func (q *mailQueue) Enqueue(_ context.Context, n notification.Notification) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rows = append(q.rows, n)
	return nil
}

func (q *mailQueue) all() []notification.Notification {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]notification.Notification(nil), q.rows...)
}

func (*mailQueue) ClaimImmediate(context.Context, time.Duration, notification.TransportFilter) (*notification.Notification, error) {
	return nil, notification.ErrNoWork
}

func (*mailQueue) ClaimDigest(context.Context, time.Duration, notification.TransportFilter) ([]notification.Notification, error) {
	return nil, notification.ErrNoWork
}
func (*mailQueue) MarkSent(context.Context, []int64) error                     { return nil }
func (*mailQueue) Retry(context.Context, []int64, string, time.Duration) error { return nil }
func (*mailQueue) Fail(context.Context, []int64, string) error                 { return nil }
func (*mailQueue) PurgeOld(context.Context, time.Duration, time.Duration) (int64, error) {
	return 0, nil
}

// TestJWTBearerRefusalReachesTheAlert_Integration is the cross-component proof
// for #1734. A jwt_bearer connection parsed from the stored config shape, served
// by the toolkit, called through a real MCP client, exchanges a real signed
// assertion at a real HTTP token endpoint; when the endpoint refuses it, the
// refusal travels through the authenticator, the auth-event writer the toolkit
// threads into it, and the platform's alerter, into a queued mail to the
// operator's recipients that renders as a refused assertion; and the next
// accepted exchange closes the alert.
//
// A unit test on each piece proves each piece. What this proves is the wiring:
// that the toolkit hands the jwt_bearer authenticator the writer at all, that the
// connection name the alert is keyed by survives parse and registration, and that
// the marker the email branches on is still set when it gets there.
func TestJWTBearerRefusalReachesTheAlert_Integration(t *testing.T) {
	idp := newJWTBearerIDP(t)
	cfg, err := apigateway.ParseConfig(map[string]any{
		"base_url":            idp.server.URL,
		"connection_name":     "erp",
		"auth_mode":           "oauth",
		"oauth_grant":         "jwt_bearer",
		"oauth_token_url":     idp.server.URL + "/token",
		"jwt_private_key_pem": idp.keyPEM(),
		"jwt_issuer":          "3MVG9-consumer-key",
		"jwt_subject":         "svc-integration",
		"trust_level":         "trusted",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	alerts := &memAlerts{open: map[string]connalert.Alert{}}
	queue := &mailQueue{}
	enq := notification.NewEnqueuer(defaultPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	alerter := connalert.NewAlerter(connalert.Config{
		Settings: fixedSettings{s: connalert.Settings{Enabled: true, Recipients: []string{"oncall@example.com"}}},
		Alerts:   alerts,
		Enqueuer: enq,
		BaseURL:  "https://platform.example.com",
	})
	if alerter == nil {
		t.Fatal("NewAlerter returned nil with every dependency supplied")
	}

	// The first connection's upstream accepts the assertion and the call
	// reaches it with the access token the exchange issued.
	call := serveJWTBearer(t, cfg, alerter)
	if res, body := call(); res.IsError || !strings.Contains(body, `"status": 200`) {
		t.Fatalf("the accepted call failed: %s", body)
	}

	// Refused. A connection registered after the refusal exchanges on its
	// first call, where the first one would answer from its cached token.
	idp.setRefuse(true)
	call = serveJWTBearer(t, cfg, alerter)
	for range 2 {
		res, body := call()
		if !res.IsError {
			t.Fatalf("a refused exchange produced a successful call: %s", body)
		}
		if !strings.Contains(body, "invalid_grant: user hasn't approved this consumer") {
			t.Errorf("the model was not told what the upstream said: %s", body)
		}
	}

	mails := queue.all()
	if len(mails) != 1 {
		t.Fatalf("queued %d alerts over two refused calls, want 1", len(mails))
	}
	mail := mails[0]
	if mail.Recipient != "oncall@example.com" {
		t.Errorf("alert went to %q, want the operator's recipient", mail.Recipient)
	}
	if got := notifyrender.Subject(mail); got != `The connection "erp" cannot get an access token` {
		t.Errorf("subject = %q", got)
	}
	if conn := mail.Payload.Connection; conn == nil || !conn.SignedAssertion || conn.Kind != apigateway.Kind ||
		conn.Description != "user hasn't approved this consumer" {
		t.Errorf("payload = %+v", mail.Payload.Connection)
	}
	if alerts.count() != 1 {
		t.Fatalf("open alerts = %d, want 1", alerts.count())
	}

	// The upstream approves the user; the next call exchanges again and the
	// accepted exchange closes the alert.
	idp.setRefuse(false)
	if res, body := call(); res.IsError {
		t.Fatalf("the call after the upstream accepted failed: %s", body)
	}
	if alerts.count() != 0 {
		t.Errorf("open alerts = %d after an accepted exchange, want 0", alerts.count())
	}
}

// serveJWTBearer registers cfg on a new toolkit wired to announce through
// alerter, serves it to an in-memory MCP client, and returns a function that
// calls api_invoke_endpoint through it and reads the result's text.
func serveJWTBearer(t *testing.T, cfg apigateway.Config, alerter *connalert.Alerter) func() (*mcp.CallToolResult, string) {
	t.Helper()
	tk := apigateway.NewMulti(apigateway.MultiConfig{
		DefaultName: "erp",
		Instances:   map[string]apigateway.Config{"erp": cfg},
	})
	t.Cleanup(func() { _ = tk.Close() })
	tk.SetAuthEvents(authevents.NewWriter(authevents.NewMemoryStore(), nil).WithRevocations(alerter))

	server := mcp.NewServer(&mcp.Implementation{Name: "gw-jwt-bearer", Version: "v0"}, nil)
	tk.RegisterTools(server)
	ctx := t.Context()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	return func() (*mcp.CallToolResult, string) {
		t.Helper()
		res, callErr := sess.CallTool(ctx, &mcp.CallToolParams{
			Name:      apigateway.ToolInvokeEndpoint,
			Arguments: map[string]any{"connection": "erp", "method": http.MethodGet, "path": "/things"},
		})
		if callErr != nil {
			t.Fatalf("CallTool: %v", callErr)
		}
		var b strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				_, _ = b.WriteString(tc.Text)
			}
		}
		return res, b.String()
	}
}
