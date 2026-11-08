package sitecert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme/autocert"

	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
)

const privateKey = "-----BEGIN EC PRIVATE KEY-----\nMHcCAQEE-the-private-key\n-----END EC PRIVATE KEY-----\n"

var errDown = errors.New("down")

// fakeSealer seals to an opaque counter, so a sealed value holds no form of
// the plaintext, and opens only with the binding it was sealed under.
type fakeSealer struct {
	rows     map[string][2]string
	bindings []string
	failSeal error
}

func newSealer() *fakeSealer { return &fakeSealer{rows: map[string][2]string{}} }

func (f *fakeSealer) SealBound(binding, plaintext string) (string, error) {
	if f.failSeal != nil {
		return "", f.failSeal
	}
	sealed := fmt.Sprintf("sealed-%d", len(f.rows)+1)
	f.rows[sealed] = [2]string{binding, plaintext}
	return sealed, nil
}

func (f *fakeSealer) OpenBound(binding, sealed string) (string, error) {
	f.bindings = append(f.bindings, binding)
	row, ok := f.rows[sealed]
	if !ok || row[0] != binding {
		return "", errors.New("sealed for something else")
	}
	return row[1], nil
}

func newCache(t *testing.T) (*Cache, sqlmock.Sqlmock, *fakeSealer) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	sealer := newSealer()
	return &Cache{DB: db, Sealer: sealer}, mock, sealer
}

// argument captures what the SQL statement was handed.
type argument struct{ got []driver.Value }

func (a *argument) Match(v driver.Value) bool {
	a.got = append(a.got, v)
	return true
}

func q(s string) string { return regexp.QuoteMeta(s) }

func TestPut_WritesOnlyTheSealedValue(t *testing.T) {
	c, mock, sealer := newCache(t)
	key, data := &argument{}, &argument{}
	mock.ExpectExec(q("INSERT INTO site_certificates")).WithArgs(key, data).WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, c.Put(context.Background(), "www.example.com", []byte(privateKey)))

	require.Equal(t, []driver.Value{"www.example.com"}, key.got)
	require.Equal(t, []driver.Value{"sealed-1"}, data.got, "what is written is what the sealer returned")
	assert.Equal(t, [2]string{"www.example.com", privateKey}, sealer.rows["sealed-1"], "sealed bound to the row's key")

	escaped, err := json.Marshal(privateKey)
	require.NoError(t, err)
	for _, arg := range []driver.Value{key.got[0], data.got[0]} {
		s, ok := arg.(string)
		require.True(t, ok)
		for _, form := range []string{
			privateKey, "the-private-key",
			base64.StdEncoding.EncodeToString([]byte(privateKey)),
			hex.EncodeToString([]byte(privateKey)),
			url.QueryEscape(privateKey),
			string(escaped[1 : len(escaped)-1]),
		} {
			assert.NotContains(t, s, form)
		}
	}
}

func TestPut_ASealFailureWritesNothing(t *testing.T) {
	c, _, sealer := newCache(t)
	sealer.failSeal = errDown
	err := c.Put(context.Background(), "k", []byte(privateKey))
	require.ErrorIs(t, err, errDown)
	assert.ErrorContains(t, err, "sealing a certificate")
}

func TestPut_AWriteFailure(t *testing.T) {
	c, mock, _ := newCache(t)
	mock.ExpectExec(q("INSERT INTO site_certificates")).WillReturnError(errDown)
	err := c.Put(context.Background(), "k", []byte(privateKey))
	require.ErrorContains(t, err, "writing a certificate")
	assert.NotContains(t, err.Error(), "the-private-key", "an error repeats no key material")
}

func TestGet_OpensWithTheRowsOwnKey(t *testing.T) {
	c, mock, sealer := newCache(t)
	sealed, err := sealer.SealBound("www.example.com", privateKey)
	require.NoError(t, err)

	mock.ExpectQuery(q("SELECT data FROM site_certificates WHERE key = $1")).WithArgs("www.example.com").
		WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow(sealed))
	got, err := c.Get(context.Background(), "www.example.com")
	require.NoError(t, err)
	assert.Equal(t, privateKey, string(got))
	assert.Equal(t, []string{"www.example.com"}, sealer.bindings)
}

func TestGet_ARowCopiedOntoAnotherKeyDoesNotOpen(t *testing.T) {
	c, mock, sealer := newCache(t)
	sealed, err := sealer.SealBound("victim.example.com", privateKey)
	require.NoError(t, err)

	mock.ExpectQuery(q("SELECT data")).WithArgs("attacker.example.com").WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow(sealed))
	got, err := c.Get(context.Background(), "attacker.example.com")
	require.ErrorContains(t, err, "opening a certificate")
	assert.Nil(t, got)
	assert.Equal(t, []string{"attacker.example.com"}, sealer.bindings, "bound to the key asked for, never the row's claim")
}

func TestGet_Misses(t *testing.T) {
	c, mock, _ := newCache(t)
	mock.ExpectQuery(q("SELECT data")).WillReturnError(sql.ErrNoRows)
	_, err := c.Get(context.Background(), "none")
	require.ErrorIs(t, err, autocert.ErrCacheMiss, "autocert orders a new certificate on exactly this error")

	mock.ExpectQuery(q("SELECT data")).WillReturnError(errDown)
	_, err = c.Get(context.Background(), "k")
	require.ErrorContains(t, err, "reading a certificate")
	assert.NotErrorIs(t, err, autocert.ErrCacheMiss, "a database failure must not order a certificate")
}

func TestDelete(t *testing.T) {
	c, mock, _ := newCache(t)
	mock.ExpectExec(q("DELETE FROM site_certificates WHERE key = $1")).WithArgs("k").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, c.Delete(context.Background(), "k"))
	mock.ExpectExec(q("DELETE FROM site_certificates")).WillReturnError(errDown)
	require.ErrorContains(t, c.Delete(context.Background(), "k"), "deleting a certificate")
}

func writeCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestACMEClient(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	fetch := func(c *http.Client) (*http.Response, error) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
		require.NoError(t, err)
		return c.Do(req)
	}

	t.Run("the configured root is trusted", func(t *testing.T) {
		c, err := acmeClient(siteconfig.ACMEConfig{DirectoryURL: srv.URL, CAFile: writeCA(t, srv)})
		require.NoError(t, err)
		assert.Equal(t, srv.URL, c.DirectoryURL)
		res, err := fetch(c.HTTPClient)
		require.NoError(t, err)
		_ = res.Body.Close()
		assert.Equal(t, http.StatusNoContent, res.StatusCode)
	})
	t.Run("with no root the server's certificate is not trusted", func(t *testing.T) {
		c, err := acmeClient(siteconfig.ACMEConfig{DirectoryURL: srv.URL})
		require.NoError(t, err)
		res, err := fetch(c.HTTPClient)
		if res != nil {
			_ = res.Body.Close()
		}
		var unknown x509.UnknownAuthorityError
		require.ErrorAs(t, err, &unknown)
	})
	t.Run("a file that is missing", func(t *testing.T) {
		_, err := acmeClient(siteconfig.ACMEConfig{CAFile: filepath.Join(t.TempDir(), "none.pem")})
		require.ErrorContains(t, err, "sites.tls.acme.ca_file")
	})
	t.Run("a file with no certificate", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ca.pem")
		require.NoError(t, os.WriteFile(path, []byte("not a certificate"), 0o600))
		_, err := acmeClient(siteconfig.ACMEConfig{CAFile: path})
		require.ErrorContains(t, err, "holds no certificate")
	})
}

func TestNew_ABadCAFileIsRefused(t *testing.T) {
	_, err := New(siteconfig.ACMEConfig{CAFile: filepath.Join(t.TempDir(), "none.pem")}, nil, nil, nil, http.NotFoundHandler())
	require.ErrorContains(t, err, "sites.tls.acme.ca_file")
}

// A hostname the platform does not serve is refused before anything is
// ordered, and the policy is asked with the name as a request's Host names it.
func TestListener_NoCertificateForAHostnameThePlatformDoesNotServe(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var asked []string
	l, err := New(siteconfig.ACMEConfig{Address: ":0", DirectoryURL: "http://127.0.0.1:1/never"}, db, newSealer(),
		func(_ context.Context, host string) bool {
			asked = append(asked, host)
			return false
		}, http.NotFoundHandler())
	require.NoError(t, err)
	assert.Equal(t, uint16(tls.VersionTLS12), l.server.TLSConfig.MinVersion)

	cert, err := l.server.TLSConfig.GetCertificate(&tls.ClientHelloInfo{ServerName: "other.example.com"})
	require.ErrorContains(t, err, "other.example.com is not a verified hostname")
	assert.Nil(t, cert)
	assert.Equal(t, []string{"other.example.com"}, asked)
	require.NoError(t, mock.ExpectationsWereMet(), "the policy refuses before the cache is read")
}

func TestListener_StartAndStop(t *testing.T) {
	bad, err := New(siteconfig.ACMEConfig{Address: "no-port-here"}, nil, nil, nil, http.NotFoundHandler())
	require.NoError(t, err)
	require.ErrorContains(t, bad.Start(), "the TLS listener stopped")

	l, err := New(siteconfig.ACMEConfig{Address: "127.0.0.1:0"}, nil, nil, nil, http.NotFoundHandler())
	require.NoError(t, err)
	require.NoError(t, l.Stop(context.Background()))
	require.NoError(t, l.Start(), "a listener stopped is a clean end, not a failure")
}
