package connprobe_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
)

// A failure carries the upstream's own words beside what was attempted: the
// two answer different questions, and flattening them into one string is what
// made a broken connection read like a routing mistake (#1805).
func TestFailure(t *testing.T) {
	res := connprobe.Failure("connection could not be opened", errors.New("invalid userinfo"))

	if res.OK {
		t.Error("a failure reported OK")
	}
	if res.Detail != "connection could not be opened" {
		t.Errorf("detail = %q", res.Detail)
	}
	if res.Error != "invalid userinfo" {
		t.Errorf("error = %q", res.Error)
	}
}

// A failure with no error behind it — a connection this toolkit does not serve
// — leaves the error empty rather than inventing one.
func TestFailure_NoError(t *testing.T) {
	res := connprobe.Failure("connection is not served here", nil)

	if res.OK || res.Error != "" {
		t.Errorf("unexpected result: %+v", res)
	}
}

// A success says what answered, because a bare tick against the wrong
// credential looks identical to one against the right credential.
func TestSuccess(t *testing.T) {
	res := connprobe.Success("the query engine answered SELECT 1")

	if !res.OK {
		t.Error("a success reported not-OK")
	}
	if res.Error != "" {
		t.Errorf("a success carries an error: %q", res.Error)
	}
	if res.Detail == "" {
		t.Error("a success says nothing about what answered")
	}
}

// A failure carries the upstream's own words, and some of those words are a
// DSN. The response a test endpoint returns must not hand out the credential
// its sibling GET redacts.
func TestFailure_ScrubsURLCredentials(t *testing.T) {
	res := connprobe.Failure("connection could not be opened", errors.New(
		`invalid DSN: parse "https://analyst:s3cr3t@trino.example.com:443/warehouse/public?source=mcp": net/url: invalid userinfo`))

	if strings.Contains(res.Error, "s3cr3t") {
		t.Fatalf("the failure carries the credential: %s", res.Error)
	}
	if !strings.Contains(res.Error, "[REDACTED]@trino.example.com") {
		t.Errorf("the host the attempt used is no longer readable: %s", res.Error)
	}
	// What was wrong has to survive the scrub, or the report is useless.
	if !strings.Contains(res.Error, "invalid userinfo") {
		t.Errorf("the reason was scrubbed away: %s", res.Error)
	}
}

// An error with no URL in it is passed through untouched.
func TestFailure_LeavesOrdinaryErrorsAlone(t *testing.T) {
	const text = "dial tcp 127.0.0.1:9: connect: connection refused"
	if got := connprobe.Failure("could not reach", errors.New(text)).Error; got != text {
		t.Errorf("error = %q, want %q", got, text)
	}
}

// A password holding an apostrophe, a credential in a query string and a
// key=value DSN are scrubbed from a failure's text (#2358).
func TestFailureScrubsEveryCredentialForm(t *testing.T) {
	cases := map[string]string{
		"apostrophe in userinfo": `parse "trino://admin:it's-s3cret@trino:8080": invalid port`,
		"query parameter":        `Get "https://api.example.com/v1?page=2&api_key=s3cret&x=1": dial tcp: refused`,
		"query password":         `open postgres://db/x?sslmode=disable&password=s3cret failed`,
		"key=value dsn":          `cannot connect: host=db user=app password=s3cret dbname=x`,
		"token parameter":        `refused (access_token=s3cret)`,
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			r := connprobe.Failure("could not reach", errors.New(msg))
			if strings.Contains(r.Error, "s3cret") || !strings.Contains(r.Error, "[REDACTED]") {
				t.Errorf("error = %q, want the credential replaced", r.Error)
			}
		})
	}
	r := connprobe.Failure("x", errors.New(`Get "https://api.example.com/v1?page=2": refused`))
	if !strings.Contains(r.Error, "page=2") {
		t.Errorf("error = %q, want a parameter that is not a credential kept", r.Error)
	}
}
