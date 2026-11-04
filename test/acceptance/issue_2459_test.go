//go:build integration

package acceptance

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Acceptance for #2459: one message the mail client cannot read no longer
// stops a mailbox source.
//
// The upstream is the dev stack's Dovecot. It writes every structure
// correctly, so the server of the ticket (one that writes NIL where
// BODYSTRUCTURE requires a list) is Dovecot behind a TLS proxy this test runs:
// the proxy passes Dovecot's own answers through and rewrites only the tokens
// the ticket names, and the platform reaches the mailbox through it.
//
// Wire forms: the admin API takes one JSON object per route (config.imap's
// folders and allow_senders are arrays of strings, the only form their schema
// admits); trino_query is called with string connection, sql and purpose.

// proxy2459 relays TLS connections to Dovecot, passing each line Dovecot
// sends through rewrite.
type proxy2459 struct {
	addr string
	ca   string
}

func startProxy2459(t *testing.T, rewrite func([]byte) []byte) *proxy2459 {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(issue2247CA(t)))
	host, port := issue2247Addr()
	upstream := net.JoinHostPort(host, strconv.Itoa(port))
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go relay2459(c, upstream, &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12}, rewrite)
		}
	}()
	return &proxy2459{addr: ln.Addr().String(), ca: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

func relay2459(c net.Conn, upstream string, cfg *tls.Config, rewrite func([]byte) []byte) {
	defer func() { _ = c.Close() }()
	d, err := tls.Dial("tcp", upstream, cfg)
	if err != nil {
		return
	}
	defer func() { _ = d.Close() }()
	go func() { _, _ = io.Copy(d, c) }()
	r := bufio.NewReader(d)
	for {
		line, err := r.ReadBytes('\n')
		if _, werr := c.Write(rewrite(line)); werr != nil || err != nil {
			return
		}
	}
}

// mail2459 is a message from the source's allowed, authenticated sender.
func mail2459(run, subject string, multipart bool) string {
	head := []string{
		"Authentication-Results: " + issue2247AuthServ + "; dmarc=pass header.from=zoom.example",
		"From: Zoom <" + issue2247Sender + ">",
		"To: platform@acme.example",
		"Subject: " + subject + " " + run,
		"Date: Wed, 21 Oct 2026 16:00:00 +0000",
		"Message-ID: <" + strings.ReplaceAll(subject, " ", "-") + "-" + run + "@zoom.example>",
		"MIME-Version: 1.0",
	}
	if !multipart {
		return issue2247CRLF(append(head, "Content-Type: text/plain", "", "body of "+subject, "")...)
	}
	return issue2247CRLF(append(head,
		`Content-Type: multipart/mixed; boundary="b2459"`, "",
		"--b2459", "Content-Type: text/plain", "", "see the attached minutes",
		"--b2459", `Content-Type: text/plain; name="minutes.txt"`, `Content-Disposition: attachment; filename="minutes.txt"`, "",
		"minutes of the standup",
		"--b2459--", "")...)
}

func TestIssue2459_AnUnreadableMessageIsSkippedAndEveryOtherMessageAndFolderIsFiled(t *testing.T) {
	c := connect(t)
	run := strconv.FormatInt(time.Now().UnixNano()%1_000_000_000_000, 36)
	first, second := "Acc2459-A-"+run, "Acc2459-B-"+run
	mb := issue2247Mailbox(t)
	for _, f := range []string{first, second} {
		if err := mb.Create(f, nil).Wait(); err != nil {
			t.Fatalf("creating folder %s: %v", f, err)
		}
		t.Cleanup(func() { _ = mb.Delete(f).Wait() })
	}
	// The first folder: UID 1 is multipart, which the proxy describes with
	// a NIL part as the ticket's server did; UID 2 the proxy makes
	// unreadable however it is fetched; UID 3 is ordinary. The second
	// folder holds one ordinary message.
	issue2247Append(t, mb, first, mail2459(run, "Minutes", true))
	issue2247Append(t, mb, first, mail2459(run, "Poison", false))
	issue2247Append(t, mb, first, mail2459(run, "Ordinary", false))
	issue2247Append(t, mb, second, mail2459(run, "Other folder", false))

	// The counts are the control: a rewrite that never matched Dovecot's
	// answers would leave every message readable and prove nothing.
	var nilParts, brokenSizes atomic.Int64
	p := startProxy2459(t, func(line []byte) []byte {
		if bytes.Contains(line, []byte("BODYSTRUCTURE ((")) {
			nilParts.Add(1)
			line = bytes.ReplaceAll(line, []byte("BODYSTRUCTURE (("), []byte("BODYSTRUCTURE (NIL ("))
		}
		if bytes.Contains(line, []byte("UID 2 ")) && bytes.Contains(line, []byte("FETCH")) && bytes.Contains(line, []byte("RFC822.SIZE ")) {
			brokenSizes.Add(1)
			line = bytes.ReplaceAll(line, []byte("RFC822.SIZE "), []byte("RFC822.SIZE x"))
		}
		return line
	})
	host, portText, _ := net.SplitHostPort(p.addr)
	port, _ := strconv.Atoi(portText)
	name := "acc2459-" + run
	body := issue2247Source(t, name, first, issue2247Password)
	imapCfg, _ := body["config"].(map[string]any)["imap"].(map[string]any)
	imapCfg["host"], imapCfg["port"], imapCfg["tls_ca_bundle_pem"] = host, port, p.ca
	imapCfg["folders"] = []string{first, second}
	issue1870Create(t, c, body)

	var subjects map[string]map[string]any
	waitFor(t, "the readable messages of both folders filed", func() (bool, string) {
		subjects = map[string]map[string]any{}
		for _, row := range issue2247Rows(t, c, name) {
			pl := issue2247Payload(t, row)
			ext, _ := pl["external"].(map[string]any)
			s, _ := ext["subject"].(string)
			subjects[strings.TrimSuffix(s, " "+run)] = pl
		}
		return len(subjects) >= 3, fmt.Sprintf("%d rows: %v", len(subjects), keys2459(subjects))
	})
	for _, want := range []string{"Minutes", "Ordinary", "Other folder"} {
		if subjects[want] == nil {
			t.Errorf("%q was not filed; filed %v", want, keys2459(subjects))
		}
	}
	if subjects["Poison"] != nil {
		t.Errorf("the unreadable message was filed")
	}
	if nilParts.Load() == 0 || brokenSizes.Load() == 0 {
		t.Fatalf("the proxy rewrote %d NIL parts and %d sizes; both must fire for the criterion to mean anything", nilParts.Load(), brokenSizes.Load())
	}
	t.Logf("the proxy wrote NIL into %d structures and broke %d answers about UID 2", nilParts.Load(), brokenSizes.Load())
	if atts, _ := subjects["Minutes"]["attachments"].([]any); len(atts) != 1 {
		t.Errorf("the message whose structure was unreadable lost its attachment: %v", subjects["Minutes"]["attachments"])
	}

	// Each folder's place moved past what it read, with no error.
	status, src := c.restText("/api/v1/admin/webhooks/sources/" + name)
	if status != http.StatusOK {
		t.Fatalf("reading the source: %d %s", status, src)
	}
	var view struct {
		IMAP []struct {
			Folder    string `json:"folder"`
			LastUID   int    `json:"last_uid"`
			LastError string `json:"last_error"`
		} `json:"imap"`
	}
	_ = json.Unmarshal([]byte(src), &view)
	marks := map[string]int{}
	for _, f := range view.IMAP {
		marks[f.Folder] = f.LastUID
		if f.LastError != "" {
			t.Errorf("folder %s reports an error: %s", f.Folder, f.LastError)
		}
	}
	if marks[first] != 3 || marks[second] != 1 {
		t.Errorf("folder marks = %v, want %s past the skipped message at 3 and %s at 1", marks, first, second)
	}

	// The skipped message is in the source's rejections once, named by its
	// folder and UID, with nothing of the message.
	st := issue1979Until(t, c, 2*time.Minute, "the unreadable message's rejection", func(st issue1979Status) bool {
		for _, r := range st.Rejections {
			if r.Source == name && r.Outcome == "unreadable" {
				return true
			}
		}
		return false
	})
	for _, r := range st.Rejections {
		if r.Source != name || r.Outcome != "unreadable" {
			continue
		}
		if !strings.Contains(r.Reason, "folder "+first+", message uid 2") {
			t.Errorf("the rejection does not name the folder and UID: %q", r.Reason)
		}
		if strings.Contains(r.Reason, "Poison") || strings.Contains(r.Reason, "zoom.example") {
			t.Errorf("the rejection holds the message's content: %q", r.Reason)
		}
	}
}

func keys2459(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
