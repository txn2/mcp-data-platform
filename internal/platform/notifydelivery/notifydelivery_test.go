package notifydelivery

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// passthroughEncryptor satisfies notification.StringEncryptor.
type passthroughEncryptor struct{}

func (passthroughEncryptor) Encrypt(s string) (string, error) { return s, nil }
func (passthroughEncryptor) Decrypt(s string) (string, error) { return s, nil }

// fakeSettings serves canned SMTP settings.
type fakeSettings struct {
	settings *notification.SMTPSettings
	err      error
}

func (f *fakeSettings) GetSMTP(context.Context) (*notification.SMTPSettings, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.settings, nil
}

func (*fakeSettings) SetSMTP(context.Context, notification.SMTPSettings, string) error {
	return nil
}

// captureSender records the last email.
type captureSender struct {
	sent []notification.Email
	err  error
}

func (c *captureSender) Send(_ context.Context, _ notification.SMTPSettings, e notification.Email) error {
	if c.err != nil {
		return c.err
	}
	c.sent = append(c.sent, e)
	return nil
}

func TestNew_NilDB(t *testing.T) {
	h, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if h != nil {
		t.Fatal("nil DB must yield nil handle")
	}
}

func TestNew_WithDB(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	h, err := New(Config{
		DB: db, Encryptor: passthroughEncryptor{},
		Branding: notification.Branding{Name: "Test"}, DigestHourUTC: 13,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if h == nil {
		t.Fatal("expected handle")
	}
	if h.Enqueuer() == nil || h.Prefs() == nil || h.Settings() == nil {
		t.Error("accessors must be populated")
	}
	if h.listener != nil {
		t.Error("empty DSN must not build a listener")
	}
}

func TestNew_WithDSNBuildsListener(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	h, err := New(Config{DB: db, DSN: "postgres://unused", Encryptor: passthroughEncryptor{}})
	if err != nil {
		t.Fatal(err)
	}
	if h.listener == nil {
		t.Error("DSN must build a listener")
	}
}

func TestNilHandleAccessors(t *testing.T) {
	var h *Handle
	if h.Enqueuer() != nil || h.Prefs() != nil || h.Settings() != nil {
		t.Error("nil handle accessors must return nil")
	}
	h.Start(context.Background()) // must not panic
	h.Stop()                      // must not panic
	if err := h.SendTest(context.Background(), "a@b.io"); err == nil {
		t.Error("nil handle SendTest must error")
	}
}

// fakeListener implements listenerControl.
type fakeListener struct {
	startErr error
	started  bool
	stopped  bool
}

func (f *fakeListener) Start(context.Context) error {
	f.started = true
	return f.startErr
}

func (f *fakeListener) Stop() { f.stopped = true }

func newTestHandle(t *testing.T, l listenerControl) *Handle {
	t.Helper()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h, err := New(Config{DB: db, Encryptor: passthroughEncryptor{}})
	if err != nil {
		t.Fatal(err)
	}
	h.listener = l
	return h
}

func TestStart_ListenerSuccess(t *testing.T) {
	l := &fakeListener{}
	h := newTestHandle(t, l)
	h.Start(context.Background())
	defer h.Stop()
	if !l.started {
		t.Error("listener must start")
	}
}

func TestStart_ListenerFailureDegradesToPolling(t *testing.T) {
	l := &fakeListener{startErr: errors.New("no LISTEN privilege")}
	h := newTestHandle(t, l)
	h.Start(context.Background())
	defer h.Stop()
	if h.listener != nil {
		t.Error("failed listener must be dropped so Stop skips it")
	}
	if l.stopped {
		t.Error("dropped listener must not be stopped")
	}
}

func TestStop_StopsListener(t *testing.T) {
	l := &fakeListener{}
	h := newTestHandle(t, l)
	h.Start(context.Background())
	h.Stop()
	if !l.stopped {
		t.Error("Stop must stop the listener")
	}
}

func TestStartStop_NoListener(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	h, err := New(Config{DB: db, Encryptor: passthroughEncryptor{}})
	if err != nil {
		t.Fatal(err)
	}
	// The worker's settings reads hit sqlmock without expectations, which
	// surfaces as a logged read error and no delivery attempts; safe here.
	h.Start(context.Background())
	h.Stop()
}

func testRenderer(t *testing.T) *notification.Renderer {
	t.Helper()
	r, err := notification.NewRenderer(notification.Branding{Name: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSendTest(t *testing.T) {
	sender := &captureSender{}
	h := &Handle{
		settings: &fakeSettings{settings: &notification.SMTPSettings{
			Enabled: true, Host: "smtp.example.com", Port: 587, From: "p@example.com",
		}},
		renderer: testRenderer(t),
		sender:   sender,
	}

	if err := h.SendTest(context.Background(), "admin@example.com"); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if len(sender.sent) != 1 || sender.sent[0].To != "admin@example.com" {
		t.Fatalf("unexpected sends: %+v", sender.sent)
	}
}

func TestSendTest_SettingsError(t *testing.T) {
	h := &Handle{
		settings: &fakeSettings{err: errors.New("no row")},
		renderer: testRenderer(t),
		sender:   &captureSender{},
	}
	if err := h.SendTest(context.Background(), "a@b.io"); err == nil {
		t.Fatal("expected settings error")
	}
}

func TestSendTest_SendError(t *testing.T) {
	h := &Handle{
		settings: &fakeSettings{settings: &notification.SMTPSettings{Enabled: true, Host: "smtp.example.com"}},
		renderer: testRenderer(t),
		sender:   &captureSender{err: errors.New("smtp down")},
	}
	if err := h.SendTest(context.Background(), "a@b.io"); err == nil {
		t.Fatal("expected send error")
	}
}

func TestSendTest_DisabledOrUnconfigured(t *testing.T) {
	tests := []struct {
		name     string
		settings *fakeSettings
	}{
		{name: "never configured", settings: &fakeSettings{err: notification.ErrNotFound}},
		{name: "disabled", settings: &fakeSettings{settings: &notification.SMTPSettings{
			Enabled: false, Host: "smtp.example.com",
		}}},
		{name: "no host", settings: &fakeSettings{settings: &notification.SMTPSettings{Enabled: true}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender := &captureSender{}
			h := &Handle{settings: tc.settings, renderer: testRenderer(t), sender: sender}
			err := h.SendTest(context.Background(), "a@b.io")
			if !errors.Is(err, notification.ErrSMTPNotConfigured) {
				t.Fatalf("err = %v; want ErrSMTPNotConfigured", err)
			}
			if len(sender.sent) != 0 {
				t.Error("nothing may be sent around the enabled switch")
			}
		})
	}
}
