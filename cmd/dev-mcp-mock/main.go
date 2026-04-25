// dev-mcp-mock is a development-only fixture that dev/start.sh
// launches alongside the platform so operators can exercise the MCP
// gateway feature end-to-end without setting up a real upstream. Two
// services run from a single process:
//
//   - :9180  Mock OAuth 2.1 provider with authorization_code+PKCE,
//            refresh_token, and client_credentials grants. Access
//            tokens default to a 1-hour TTL (override with
//            ACCESS_TTL_SECONDS=10 to exercise refresh quickly).
//   - :9181  Mock MCP server (streamable HTTP) with three trivial
//            tools: echo, add, now. Bearer-permissive by default;
//            set STRICT_AUTH=1 to validate that the bearer token
//            was issued by the OAuth server above.
//
// dev/seed.sql pre-creates an mcp connection named "dev-mock"
// pointing at this server, so opening the admin portal immediately
// shows dev-mock__echo / dev-mock__add / dev-mock__now in the
// platform's tools/list.
//
// Usage:
//   go run ./cmd/dev-mcp-mock
//   STRICT_AUTH=1 go run ./cmd/dev-mcp-mock
//   ACCESS_TTL_SECONDS=10 go run ./cmd/dev-mcp-mock
//
// Health:
//   curl http://localhost:9180/.health
//   curl http://localhost:9181/.health
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// tokenStore is a tiny in-memory token registry for the mock OAuth
// provider. Counters are bumped on each issuance so the tester can see
// whether a fresh token was minted (proves refresh rotation worked).
type tokenStore struct {
	mu       sync.Mutex
	issued   map[string]issuedToken // access_token -> details
	refresh  map[string]string      // refresh_token -> connection name (just for replay-prevention)
	pkce     map[string]pkceState   // code -> state
	accessN  int
	refreshN int
}

type issuedToken struct {
	AccessN     int
	IssuedAt    time.Time
	ExpiresAt   time.Time
	Scope       string
	IsRefreshed bool
}

type pkceState struct {
	CodeChallenge       string
	CodeChallengeMethod string
	RedirectURI         string
	State               string
	ClientID            string
	IssuedAt            time.Time
}

var (
	store = &tokenStore{
		issued:  map[string]issuedToken{},
		refresh: map[string]string{},
		pkce:    map[string]pkceState{},
	}
	accessTTL = 1 * time.Hour
)

func main() {
	if v := os.Getenv("ACCESS_TTL_SECONDS"); v != "" {
		var s int
		_, _ = fmt.Sscanf(v, "%d", &s)
		if s > 0 {
			accessTTL = time.Duration(s) * time.Second
		}
	}

	go startOAuth(":9180")
	go startMCP(":9181")

	log.Printf("livetest: OAuth provider on :9180, MCP upstream on :9181 (access_ttl=%s)", accessTTL)

	// Block forever.
	select {}
}

// ---------- OAuth provider on :9180 ----------

func startOAuth(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/authorize", oauthAuthorize)
	mux.HandleFunc("/token", oauthToken)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("oauth: listening %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("oauth: %v", err)
	}
}

func oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	state := q.Get("state")
	if redirect == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}

	store.mu.Lock()
	store.accessN++
	code := fmt.Sprintf("auth-code-%d", store.accessN)
	store.pkce[code] = pkceState{
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
		RedirectURI:         redirect,
		State:               state,
		ClientID:            q.Get("client_id"),
		IssuedAt:            time.Now(),
	}
	store.mu.Unlock()

	dest, err := url.Parse(redirect)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	qq := dest.Query()
	qq.Set("code", code)
	if state != "" {
		qq.Set("state", state)
	}
	dest.RawQuery = qq.Encode()
	log.Printf("oauth: /authorize → redirect to %s (code=%s)", dest, code)
	http.Redirect(w, r, dest.String(), http.StatusFound)
}

func oauthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	grant := r.Form.Get("grant_type")
	switch grant {
	case "authorization_code":
		oauthExchangeCode(w, r)
	case "refresh_token":
		oauthRefresh(w, r)
	case "client_credentials":
		oauthClientCredentials(w, r)
	default:
		writeJSONErr(w, http.StatusBadRequest, "unsupported_grant_type", grant)
	}
}

func oauthExchangeCode(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")
	verifier := r.Form.Get("code_verifier")
	store.mu.Lock()
	state, ok := store.pkce[code]
	if !ok {
		store.mu.Unlock()
		writeJSONErr(w, http.StatusBadRequest, "invalid_grant", "unknown code")
		return
	}
	delete(store.pkce, code)
	// Verify PKCE: SHA-256 of verifier, base64url-no-pad, == code_challenge.
	got := pkceS256(verifier)
	if state.CodeChallengeMethod == "S256" && got != state.CodeChallenge {
		store.mu.Unlock()
		writeJSONErr(w, http.StatusBadRequest, "invalid_grant", fmt.Sprintf("PKCE mismatch: got=%s expected=%s", got, state.CodeChallenge))
		return
	}
	access, refresh := mintLocked()
	store.mu.Unlock()
	log.Printf("oauth: /token authorization_code OK, access=%s refresh=%s", access, refresh)
	writeTokenResponse(w, access, refresh)
}

func oauthRefresh(w http.ResponseWriter, r *http.Request) {
	rt := r.Form.Get("refresh_token")
	store.mu.Lock()
	if _, ok := store.refresh[rt]; !ok {
		store.mu.Unlock()
		writeJSONErr(w, http.StatusBadRequest, "invalid_grant", "unknown refresh_token")
		return
	}
	// Rotate: invalidate old refresh, mint new pair.
	delete(store.refresh, rt)
	access, refresh := mintLocked()
	t := store.issued[access]
	t.IsRefreshed = true
	store.issued[access] = t
	store.mu.Unlock()
	log.Printf("oauth: /token refresh_token rotated old=%s → access=%s refresh=%s", rt, access, refresh)
	writeTokenResponse(w, access, refresh)
}

func oauthClientCredentials(w http.ResponseWriter, _ *http.Request) {
	store.mu.Lock()
	access, _ := mintLocked()
	store.mu.Unlock()
	log.Printf("oauth: /token client_credentials OK, access=%s (no refresh)", access)
	// client_credentials does not return a refresh_token.
	resp := map[string]any{
		"access_token": access,
		"token_type":   "Bearer",
		"expires_in":   int(accessTTL.Seconds()),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func mintLocked() (access, refresh string) {
	store.accessN++
	store.refreshN++
	access = fmt.Sprintf("acc-%d", store.accessN)
	refresh = fmt.Sprintf("ref-%d", store.refreshN)
	now := time.Now()
	store.issued[access] = issuedToken{
		AccessN:   store.accessN,
		IssuedAt:  now,
		ExpiresAt: now.Add(accessTTL),
		Scope:     "api",
	}
	store.refresh[refresh] = "live"
	return access, refresh
}

func writeTokenResponse(w http.ResponseWriter, access, refresh string) {
	resp := map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL.Seconds()),
		"scope":         "api",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeJSONErr(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": desc,
	})
}

// pkceS256 computes the base64url-no-pad SHA-256 of the input — the
// canonical S256 transformation from RFC 7636.
func pkceS256(verifier string) string {
	if verifier == "" {
		return ""
	}
	// Inline implementation to avoid extra imports.
	return base64URLNoPadSHA256([]byte(verifier))
}

// ---------- MCP upstream on :9181 ----------

func startMCP(addr string) {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "livetest-mcp", Version: "0.0.1"},
		nil,
	)

	type echoArgs struct {
		Message string `json:"message"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo input message"},
		func(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + args.Message}},
			}, nil, nil
		})

	type addArgs struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "add", Description: "Sum two integers"},
		func(_ context.Context, _ *mcp.CallToolRequest, args addArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%d", args.A+args.B)}},
			}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "now", Description: "Return current UTC time"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: time.Now().UTC().Format(time.RFC3339)}},
			}, nil, nil
		})

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/.health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/mcp", authMiddleware(mcpHandler))
	mux.Handle("/mcp/", authMiddleware(mcpHandler))

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("mcp:   listening %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("mcp: %v", err)
	}
}

// authMiddleware enforces Bearer token auth. Any token issued by the
// OAuth server (in store.issued, not yet expired) is accepted, OR the
// static token "static-bearer-token" for non-OAuth tests.
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("STRICT_AUTH") == "" {
			// Permissive: accept any non-empty Authorization header or none at all.
			next.ServeHTTP(w, r)
			return
		}
		hdr := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(hdr, prefix) {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		tok := strings.TrimPrefix(hdr, prefix)
		if tok == "static-bearer-token" {
			next.ServeHTTP(w, r)
			return
		}
		store.mu.Lock()
		t, ok := store.issued[tok]
		store.mu.Unlock()
		if !ok {
			http.Error(w, "unknown token", http.StatusUnauthorized)
			return
		}
		if time.Now().After(t.ExpiresAt) {
			http.Error(w, "token expired", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
