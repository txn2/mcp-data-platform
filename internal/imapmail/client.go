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
	// ctx and account are what the session was opened with, kept so a
	// connection the client closed can be opened again (#2459).
	ctx     context.Context //nolint:containedctx // the session's lifetime, which a reconnect is bounded by
	account Account
	stop    func() bool
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
	return &Session{c: c, ctx: ctx, account: a, stop: stop}, nil
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
	if s.stop != nil {
		s.stop()
	}
	_ = s.c.Logout().Wait()
	return s.c.Close() //nolint:wrapcheck // closing; nothing to add
}

// closed reports whether the client has closed the connection: go-imap closes
// it when it cannot read a response, so every later command on it fails with
// "use of closed network connection" (#2459).
func (s *Session) closed() bool {
	select {
	case <-s.c.Closed():
		return true
	default:
		return false
	}
}

// broken reports whether err left the connection unusable. A server's NO or
// BAD is an *imap.Error on a connection that still works; anything else (a
// response the client could not parse, a dropped connection) ends it.
func (s *Session) broken(err error) bool {
	var status *imap.Error
	return s.closed() || !errors.As(err, &status)
}

// reopen replaces a connection the client closed with a new one, logged in
// and in the folder that was open. A folder renumbered in between
// (UIDVALIDITY changed) is refused, since the UIDs the caller holds name other
// messages there now.
func (s *Session) reopen() error {
	if s.ctx == nil {
		return errors.New("imapmail: the connection closed and this session cannot reconnect")
	}
	if s.stop != nil {
		s.stop()
	}
	_ = s.c.Close()
	fresh, err := Open(s.ctx, s.account)
	if err != nil {
		return fmt.Errorf("imapmail: reconnecting: %w", err)
	}
	s.c, s.stop = fresh.c, fresh.stop
	if s.examined == "" {
		return nil
	}
	folder, validity := s.examined, s.uidValidity
	data, err := s.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return fmt.Errorf("imapmail: reopening %s: %w", folder, err)
	}
	if data.UIDValidity != validity {
		return fmt.Errorf("imapmail: %s was renumbered while reconnecting (UIDVALIDITY %d, was %d)", folder, data.UIDValidity, validity)
	}
	return nil
}

// recover reopens the connection when err ended it, and reports a reconnect
// that failed.
func (s *Session) recover(err error) error {
	if !s.broken(err) {
		return nil
	}
	return s.reopen()
}

// Examine opens folder read-only (EXAMINE), so nothing the session does can
// change it, and returns its UIDVALIDITY and message count. A connection an
// earlier folder's failure closed is opened again first, so one folder's
// failure is never another's (#2459).
func (s *Session) Examine(folder string) (uidValidity, messages uint32, err error) {
	if s.closed() {
		s.examined = ""
		if err := s.reopen(); err != nil {
			return 0, 0, err
		}
	}
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

// Unreadable is a message the client could not read the server's answer
// about, even fetched alone and without its structure (#2459). Reason is the
// client's account of the answer it could not parse, which names protocol
// tokens and never the message's content.
type Unreadable struct {
	UID    uint32
	Reason string
}

// maxReasonRunes bounds an Unreadable's reason.
const maxReasonRunes = 300

// Headers fetches the header block, size and attachment list of each UID.
//
// A server may describe a message in a form the client cannot parse (a NIL
// where the protocol requires a list, emersion/go-imap#678), and that one
// message then fails the whole batch and closes the connection. When the
// batch fails, the connection is reopened and each UID is fetched alone; one
// that still fails is fetched once more without BODYSTRUCTURE, its
// attachments then read from the raw message by whoever reads it. A message
// that fails both ways is returned as Unreadable for the caller to skip, so
// the messages beside it are read. A failure that is not the message's (the
// server's NO, a reconnect that fails, the session's time running out) is the
// call's error.
func (s *Session) Headers(uids []uint32) ([]Envelope, []Unreadable, error) {
	if len(uids) == 0 {
		return []Envelope{}, nil, nil
	}
	out, err := s.fetchHeaders(uids, true)
	if err == nil {
		return out, nil, nil
	}
	if err := s.fatal(err); err != nil {
		return nil, nil, err
	}
	var bad []Unreadable
	out = make([]Envelope, 0, len(uids))
	for _, uid := range uids {
		env, err := s.headerOf(uid)
		var unreadable *unreadableError
		switch {
		case errors.As(err, &unreadable):
			bad = append(bad, Unreadable{UID: uid, Reason: reasonOf(unreadable.err)})
		case err != nil:
			return nil, nil, err
		default:
			out = append(out, env...)
		}
	}
	return out, bad, nil
}

// headerOf fetches one UID: with its structure, then without it.
func (s *Session) headerOf(uid uint32) ([]Envelope, error) {
	envs, err := s.fetchHeaders([]uint32{uid}, true)
	if err == nil {
		return envs, nil
	}
	if err := s.fatal(err); err != nil {
		return nil, err
	}
	envs, err = s.fetchHeaders([]uint32{uid}, false)
	if err == nil {
		return envs, nil
	}
	if err := s.fatal(err); err != nil {
		return nil, err
	}
	return nil, &unreadableError{err: err}
}

// unreadableError is a message whose answer the client could not parse.
type unreadableError struct{ err error }

func (u *unreadableError) Error() string { return u.err.Error() }

// fatal returns the error a failed fetch ends the call with, or nil when the
// failure was the client's reading of one answer and the connection works
// again. A server's NO or BAD is the server's refusal, not a message's, and
// is the call's error; so is the session's time running out and a reconnect
// that fails.
func (s *Session) fatal(err error) error {
	if s.ctx != nil && s.ctx.Err() != nil {
		return fmt.Errorf("imapmail: fetching headers from %s: %w", s.examined, err)
	}
	var status *imap.Error
	if errors.As(err, &status) && !s.closed() {
		return fmt.Errorf("imapmail: fetching headers from %s: %w", s.examined, err)
	}
	if rerr := s.recover(err); rerr != nil {
		return fmt.Errorf("imapmail: fetching headers from %s: %w; %w", s.examined, err, rerr)
	}
	return nil
}

// reasonOf is the client's account of an answer it could not parse, bounded.
func reasonOf(err error) string {
	r := []rune(strings.ToValidUTF8(err.Error(), "?"))
	if len(r) > maxReasonRunes {
		r = append(r[:maxReasonRunes], '…')
	}
	return string(r)
}

// fetchHeaders is one FETCH of uids' headers, sizes and arrival times, and of
// their structure when withStructure.
func (s *Session) fetchHeaders(uids []uint32, withStructure bool) ([]Envelope, error) {
	opts := &imap.FetchOptions{
		UID:          true,
		RFC822Size:   true,
		InternalDate: true,
		BodySection:  []*imap.FetchItemBodySection{headerSection},
	}
	if withStructure {
		opts.BodyStructure = &imap.FetchItemBodyStructure{Extended: true}
	}
	msgs, err := s.c.Fetch(uidSet(uids), opts).Collect()
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
