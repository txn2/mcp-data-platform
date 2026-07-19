package notification

import (
	"bufio"
	"context"
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
// raw DATA payload.
func startFakeSMTPServer(t *testing.T) (port int, data <-chan string) {
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
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				w("250-test")
				w("250 8BITMIME")
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
	port, dataCh := startFakeSMTPServer(t)
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
