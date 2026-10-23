// Package imapmail reads a dedicated platform mailbox over IMAP for the imap
// connection kind (#2247): a TLS session, folders opened read-only, searches,
// header and whole-message fetches, and the reading of a message's MIME parts
// into plain text the platform can show. Which messages may be shown is
// internal/mailtrust's.
//
// It only reads. Folders are opened with EXAMINE and bodies fetched with
// BODY.PEEK, so the server never marks a message seen, and there is no STORE,
// COPY, MOVE, EXPUNGE, APPEND or send to call.
package imapmail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// The two ways a connection reaches the server. Plain IMAP is not one of
// them: the account's password would cross the network in the clear.
const (
	// TLSImplicit is IMAPS: TLS from the first byte, port 993.
	TLSImplicit = "tls"
	// TLSStartTLS upgrades a port-143 connection before anything is sent.
	TLSStartTLS = "starttls"
)

// Account is how one mailbox is reached.
type Account struct {
	Host     string
	Port     int
	TLS      string
	Username string
	Password string
	// TLSConfig carries the CA bundle and server name; nil uses the system
	// roots and Host.
	TLSConfig *tls.Config
	// DialTimeout bounds connecting and logging in.
	DialTimeout time.Duration
}

// Address is host:port.
func (a Account) Address() string { return net.JoinHostPort(a.Host, strconv.Itoa(a.Port)) }

// Session is one logged-in connection to the account. It is not safe for
// concurrent use: a tool call opens one, uses it, and closes it.
type Session struct {
	c *imapclient.Client
	// examined is the folder currently open read-only, and its UIDVALIDITY.
	examined    string
	uidValidity uint32
}

// Open connects, negotiates TLS, and logs in. ctx bounds the whole session:
// when it ends, the connection is closed and any command waiting fails.
func Open(ctx context.Context, a Account) (*Session, error) {
	if a.TLS != TLSImplicit && a.TLS != TLSStartTLS {
		return nil, fmt.Errorf("imapmail: tls must be %q or %q", TLSImplicit, TLSStartTLS)
	}
	tlsCfg := tlsConfig(a)
	d := net.Dialer{Timeout: a.DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", a.Address())
	if err != nil {
		return nil, fmt.Errorf("imapmail: connecting to %s: %w", a.Address(), err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := handshake(ctx, conn, a, tlsCfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	if err := c.Login(a.Username, a.Password).Wait(); err != nil {
		stop()
		_ = c.Close()
		return nil, fmt.Errorf("imapmail: logging in as %s: %w", a.Username, err)
	}
	return &Session{c: c}, nil
}

// tlsConfig is the account's TLS settings, defaulting the server name to the
// host and the version floor to TLS 1.2.
func tlsConfig(a Account) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if a.TLSConfig != nil {
		cfg = a.TLSConfig.Clone()
	}
	if cfg.ServerName == "" {
		cfg.ServerName = a.Host
	}
	return cfg
}

// handshake starts the IMAP client over conn: TLS first for IMAPS, STARTTLS
// before anything else is sent otherwise.
func handshake(ctx context.Context, conn net.Conn, a Account, cfg *tls.Config) (*imapclient.Client, error) {
	opts := &imapclient.Options{TLSConfig: cfg}
	if a.TLS == TLSStartTLS {
		c, err := imapclient.NewStartTLS(conn, opts)
		if err != nil {
			return nil, fmt.Errorf("imapmail: STARTTLS with %s: %w", a.Address(), err)
		}
		return c, nil
	}
	tc := tls.Client(conn, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("imapmail: TLS with %s: %w", a.Address(), err)
	}
	return imapclient.New(tc, opts), nil
}

// Close logs out and closes the connection.
func (s *Session) Close() error {
	_ = s.c.Logout().Wait()
	return s.c.Close() //nolint:wrapcheck // closing; nothing to add
}

// Examine opens folder read-only (EXAMINE), so nothing the session does can
// change it, and returns its UIDVALIDITY and message count.
func (s *Session) Examine(folder string) (uidValidity, messages uint32, err error) {
	data, err := s.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return 0, 0, fmt.Errorf("imapmail: opening %s: %w", folder, err)
	}
	s.examined, s.uidValidity = folder, data.UIDValidity
	return data.UIDValidity, data.NumMessages, nil
}

// Criteria is what a search narrows by.
type Criteria struct {
	// SinceUID returns only messages with a UID above it: a folder's
	// watermark.
	SinceUID uint32
}

func (c Criteria) imap() *imap.SearchCriteria {
	sc := &imap.SearchCriteria{}
	if c.SinceUID > 0 {
		sc.UID = []imap.UIDSet{{imap.UIDRange{Start: imap.UID(c.SinceUID + 1), Stop: 0}}}
	}
	return sc
}

// Search returns the UIDs in the examined folder that match, ascending.
func (s *Session) Search(c Criteria) ([]uint32, error) {
	data, err := s.c.UIDSearch(c.imap(), nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imapmail: searching %s: %w", s.examined, err)
	}
	uids := data.AllUIDs()
	out := make([]uint32, 0, len(uids))
	for _, u := range uids {
		if c.SinceUID == 0 || uint32(u) > c.SinceUID {
			out = append(out, uint32(u))
		}
	}
	slices.Sort(out)
	return out, nil
}

// Envelope is what a header fetch returns for one message.
type Envelope struct {
	UID  uint32
	Size int64
	// Arrived is when the server received the message (INTERNALDATE).
	Arrived     time.Time
	Header      []byte
	Attachments []Attachment
}

// headerSection is BODY.PEEK[HEADER]: the header block, without setting \Seen.
var headerSection = &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, Peek: true} //nolint:gochecknoglobals // fixed fetch item

// Headers fetches the header block, size and attachment list of each UID.
func (s *Session) Headers(uids []uint32) ([]Envelope, error) {
	if len(uids) == 0 {
		return []Envelope{}, nil
	}
	set := uidSet(uids)
	msgs, err := s.c.Fetch(set, &imap.FetchOptions{
		UID:           true,
		RFC822Size:    true,
		InternalDate:  true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		BodySection:   []*imap.FetchItemBodySection{headerSection},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("imapmail: fetching headers from %s: %w", s.examined, err)
	}
	out := make([]Envelope, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, Envelope{
			UID:         uint32(m.UID),
			Size:        m.RFC822Size,
			Arrived:     m.InternalDate,
			Header:      m.FindBodySection(headerSection),
			Attachments: attachmentsOf(m.BodyStructure),
		})
	}
	slices.SortFunc(out, func(a, b Envelope) int { return int(a.UID) - int(b.UID) })
	return out, nil
}

// wholeSection is BODY.PEEK[]: the whole message, without setting \Seen.
var wholeSection = &imap.FetchItemBodySection{Peek: true} //nolint:gochecknoglobals // fixed fetch item

// ErrNoSuchMessage is the refusal of a UID the folder does not hold.
var ErrNoSuchMessage = errors.New("imapmail: no message with that uid in the folder")

// Whole fetches one message, refusing it when it is larger than limit bytes.
func (s *Session) Whole(uid uint32, limit int64) ([]byte, error) {
	set := imap.UIDSetNum(imap.UID(uid))
	sized, err := s.c.Fetch(set, &imap.FetchOptions{UID: true, RFC822Size: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("imapmail: reading message %d: %w", uid, err)
	}
	if len(sized) == 0 {
		return nil, ErrNoSuchMessage
	}
	if limit > 0 && sized[0].RFC822Size > limit {
		return nil, fmt.Errorf("imapmail: message %d is %d bytes, over the connection's max_message_bytes (%d)", uid, sized[0].RFC822Size, limit)
	}
	msgs, err := s.c.Fetch(set, &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{wholeSection}}).Collect()
	if err != nil {
		return nil, fmt.Errorf("imapmail: reading message %d: %w", uid, err)
	}
	if len(msgs) == 0 {
		return nil, ErrNoSuchMessage
	}
	return msgs[0].FindBodySection(wholeSection), nil
}

func uidSet(uids []uint32) imap.UIDSet {
	u := make([]imap.UID, len(uids))
	for i, v := range uids {
		u[i] = imap.UID(v)
	}
	return imap.UIDSetNum(u...)
}

// attachmentsOf lists the parts of a body structure that are files.
func attachmentsOf(bs imap.BodyStructure) []Attachment {
	out := []Attachment{}
	if bs == nil {
		return out
	}
	bs.Walk(func(_ []int, p imap.BodyStructure) bool {
		sp, ok := p.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		name := sp.Filename()
		disp := sp.Disposition()
		if name == "" && (disp == nil || !strings.EqualFold(disp.Value, "attachment")) {
			return true
		}
		out = append(out, Attachment{
			Index:       len(out),
			Name:        safeFilename(name, sp.MediaType()),
			ContentType: sp.MediaType(),
			Size:        int64(sp.Size),
		})
		return true
	})
	return out
}
