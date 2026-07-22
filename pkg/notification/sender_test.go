package notification

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"
)

func TestBuildMessage(t *testing.T) {
	settings := SMTPSettings{From: "p@example.com", FromName: "Data Platform"}
	email := Email{To: "a@b.io", Subject: "Hello", Text: "plain body", HTML: "<p>html body</p>"}

	msg, err := buildMessage(settings, email)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var out strings.Builder
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	raw := out.String()
	for _, want := range []string{
		"Subject: Hello", "To: <a@b.io>", "Data Platform",
		"plain body", "html body", "multipart/alternative",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("message missing %q", want)
		}
	}
}

// TestBuildMessage_EmbedsLogo proves the bytes actually reach the wire as an
// inline part under the Content-ID the HTML references. A rendered cid: URL
// with no matching part is a broken image in every client.
func TestBuildMessage_EmbedsLogo(t *testing.T) {
	logo := []byte("\x89PNG\r\n\x1a\nfake-raster-bytes")
	email := Email{
		To: "a@b.io", Subject: "Hello", Text: "plain",
		HTML:    `<img src="cid:` + logoContentID + `">`,
		LogoPNG: logo,
	}

	msg, err := buildMessage(SMTPSettings{From: "p@example.com"}, email)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var out strings.Builder
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	raw := out.String()
	for _, want := range []string{logoContentID, "multipart/related", base64.StdEncoding.EncodeToString(logo)} {
		if !strings.Contains(raw, want) {
			t.Errorf("message missing %q; got:\n%s", want, raw)
		}
	}
}

// TestBuildMessage_NoLogoNoRelatedPart pins the unconfigured default: no
// attachment machinery when no logo is set.
func TestBuildMessage_NoLogoNoRelatedPart(t *testing.T) {
	msg, err := buildMessage(SMTPSettings{From: "p@example.com"},
		Email{To: "a@b.io", Subject: "Hello", Text: "plain", HTML: "<p>x</p>"})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var out strings.Builder
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if strings.Contains(out.String(), logoContentID) {
		t.Error("no logo configured, yet the message carries a logo part")
	}
}

// TestBuildMessage_ListUnsubscribeHeaders proves the RFC 8058 headers reach
// the wire when the email carries the footer opt-out link. Gmail and Yahoo
// require them for bulk senders; the in-body link alone does not qualify.
func TestBuildMessage_ListUnsubscribeHeaders(t *testing.T) {
	unsub := "https://platform.example.com/portal/notifications/unsubscribe?tok=abc"
	msg, err := buildMessage(SMTPSettings{From: "p@example.com"},
		Email{To: "a@b.io", Subject: "Hello", Text: "plain", HTML: "<p>x</p>", UnsubURL: unsub})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var out strings.Builder
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	// Unfold RFC 5322 continuation lines: go-mail folds the long URL header.
	raw := strings.NewReplacer("\r\n ", " ", "\n ", " ").Replace(out.String())
	for _, want := range []string{
		"List-Unsubscribe: <" + unsub + ">",
		"List-Unsubscribe-Post: List-Unsubscribe=One-Click",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("message missing %q; got:\n%s", want, raw)
		}
	}
}

// TestBuildMessage_NonHTTPSUnsubURLNoOneClick pins the RFC 8058 conformance
// fallback: a non-https unsubscribe URL advertises the plain List-Unsubscribe
// URI but not the one-click POST header, which the RFC restricts to https.
func TestBuildMessage_NonHTTPSUnsubURLNoOneClick(t *testing.T) {
	unsub := "http://internal.example.com/portal/notifications/unsubscribe?tok=abc"
	msg, err := buildMessage(SMTPSettings{From: "p@example.com"},
		Email{To: "a@b.io", Subject: "Hello", Text: "plain", HTML: "<p>x</p>", UnsubURL: unsub})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var out strings.Builder
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	raw := strings.NewReplacer("\r\n ", " ", "\n ", " ").Replace(out.String())
	if !strings.Contains(raw, "List-Unsubscribe: <"+unsub+">") {
		t.Errorf("message missing the plain List-Unsubscribe header:\n%s", raw)
	}
	if strings.Contains(raw, "List-Unsubscribe-Post") {
		t.Errorf("one-click header must not advertise a non-https target:\n%s", raw)
	}
}

// TestBuildMessage_NoUnsubURLNoHeaders pins the transactional default:
// recipient-requested mail (guest links, admin tests) carries no unsubscribe
// headers, mirroring its footer.
func TestBuildMessage_NoUnsubURLNoHeaders(t *testing.T) {
	msg, err := buildMessage(SMTPSettings{From: "p@example.com"},
		Email{To: "a@b.io", Subject: "Hello", Text: "plain", HTML: "<p>x</p>"})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var out strings.Builder
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if strings.Contains(out.String(), "List-Unsubscribe") {
		t.Errorf("no UnsubURL, yet the message carries unsubscribe headers:\n%s", out.String())
	}
}

// TestBuildMessage_MessageIDUsesFromDomain proves the Message-ID right-hand
// side is the From domain, not the local hostname a containerized deployment
// would otherwise leak.
func TestBuildMessage_MessageIDUsesFromDomain(t *testing.T) {
	msg, err := buildMessage(SMTPSettings{From: "p@example.com"},
		Email{To: "a@b.io", Subject: "Hello", Text: "plain", HTML: "<p>x</p>"})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	id := msg.GetMessageID()
	if id == "" {
		t.Fatal("no Message-ID set")
	}
	if !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("Message-ID domain must come from the From address: %q", id)
	}
	other, err := buildMessage(SMTPSettings{From: "p@example.com"}, Email{To: "a@b.io"})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if other.GetMessageID() == id {
		t.Error("Message-ID left-hand side must be random per message")
	}
}

func TestMessageIDDomain(t *testing.T) {
	tests := []struct {
		from, want string
	}{
		{"p@example.com", "example.com"},
		{"Data Platform <p@example.com>", "example.com"},
		{"no-at-sign", ""},
		{"trailing@", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := messageIDDomain(tc.from); got != tc.want {
			t.Errorf("messageIDDomain(%q) = %q, want %q", tc.from, got, tc.want)
		}
	}
}

func TestBuildMessage_NoFromName(t *testing.T) {
	msg, err := buildMessage(SMTPSettings{From: "p@example.com"}, Email{To: "a@b.io"})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var out strings.Builder
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "From: <p@example.com>") {
		t.Errorf("plain From missing:\n%s", out.String())
	}
}

func TestBuildMessage_InvalidAddresses(t *testing.T) {
	if _, err := buildMessage(SMTPSettings{From: "not-an-address"}, Email{To: "a@b.io"}); err == nil {
		t.Error("expected invalid from error")
	}
	if _, err := buildMessage(SMTPSettings{From: "not-an-address", FromName: "X"}, Email{To: "a@b.io"}); err == nil {
		t.Error("expected invalid from error (with name)")
	}
	if _, err := buildMessage(SMTPSettings{From: "p@example.com"}, Email{To: "bad recipient"}); err == nil {
		t.Error("expected invalid recipient error")
	}
}

func TestBuildClient(t *testing.T) {
	tests := []struct {
		name     string
		settings SMTPSettings
	}{
		{name: "starttls with auth", settings: SMTPSettings{
			Host: "smtp.example.com",
			Port: 587, TLSMode: TLSModeStartTLS, Username: "u", Password: "p",
		}},
		{name: "implicit tls", settings: SMTPSettings{
			Host: "smtp.example.com",
			Port: 465, TLSMode: TLSModeImplicit,
		}},
		{name: "plaintext relay", settings: SMTPSettings{
			Host: "relay.internal",
			Port: 25, TLSMode: TLSModeNone,
		}},
		{name: "default tls mode", settings: SMTPSettings{Host: "smtp.example.com", Port: 587}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, err := buildClient(tc.settings)
			if err != nil {
				t.Fatalf("buildClient: %v", err)
			}
			if client == nil {
				t.Fatal("nil client")
			}
		})
	}
}

func TestBuildClient_EmptyHost(t *testing.T) {
	if _, err := buildClient(SMTPSettings{Port: 587}); err == nil {
		t.Error("expected error for empty host")
	}
}

// startFakeSMTPServer runs a minimal single-connection SMTP conversation on
// a random localhost port and returns the port plus a channel delivering the
// raw DATA payload. When withAuth is true the server advertises CRAM-MD5
// (the only mechanism go-mail permits on an unencrypted test connection) and
// requires the client to complete the challenge before accepting mail, which
// exercises the AutoDiscover negotiation end to end.
func startFakeSMTPServer(t *testing.T, withAuth bool) (port int, data <-chan string) {
	t.Helper()
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	dataCh := make(chan string, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		w("220 test ready")
		scanner := bufio.NewScanner(conn)
		inData := false
		awaitingAuth := false
		var data strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case inData:
				if line == "." {
					inData = false
					dataCh <- data.String()
					w("250 OK")
					continue
				}
				data.WriteString(line + "\n")
			case awaitingAuth:
				// The CRAM-MD5 digest response; accept anything.
				awaitingAuth = false
				w("235 authenticated")
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				w("250-test")
				if withAuth {
					w("250-AUTH CRAM-MD5")
				}
				w("250 8BITMIME")
			case strings.HasPrefix(line, "AUTH CRAM-MD5"):
				awaitingAuth = true
				w("334 " + base64.StdEncoding.EncodeToString([]byte("<challenge@test>")))
			case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
				w("250 OK")
			case line == "DATA":
				inData = true
				w("354 go ahead")
			case line == "QUIT":
				w("221 bye")
				return
			default:
				w("250 OK")
			}
		}
	}()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address type %T", ln.Addr())
	}
	return addr.Port, dataCh
}

func TestSMTPSender_Send_EndToEnd(t *testing.T) {
	port, dataCh := startFakeSMTPServer(t, false)
	s := NewSMTPSender()

	err := s.Send(context.Background(), SMTPSettings{
		Host: "127.0.0.1", Port: port, From: "p@example.com", TLSMode: TLSModeNone,
	}, Email{To: "a@b.io", Subject: "Wire test", Text: "plain", HTML: "<p>html</p>"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case data := <-dataCh:
		if !strings.Contains(data, "Subject: Wire test") {
			t.Errorf("DATA missing subject:\n%s", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no DATA received")
	}
}

func TestSMTPSender_Send_AutoDiscoverAuth(t *testing.T) {
	// The server advertises only CRAM-MD5; AutoDiscover must pick it up,
	// complete the challenge, and deliver.
	port, dataCh := startFakeSMTPServer(t, true)
	s := NewSMTPSender()

	err := s.Send(context.Background(), SMTPSettings{
		Host: "127.0.0.1", Port: port, From: "p@example.com", TLSMode: TLSModeNone,
		Username: "mailer", Password: "secret",
	}, Email{To: "a@b.io", Subject: "Auth wire test", Text: "plain", HTML: "<p>html</p>"})
	if err != nil {
		t.Fatalf("Send with negotiated auth: %v", err)
	}

	select {
	case data := <-dataCh:
		if !strings.Contains(data, "Subject: Auth wire test") {
			t.Errorf("DATA missing subject:\n%s", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no DATA received")
	}
}

func TestSMTPSender_Send_BadMessage(t *testing.T) {
	s := NewSMTPSender()
	err := s.Send(context.Background(), SMTPSettings{From: "bad"}, Email{To: "a@b.io"})
	if err == nil {
		t.Fatal("expected message build error")
	}
}

func TestSMTPSender_Send_BadClient(t *testing.T) {
	s := NewSMTPSender()
	err := s.Send(context.Background(),
		SMTPSettings{From: "p@example.com", Port: 587}, Email{To: "a@b.io"})
	if err == nil {
		t.Fatal("expected client build error for empty host")
	}
}
