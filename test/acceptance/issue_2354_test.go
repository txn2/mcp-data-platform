//go:build integration

package acceptance

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// Acceptance for #2354: no webhook source stores its secret. A request in
// every auth mode and from every sender preset goes to POST /hooks/{source}
// on the running platform; once the source's window is compacted, its view
// (which then serves the compacted file) and its raw table are searched for
// the secret as written, as base64 and as hex. The modes and presets come
// from internal/webhook/whsource, so one added later fails here until it has
// a request.
//
// Wire forms: the admin route takes one JSON object per source; each request
// is the content type its sender posts (application/json, or a form body for
// mattermost); trino_query takes string connection, sql and purpose.

// issue2354Sender is one request a source accepts: the preset or mode it is
// created with, the secret it holds, and the request.
type issue2354Sender struct {
	label  string
	mode   string
	secret string
	auth   map[string]any
	config map[string]any
	// path is what follows /hooks/{source}, for path_token.
	path  string
	build func(now time.Time, marker string) issue2223Request
	// public marks a secret that is the sender's public key, which a
	// source may store.
	public bool
}

// issue2354Senders are a request for every preset (#2223's senders, and the
// four hmac presets signed through the scheme the preset fills) and for the
// three modes no preset names.
func issue2354Senders(t *testing.T) []issue2354Sender {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []issue2354Sender
	for _, s := range issue2223Senders() {
		a := map[string]any{"preset": s.preset}
		mode := whsource.Source{Auth: whsource.Auth{Preset: s.preset}}.WithDefaults().Auth.Mode
		build := s.build
		out = append(out, issue2354Sender{
			label: s.preset, mode: mode, secret: s.secret(pub), auth: a, config: s.config,
			build:  func(now time.Time, marker string) issue2223Request { return build(now, marker, priv) },
			public: mode == whsource.AuthEd25519,
		})
	}
	for _, preset := range []string{whsource.PresetStandardWebhooks, whsource.PresetGitHub, whsource.PresetStripe, whsource.PresetPlatform} {
		out = append(out, issue2354HMACPreset(preset))
	}
	const token = "acc2354-plain-token-c2VjcmV0"
	out = append(out,
		issue2354Sender{
			label: "header_token", mode: whsource.AuthHeaderToken, secret: token,
			auth: map[string]any{"mode": "header_token", "header": "X-Hook-Token"},
			build: func(_ time.Time, marker string) issue2223Request {
				return issue2223Request{contentType: "application/json", body: []byte(`{"id":"` + marker + `","token":"` + token + `"}`),
					header: map[string]string{"X-Hook-Token": token}}
			},
		},
		issue2354Sender{
			label: "basic", mode: whsource.AuthBasic, secret: token,
			auth: map[string]any{"mode": "basic", "username": "hooks"},
			build: func(_ time.Time, marker string) issue2223Request {
				cred := base64.StdEncoding.EncodeToString([]byte("hooks:" + token))
				return issue2223Request{contentType: "application/json", body: []byte(`{"id":"` + marker + `"}`),
					header: map[string]string{"Authorization": "Basic " + cred}}
			},
		},
		issue2354Sender{
			label: "path_token", mode: whsource.AuthPathToken, secret: token, path: "/" + token,
			auth: map[string]any{"mode": "path_token"},
			build: func(_ time.Time, marker string) issue2223Request {
				return issue2223Request{contentType: "application/json", body: []byte(`{"id":"` + marker + `"}`)}
			},
		},
	)
	return out
}

// issue2354HMACPreset signs for an hmac preset with the scheme the preset
// fills, which #1996's criteria hold to the senders' own documentation.
func issue2354HMACPreset(preset string) issue2354Sender {
	secret := "acc2354-" + preset + "-c2VjcmV0"
	if preset == whsource.PresetStandardWebhooks {
		secret = issue2223GitLabSecret
	}
	a := whsource.Source{Auth: whsource.Auth{Preset: preset, Secret: secret}}.WithDefaults().Auth
	return issue2354Sender{
		label: preset, mode: whsource.AuthHMAC, secret: secret, auth: map[string]any{"preset": preset},
		build: func(now time.Time, marker string) issue2223Request {
			body := []byte(`{"id":"` + marker + `","type":"created"}`)
			ts := strconv.FormatInt(now.Unix(), 10)
			h := map[string]string{a.SignatureHeader: a.Scheme().Header(secret, marker, ts, body)}
			if a.TimestampHeader != "" {
				h[a.TimestampHeader] = ts
			}
			if a.IDHeader != "" {
				h[a.IDHeader] = marker
			}
			return issue2223Request{contentType: "application/json", body: body, header: h}
		},
	}
}

func TestIssue2354_NoSourceStoresItsSecret(t *testing.T) {
	c := connectFor(t, 10*time.Minute)
	db := issue1870DB(t)
	senders := issue2354Senders(t)

	presets, modes := map[string]bool{}, map[string]bool{}
	for _, s := range senders {
		presets[s.label], modes[s.mode] = true, true
	}
	for _, p := range whsource.PresetNames() {
		if !presets[p] {
			t.Errorf("preset %s has no request here; add one", p)
		}
	}
	for _, m := range whsource.AuthModes() {
		if !modes[m] {
			t.Errorf("auth mode %s has no request here; add one", m)
		}
	}

	type made struct {
		sender issue2354Sender
		source string
	}
	var sources []made
	for _, s := range senders {
		name := issue1870Name("s2354-" + strings.ReplaceAll(s.label, "_", "-"))
		auth := map[string]any{"secret": s.secret}
		for k, v := range s.auth {
			auth[k] = v
		}
		config := map[string]any{"persona": "admin", "compact_every_minutes": 1}
		for k, v := range s.config {
			config[k] = v
		}
		issue1870Create(t, c, map[string]any{"name": name, "connection": issue1870Conn, "auth": auth, "config": config})
		req := s.build(time.Now(), "m-"+name)
		res, body := issue1870Post(t, baseURL(), name+s.path, req.contentType, req.body, req.header)
		if res.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: the request was answered %d %s, want 202", s.label, res.StatusCode, body)
		}
		sources = append(sources, made{sender: s, source: name})
	}

	for _, m := range sources {
		issue1870AwaitRows(t, c, m.source, "", 1)
	}
	for _, m := range sources {
		issue1870WaitAllCompacted(t, db, m.source, 4*time.Minute)
	}
	for _, m := range sources {
		t.Run(m.sender.label, func(t *testing.T) {
			if m.sender.public {
				t.Logf("%s's secret is the sender's public key, which a source may store", m.sender.label)
				return
			}
			s := m.sender.secret
			var clauses []string
			for _, form := range []string{s, base64.StdEncoding.EncodeToString([]byte(s)), hex.EncodeToString([]byte(s))} {
				clauses = append(clauses, "strpos(payload, '"+strings.ReplaceAll(form, "'", "''")+"') > 0")
			}
			where := strings.Join(clauses, " OR ")
			if n := issue1870Rows(t, c, m.source, where); n != 0 {
				t.Errorf("the view holds the secret in %d events", n)
			}
			raw := issue1870Count(t, c, "SELECT count(*) FROM "+issue1870Table(m.source)+"_raw WHERE "+where)
			if raw != 0 {
				t.Errorf("the raw table holds the secret in %d events", raw)
			}
		})
	}
}
