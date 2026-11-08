// Package sitecert is the built-in certificate option for verified domains
// (#2518): ACME through autocert, a listener of its own, and a certificate
// cache in PostgreSQL shared by every replica.
//
// Everything autocert stores holds a private key (the account key, and each
// certificate with its key), so a row is sealed, bound to its cache key. A
// row copied onto another key does not open.
package sitecert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/site"
)

const readHeaderTimeout = 10 * time.Second

// Sealer keeps a row's data sealed, bound to its key.
type Sealer interface {
	SealBound(binding, plaintext string) (string, error)
	OpenBound(binding, sealed string) (string, error)
}

// Cache is autocert's cache, in site_certificates.
type Cache struct {
	DB     *sql.DB
	Sealer Sealer
}

// Get reads and opens one entry.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	var sealed string
	err := c.DB.QueryRowContext(ctx, `SELECT data FROM site_certificates WHERE key = $1`, key).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, autocert.ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("reading a certificate: %w", err)
	}
	plain, err := c.Sealer.OpenBound(key, sealed)
	if err != nil {
		return nil, fmt.Errorf("opening a certificate: %w", err)
	}
	return []byte(plain), nil
}

// Put seals and writes one entry.
func (c *Cache) Put(ctx context.Context, key string, data []byte) error {
	sealed, err := c.Sealer.SealBound(key, string(data))
	if err != nil {
		return fmt.Errorf("sealing a certificate: %w", err)
	}
	_, err = c.DB.ExecContext(ctx, `
		INSERT INTO site_certificates (key, data) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET data = EXCLUDED.data, updated_at = NOW()`, key, sealed)
	if err != nil {
		return fmt.Errorf("writing a certificate: %w", err)
	}
	return nil
}

// Delete removes one entry.
func (c *Cache) Delete(ctx context.Context, key string) error {
	if _, err := c.DB.ExecContext(ctx, `DELETE FROM site_certificates WHERE key = $1`, key); err != nil {
		return fmt.Errorf("deleting a certificate: %w", err)
	}
	return nil
}

// Hosts reports whether a hostname is one the platform serves.
type Hosts func(ctx context.Context, host string) bool

// Listener serves the sites over TLS with automatic certificates.
type Listener struct {
	server *http.Server
}

// New builds the listener over handler; a hostname gets a certificate only
// when hosts says the platform serves it, so a request naming any other
// hostname can never make the platform order one.
func New(cfg siteconfig.ACMEConfig, db *sql.DB, sealer Sealer, hosts Hosts, handler http.Handler) (*Listener, error) {
	client, err := acmeClient(cfg)
	if err != nil {
		return nil, err
	}
	manager := &autocert.Manager{
		Prompt: autocert.AcceptTOS, Email: cfg.Email, Client: client, Cache: &Cache{DB: db, Sealer: sealer},
		HostPolicy: func(ctx context.Context, host string) error {
			if hosts(ctx, site.NormalizeHost(host)) {
				return nil
			}
			return fmt.Errorf("sites: %s is not a verified hostname", host) //nolint:err113 // autocert's policy refusal
		},
	}
	tlsConfig := manager.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12
	return &Listener{server: &http.Server{
		Addr: cfg.Address, Handler: handler, TLSConfig: tlsConfig, ReadHeaderTimeout: readHeaderTimeout,
	}}, nil
}

func acmeClient(cfg siteconfig.ACMEConfig) (*acme.Client, error) {
	opts := outbound.Options{Kind: outbound.KindSite}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("sites.tls.acme.ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("sites.tls.acme.ca_file holds no certificate")
		}
		opts.Base = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	}
	return &acme.Client{DirectoryURL: cfg.DirectoryURL, HTTPClient: outbound.NewClient(opts)}, nil
}

// Start serves until Stop.
func (l *Listener) Start() error {
	err := l.server.ListenAndServeTLS("", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("sites: the TLS listener stopped: %w", err)
}

// Stop drains the listener.
func (l *Listener) Stop(ctx context.Context) error {
	return l.server.Shutdown(ctx) //nolint:wrapcheck // the caller logs it
}
