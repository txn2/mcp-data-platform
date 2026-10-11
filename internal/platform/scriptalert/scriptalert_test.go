package scriptalert

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyalert"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func mockStore(t *testing.T) (SettingsStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), mock
}

func TestPostgresStore_GetSetAndTheUnconfiguredCase(t *testing.T) {
	store, mock := mockStore(t)
	ctx := context.Background()
	query := regexp.QuoteMeta(`SELECT value, updated_by, updated_at FROM platform_settings WHERE section = $1`)

	mock.ExpectQuery(query).WithArgs(SettingsSection).WillReturnRows(sqlmock.NewRows([]string{"value"}))
	_, err := store.Get(ctx)
	assert.ErrorIs(t, err, ErrNotFound)

	at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery(query).WithArgs(SettingsSection).WillReturnRows(
		sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow([]byte(`{"channels":["ops"]}`), "admin@example.com", at))
	got, err := store.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, Settings{Channels: []string{"ops"}, UpdatedBy: "admin@example.com", UpdatedAt: at}, *got)

	mock.ExpectQuery(query).WithArgs(SettingsSection).WillReturnRows(
		sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow([]byte(`{`), "", at))
	_, err = store.Get(ctx)
	assert.ErrorContains(t, err, "decoding")

	mock.ExpectQuery(query).WillReturnError(errors.New("db down"))
	_, err = store.Get(ctx)
	assert.ErrorContains(t, err, "querying")

	mock.ExpectExec("INSERT INTO platform_settings").
		WithArgs(SettingsSection, []byte(`{"channels":["ops"]}`), "admin@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.Set(ctx, Settings{Channels: []string{"ops"}, UpdatedBy: "stale"}, "admin@example.com"))
	mock.ExpectExec("INSERT INTO platform_settings").WillReturnError(errors.New("db down"))
	assert.ErrorContains(t, store.Set(ctx, Settings{}, "a"), "storing")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// memSettings serves settings from memory.
type memSettings struct {
	settings *Settings
	err      error
}

func (m memSettings) Get(context.Context) (*Settings, error) {
	if m.settings == nil && m.err == nil {
		return nil, ErrNotFound
	}
	return m.settings, m.err
}
func (memSettings) Set(context.Context, Settings, string) error { return nil }

func TestSettingsOf(t *testing.T) {
	got, err := SettingsOf(context.Background(), memSettings{})
	require.NoError(t, err)
	assert.Empty(t, got.Channels)
	_, err = SettingsOf(context.Background(), memSettings{err: errors.New("db down")})
	assert.Error(t, err)
}

func TestSettingsInput(t *testing.T) {
	names := []string{" ops", "ops", ""}
	in := SettingsInput{Channels: &names}
	require.Empty(t, in.Validate())
	assert.Equal(t, []string{"ops"}, in.Settings().Channels)
	assert.Empty(t, (&SettingsInput{}).Validate())
	assert.Equal(t, []string{}, (&SettingsInput{}).Settings().Channels)
	many := make([]string, notifyalert.MaxChannels+1)
	for i := range many {
		many[i] = string(rune('a' + i))
	}
	assert.Contains(t, (&SettingsInput{Channels: &many}).Validate(), "at most")
	assert.Equal(t, []string{}, Settings{}.View().Channels)
}

// oneChannel serves the ops channel.
type oneChannel struct{}

func (oneChannel) List(context.Context) ([]notification.Channel, error) { return nil, nil }
func (oneChannel) Get(context.Context, string) (*notification.Channel, error) {
	return &notification.Channel{
		Name: "ops", Kind: notification.ChannelKindEmail, Enabled: true,
		Mode: notification.ChannelModeImmediate, Recipients: []string{"ops@example.com"},
	}, nil
}
func (oneChannel) Set(context.Context, notification.Channel) error { return nil }
func (oneChannel) Delete(context.Context, string) error            { return nil }

type nopPrefs struct{}

func (nopPrefs) Get(_ context.Context, e string) (notification.Prefs, error) {
	return notification.DefaultPrefs(e), nil
}

func (nopPrefs) Set(_ context.Context, e string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(e), nil
}

// countingQueue counts channel sends.
type countingQueue struct {
	notification.QueueStore
	sends int
}

func (q *countingQueue) EnqueueChannel(context.Context, []notification.Notification, notification.ChannelGate) (notification.ChannelOutcome, error) {
	q.sends++
	return notification.ChannelOutcome{}, nil
}

func TestPoster(t *testing.T) {
	q := &countingQueue{}
	enq := notification.NewEnqueuer(nopPrefs{}, q, 13)
	t.Cleanup(enq.Close)
	sender := notifyalert.New(oneChannel{}, enq)
	p := NewPoster(memSettings{settings: &Settings{Channels: []string{"ops"}}}, sender)
	n, err := p.Post(context.Background(), "script:s1", notification.Payload{Kind: notification.KindScriptRun, ItemTitle: "nightly"})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, q.sends)

	_, err = NewPoster(memSettings{err: errors.New("db down")}, sender).Post(context.Background(), "k", notification.Payload{})
	assert.Error(t, err)
	var nilPoster *Poster
	n, err = nilPoster.Post(context.Background(), "k", notification.Payload{})
	assert.Zero(t, n)
	assert.NoError(t, err)
	assert.Nil(t, NewPoster(nil, sender))
}
