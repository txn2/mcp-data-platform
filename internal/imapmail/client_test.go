package imapmail

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testServer is an IMAPS server holding one account with a "Platform" folder,
// served by go-imap's in-memory backend over TLS with a certificate made for
// the test. It speaks the protocol; the client under test cannot tell it from
// a mail server.
type testServer struct {
	addr string
	pool *x509.CertPool
	user *imapmemserver.User
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func startServer(t *testing.T, messages ...[]byte) *testServer {
	t.Helper()
	cert, pool := selfSigned(t)
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("platform", "app-password")
	require.NoError(t, user.Create("Platform", nil))
	for _, m := range messages {
		_, err := user.Append("Platform", literal(m), &imap.AppendOptions{})
		require.NoError(t, err)
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
		InsecureAuth: true,
	})
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &testServer{addr: ln.Addr().String(), pool: pool, user: user}
}

type literalReader struct {
	*bytes.Reader
	n int64
}

func (l literalReader) Size() int64 { return l.n }

func literal(b []byte) imap.LiteralReader {
	r := bytes.NewReader(b)
	return literalReader{r, r.Size()}
}

func (s *testServer) account(password string) Account {
	host, port, _ := net.SplitHostPort(s.addr)
	p := 0
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	return Account{
		Host: host, Port: p, TLS: TLSImplicit, Username: "platform", Password: password,
		TLSConfig:   &tls.Config{RootCAs: s.pool, MinVersion: tls.VersionTLS12},
		DialTimeout: 5 * time.Second,
	}
}

var replyMail = crlf(
	"From: Dana Ortiz <dana@vendor.example>",
	"To: platform@company.example",
	"Subject: Re: Invoice 4411",
	"Date: Wed, 21 Oct 2026 10:00:00 +0000",
	"Message-ID: <reply-1@vendor.example>",
	"In-Reply-To: <inv-4411@vendor.example>",
	"References: <inv-4411@vendor.example>",
	"Content-Type: text/plain",
	"",
	"Paid today.",
)

func TestSession_ReadsWithoutChangingTheMailbox(t *testing.T) {
	srv := startServer(t, invoiceMail, replyMail)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := Open(ctx, srv.account("app-password"))
	require.NoError(t, err)
	defer s.Close() //nolint:errcheck // test

	validity, count, err := s.Examine("Platform")
	require.NoError(t, err)
	assert.NotZero(t, validity)
	assert.Equal(t, uint32(2), count)

	all, err := s.Search(Criteria{})
	require.NoError(t, err)
	require.Len(t, all, 2)

	bySubject, err := s.Search(Criteria{Subject: "Re:"})
	require.NoError(t, err)
	assert.Equal(t, all[1:], bySubject)

	after, err := s.Search(Criteria{SinceUID: all[0]})
	require.NoError(t, err)
	assert.Equal(t, all[1:], after, "a watermark returns only newer mail")

	thread, err := s.Search(Criteria{HeaderIDs: []string{"inv-4411@vendor.example"}})
	require.NoError(t, err)
	assert.Equal(t, all, thread, "the original and the reply that names it")

	envs, err := s.Headers(all)
	require.NoError(t, err)
	require.Len(t, envs, 2)
	h, err := ReadHeaders(envs[0].Header)
	require.NoError(t, err)
	assert.Equal(t, "inv-4411@vendor.example", h.MessageID)
	require.Len(t, envs[0].Attachments, 1)
	assert.Equal(t, "application/pdf", envs[0].Attachments[0].ContentType)
	assert.Positive(t, envs[0].Size)

	raw, err := s.Whole(all[0], 0)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "Please find invoice 4411 attached.")

	_, err = s.Whole(all[0], 10)
	assert.ErrorContains(t, err, "over the connection's max_message_bytes")
	_, err = s.Whole(9999, 0)
	assert.ErrorIs(t, err, ErrNoSuchMessage)

	unseen, err := s.Search(Criteria{Unseen: true})
	require.NoError(t, err)
	assert.Equal(t, all, unseen, "reading a message never marks it seen")

	none, err := s.Headers(nil)
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestOpen_Refusals(t *testing.T) {
	srv := startServer(t)
	ctx := context.Background()

	_, err := Open(ctx, srv.account("wrong"))
	assert.ErrorContains(t, err, "logging in as platform")

	plain := srv.account("app-password")
	plain.TLS = "none"
	_, err = Open(ctx, plain)
	assert.ErrorContains(t, err, "tls must be")

	untrusted := srv.account("app-password")
	untrusted.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	_, err = Open(ctx, untrusted)
	assert.ErrorContains(t, err, "TLS with", "a certificate the platform does not trust is refused")

	s, err := Open(ctx, srv.account("app-password"))
	require.NoError(t, err)
	_, _, err = s.Examine("Nope")
	assert.ErrorContains(t, err, "opening Nope")
	require.NoError(t, s.Close())

	closed := srv.account("app-password")
	closed.Port = 1
	_, err = Open(ctx, closed)
	assert.ErrorContains(t, err, "connecting to")
}

func TestCriteria_Imap(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	sc := Criteria{From: "vendor", To: "platform", Cc: "dana", Text: "invoice", Since: since, Unseen: true}.imap()
	assert.Len(t, sc.Header, 3)
	assert.Equal(t, []string{"invoice"}, sc.Text)
	assert.Equal(t, []imap.Flag{imap.FlagSeen}, sc.NotFlag)
	assert.Equal(t, since, sc.Since)
	assert.Nil(t, idCriteria(nil))
}
