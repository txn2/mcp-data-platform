package whimap

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// rewrite changes one line the server sends before the client reads it.
type rewrite func(line []byte) []byte

// nilBodyPart is the answer of #2459: a server that writes NIL where a
// multipart body structure requires a parenthesized part, which go-imap
// refuses ("in body-type-mpart: in body-type-1part: imapwire: expected '(',
// got \"N\"") and closes the connection over.
func nilBodyPart(line []byte) []byte {
	return bytes.ReplaceAll(line, []byte("BODYSTRUCTURE (("), []byte("BODYSTRUCTURE (NIL ("))
}

// unreadableUID2 breaks every answer about UID 2, with or without its
// structure.
func unreadableUID2(line []byte) []byte {
	if !bytes.Contains(line, []byte("UID 2")) {
		return line
	}
	return bytes.ReplaceAll(line, []byte("RFC822.SIZE "), []byte("RFC822.SIZE x"))
}

// startProxiedServer serves folders from go-imap's in-memory backend in
// plain text behind a TLS proxy that passes every server line through each
// rewrite, so a test chooses exactly what the client reads.
func startProxiedServer(t *testing.T, folders map[string][][]byte, rewrites ...rewrite) *server {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("platform", "app-pw")
	for name, msgs := range folders {
		require.NoError(t, user.Create(name, nil))
		for _, m := range msgs {
			r := bytes.NewReader(m)
			_, err := user.Append(name, literal{r, r.Size()}, &imap.AppendOptions{})
			require.NoError(t, err)
		}
	}
	mem.AddUser(user)
	is := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps: imap.CapSet{imap.CapIMAP4rev1: {}}, InsecureAuth: true,
	})
	backend, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = is.Serve(backend) }()
	t.Cleanup(func() { _ = is.Close() })

	cert, ca := testCert(t)
	front, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	require.NoError(t, err)
	t.Cleanup(func() { _ = front.Close() })
	go proxy(front, backend.Addr().String(), rewrites)

	h, p, _ := net.SplitHostPort(front.Addr().String())
	srv := &server{host: h, ca: ca, user: user}
	srv.port, _ = strconv.Atoi(p)
	return srv
}

// proxy relays each accepted connection to backend, rewriting the server's
// lines.
func proxy(front net.Listener, backend string, rewrites []rewrite) {
	for {
		c, err := front.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = c.Close() }()
			b, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", backend)
			if err != nil {
				return
			}
			defer func() { _ = b.Close() }()
			go func() { _, _ = io.Copy(b, c) }()
			r := bufio.NewReader(b)
			for {
				line, err := r.ReadBytes('\n')
				for _, rw := range rewrites {
					line = rw(line)
				}
				if _, werr := c.Write(line); werr != nil || err != nil {
					return
				}
			}
		}()
	}
}

func testCert(t *testing.T) (cert tls.Certificate, caPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// trustedNote is a plain message from an allowed, authenticated sender.
func trustedNote(subject string) []byte {
	return mail(
		"Authentication-Results: "+mx+"; dmarc=pass header.from=vendor.example",
		"From: ap@vendor.example", "Subject: "+subject, "Message-ID: <"+subject+"@vendor.example>",
		"Content-Type: text/plain", "", "body of "+subject,
	)
}

// rejections records what a poll skipped.
type rejections struct {
	mu   sync.Mutex
	rows []whstore.Rejection
	outs []string
}

func (r *rejections) RecordRejections(_ context.Context, rows []whstore.Rejection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, rows...)
	return nil
}

func (r *rejections) WebhookRequest(_ context.Context, _, outcome string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outs = append(r.outs, outcome)
}

// A message the client cannot read is skipped and recorded once, the messages
// beside it are filed, and the folders after it are polled (#2459).
func TestPoll_AnUnreadableMessageIsSkippedAndTheOtherFoldersPolled(t *testing.T) {
	srv := startProxiedServer(t, map[string][][]byte{
		// UID 1 is the invoice, whose multipart structure the server
		// describes with a NIL part; UID 2 cannot be read at all; UID 3 is
		// ordinary.
		"Broken":   {invoice, trustedNote("two"), trustedNote("three")},
		"Platform": {trustedNote("other-folder")},
	}, nilBodyPart, unreadableUID2)
	src := srv.source("app-pw")
	src.Config.IMAP.Folders = []string{"Broken", "Platform"}
	f := newFakes(src)
	rec := &rejections{}
	p := New(Config{
		Sources: f, Objects: f, Bucket: "resources", Recorder: f, RawWindows: f, Marks: f, Files: f,
		Rejections: rec, Metrics: rec, Lock: f.lock, Replica: "r1",
	})

	require.NoError(t, p.Poll(context.Background(), src), "one unreadable message does not fail the poll")

	subjects := map[string]bool{}
	for _, ev := range f.events(t) {
		var pl Payload
		require.NoError(t, json.Unmarshal(ev.Payload, &pl))
		subjects[pl.External.Subject] = true
		if pl.External.Subject == "Invoice 4411" {
			require.Len(t, pl.Attachments, 1, "the attachment is read from the raw message when the structure is not")
			assert.Equal(t, "lines.csv", pl.Attachments[0].Name)
		}
	}
	assert.Equal(t, map[string]bool{"Invoice 4411": true, "three": true, "other-folder": true}, subjects,
		"the readable messages of the folder and the next folder are filed")

	broken := f.marks["vendor-mail/Broken"]
	assert.EqualValues(t, 3, broken.LastUID, "the watermark moves past the skipped message")
	assert.Empty(t, broken.LastError)
	assert.EqualValues(t, 1, f.marks["vendor-mail/Platform"].LastUID, "the next folder is polled on a working connection")
	assert.Empty(t, f.marks["vendor-mail/Platform"].LastError)

	require.Len(t, rec.rows, 1, "the skipped message is recorded once")
	row := rec.rows[0]
	assert.Equal(t, OutcomeUnreadable, row.Outcome)
	assert.Equal(t, "vendor-mail", row.Source)
	assert.Contains(t, row.Reason, "folder Broken, message uid 2")
	assert.NotContains(t, row.Reason, "body of two", "nothing of the message is kept")
	assert.NotContains(t, row.Reason, "vendor.example", "nothing of the message is kept")
	assert.Equal(t, []string{OutcomeUnreadable}, rec.outs)

	// The next poll starts past it: nothing is skipped or recorded again.
	require.NoError(t, p.Poll(context.Background(), src))
	assert.Len(t, rec.rows, 1)
}
