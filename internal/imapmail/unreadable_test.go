package imapmail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var multipartMail = crlf(
	"From: ap@vendor.example",
	"Subject: Invoice",
	`Content-Type: multipart/mixed; boundary="b"`,
	"",
	"--b", "Content-Type: text/plain", "", "see attached",
	"--b", `Content-Type: text/csv; name="lines.csv"`, `Content-Disposition: attachment; filename="lines.csv"`, "",
	"sku,qty", "--b--",
)

// startRewritingServer serves messages in folder Platform behind a TLS proxy
// that passes each line the server sends through rewrite.
func startRewritingServer(t *testing.T, rewrite func([]byte) []byte, messages ...[]byte) *testServer {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("platform", "app-password")
	require.NoError(t, user.Create("Platform", nil))
	require.NoError(t, user.Create("Other", nil))
	for _, m := range messages {
		_, err := user.Append("Platform", literal(m), &imap.AppendOptions{})
		require.NoError(t, err)
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps: imap.CapSet{imap.CapIMAP4rev1: {}}, InsecureAuth: true,
	})
	backend, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(backend) }()
	t.Cleanup(func() { _ = srv.Close() })
	cert, pool := selfSigned(t)
	front, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	require.NoError(t, err)
	t.Cleanup(func() { _ = front.Close() })
	go func() {
		for {
			c, err := front.Accept()
			if err != nil {
				return
			}
			go relay(c, backend.Addr().String(), rewrite)
		}
	}()
	return &testServer{addr: front.Addr().String(), pool: pool, user: user}
}

func relay(c net.Conn, backend string, rewrite func([]byte) []byte) {
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
		if _, werr := c.Write(rewrite(line)); werr != nil || err != nil {
			return
		}
	}
}

// nilPart writes NIL where a multipart structure requires a part, as the
// server in #2459 did.
func nilPart(line []byte) []byte {
	return bytes.ReplaceAll(line, []byte("BODYSTRUCTURE (("), []byte("BODYSTRUCTURE (NIL ("))
}

func openTest(t *testing.T, srv *testServer) *Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	s, err := Open(ctx, srv.account("app-password"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestHeaders_ABodyStructureTheClientCannotParseIsFetchedWithoutIt(t *testing.T) {
	srv := startRewritingServer(t, nilPart, replyMail, multipartMail, replyMail)
	s := openTest(t, srv)
	_, _, err := s.Examine("Platform")
	require.NoError(t, err)

	// The control: the batch fetch fails as #2459's did, and the client
	// closes the connection over it.
	_, err = s.fetchHeaders([]uint32{1, 2, 3}, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected '('")
	assert.Eventually(t, s.closed, time.Second, 10*time.Millisecond, "go-imap closes the connection it could not read")

	envs, bad, err := s.Headers([]uint32{1, 2, 3})
	require.NoError(t, err)
	assert.Empty(t, bad, "fetched without its structure, the message is read")
	require.Len(t, envs, 3)
	assert.Equal(t, []uint32{1, 2, 3}, []uint32{envs[0].UID, envs[1].UID, envs[2].UID})
	assert.Contains(t, string(envs[1].Header), "Subject: Invoice")
	assert.Empty(t, envs[1].Attachments, "without a structure the attachments come from the raw message")
}

func TestHeaders_AMessageUnreadableBothWaysIsReturnedToSkip(t *testing.T) {
	breakUID2 := func(line []byte) []byte {
		if bytes.Contains(line, []byte("UID 2")) {
			return bytes.ReplaceAll(line, []byte("RFC822.SIZE "), []byte("RFC822.SIZE x"))
		}
		return line
	}
	srv := startRewritingServer(t, breakUID2, replyMail, replyMail, replyMail)
	s := openTest(t, srv)
	_, _, err := s.Examine("Platform")
	require.NoError(t, err)

	envs, bad, err := s.Headers([]uint32{1, 2, 3})
	require.NoError(t, err)
	require.Len(t, bad, 1)
	assert.EqualValues(t, 2, bad[0].UID)
	assert.NotContains(t, bad[0].Reason, "Paid today", "the reason names protocol tokens, never the message")
	require.Len(t, envs, 2)
	assert.EqualValues(t, 1, envs[0].UID)
	assert.EqualValues(t, 3, envs[1].UID)
}

func TestExamine_ReopensAConnectionAnEarlierFailureClosed(t *testing.T) {
	srv := startServer(t, replyMail)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := Open(ctx, srv.account("app-password"))
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	_, _, err = s.Examine("Platform")
	require.NoError(t, err)

	_ = s.c.Close() // what go-imap does after a response it cannot read
	require.Eventually(t, s.closed, time.Second, 10*time.Millisecond)

	_, n, err := s.Examine("Platform")
	require.NoError(t, err, "the next folder is opened on a new connection")
	assert.EqualValues(t, 1, n)
	uids, err := s.Search(Criteria{})
	require.NoError(t, err)
	assert.Equal(t, []uint32{1}, uids)
}

func TestHeaders_ATimeOutIsTheCallsError(t *testing.T) {
	srv := startRewritingServer(t, nilPart, multipartMail)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	s, err := Open(ctx, srv.account("app-password"))
	require.NoError(t, err)
	_, _, err = s.Examine("Platform")
	require.NoError(t, err)
	cancel()
	_, _, err = s.Headers([]uint32{1})
	require.Error(t, err, "a session out of time skips nothing")
}

func TestReasonOf_Bounded(t *testing.T) {
	long := bytes.Repeat([]byte("x"), 1000)
	assert.LessOrEqual(t, len([]rune(reasonOf(io.ErrUnexpectedEOF))), maxReasonRunes+1)
	assert.Len(t, []rune(reasonOf(assert.AnError)), len([]rune(assert.AnError.Error())))
	assert.Len(t, []rune(reasonOf(&unreadableError{err: stringError(long)})), maxReasonRunes+1)
}

type stringError []byte

func (e stringError) Error() string { return string(e) }

func TestReconnectPaths(t *testing.T) {
	if err := (&Session{}).reopen(); err == nil {
		t.Error("a session not opened by Open cannot reconnect")
	}
	srv := startServer(t, replyMail)
	s := openTest(t, srv)
	status := &imap.Error{Type: imap.StatusResponseTypeNo, Text: "no such message"}
	require.NoError(t, s.recover(status), "a server's NO leaves the connection as it is")
	require.Error(t, s.fatal(status), "a server's NO is the call's error, not a message's")

	_, _, err := s.Examine("Platform")
	require.NoError(t, err)
	_ = s.c.Close()
	require.Eventually(t, s.closed, time.Second, 10*time.Millisecond)
	require.NoError(t, s.recover(io.ErrUnexpectedEOF), "a closed connection is opened again, in the folder that was open")
	uids, err := s.Search(Criteria{})
	require.NoError(t, err)
	assert.Equal(t, []uint32{1}, uids)

	s.examined, s.uidValidity = "Platform", 987654321
	_ = s.c.Close()
	require.Eventually(t, s.closed, time.Second, 10*time.Millisecond)
	require.ErrorContains(t, s.reopen(), "renumbered", "a folder whose UIDVALIDITY changed is refused")
	s.examined = "Nope"
	require.Error(t, s.reopen(), "a folder that is gone is refused")
}
