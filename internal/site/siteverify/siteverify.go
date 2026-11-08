// Package siteverify checks that the person mapping a hostname controls it
// (#2518), by a DNS TXT record or by a token the hostname serves over HTTP.
//
// The HTTP check fetches a URL a caller chose, so it goes through the egress
// guard and refuses private addresses. Nothing it reads is echoed: the answer
// is compared with the token and the result is pass or a fixed sentence.
package siteverify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"

	"github.com/txn2/mcp-data-platform/internal/egressguard"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// The two places a token is looked for.
const (
	// TXTPrefix is the label the TXT record is published at.
	TXTPrefix = "_platform-site-verify."
	// WellKnownPath is where a hostname serves the token over HTTP.
	WellKnownPath = "/.well-known/platform-site-verify"
)

const (
	lookupTimeout = 10 * time.Second
	httpTimeout   = 10 * time.Second
	maxBody       = 4 << 10
	dnsTimeout    = 5 * time.Second
)

// Lookup reads a name's TXT records.
type Lookup func(ctx context.Context, name string) ([]string, error)

// Verifier runs the two checks.
type Verifier struct {
	TXT  Lookup
	HTTP *http.Client
}

// New builds a verifier: TXT lookups go to cfg.DNSServer when set, and the
// HTTP check through a guard admitting only cfg.AllowPrivateCIDRs beyond the
// public internet.
func New(cfg siteconfig.NetConfig) (*Verifier, error) {
	guard, err := egressguard.New(cfg.AllowPrivateCIDRs)
	if err != nil {
		return nil, fmt.Errorf("building the egress guard: %w", err)
	}
	client := outbound.NewClient(outbound.Options{Kind: outbound.KindSite, Base: guard.Transport(), Timeout: httpTimeout})
	return &Verifier{TXT: resolver(cfg.DNSServer).LookupTXT, HTTP: client}, nil
}

func resolver(server string) *net.Resolver {
	if server == "" {
		return net.DefaultResolver
	}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		d := net.Dialer{Timeout: dnsTimeout}
		return d.DialContext(ctx, network, server)
	}}
}

// Verify passes when either check finds the token.
func (v *Verifier) Verify(ctx context.Context, hostname, token string) error {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	if v.txtHolds(ctx, hostname, token) || v.httpHolds(ctx, hostname, token) {
		return nil
	}
	return &sitesvc.RefusedError{Msg: fmt.Sprintf(
		"%s is not verified: publish a TXT record at %s%s holding %q, or serve that text at http://%s%s, then verify again",
		hostname, TXTPrefix, hostname, token, hostname, WellKnownPath)}
}

func (v *Verifier) txtHolds(ctx context.Context, hostname, token string) bool {
	if v.TXT == nil {
		return false
	}
	records, err := v.TXT(ctx, TXTPrefix+hostname)
	if err != nil {
		return false
	}
	for _, r := range records {
		if strings.TrimSpace(r) == token {
			return true
		}
	}
	return false
}

func (v *Verifier) httpHolds(ctx context.Context, hostname, token string) bool {
	if v.HTTP == nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostname+WellKnownPath, http.NoBody) // #nosec G107 -- through the egress guard
	if err != nil {
		return false
	}
	res, err := v.HTTP.Do(req) // #nosec G704 -- the client's transport is the egress guard's
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return strings.TrimSpace(string(body)) == token
}
