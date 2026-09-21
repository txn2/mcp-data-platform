package notifychannel

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// channelColumns is the scan list every read returns.
var channelColumns = []string{"name", "kind", "config", "enabled", "created_by", "updated_at"}

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	return NewPostgresStore(db), mock, func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
		_ = db.Close()
	}
}

func TestSet_WritesEveryKindSpecificFieldIntoTheConfig(t *testing.T) {
	// The kind-specific half is JSONB rather than a column per kind, so this
	// is the only thing holding the encoding to what scanChannel reads back.
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO notification_channels`)).
		WithArgs("ops", notification.ChannelKindMattermost, sqlmock.AnyArg(), true, "admin@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := store.Set(context.Background(), notification.Channel{
		Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true,
		Connection: "chat-bot", Target: "C1", Mode: notification.ChannelModeImmediate,
		RepeatAfter: 30 * time.Minute, MaxPerHour: 5, CreatedBy: "admin@example.com",
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
}

func TestGet_DecodesTheConfigBackIntoTheChannel(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	cfg := `{"description":"ops alerts","connection":"chat-bot","target":"C1",` +
		`"mode":"immediate","repeat_after_seconds":1800,"max_per_hour":5}`
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT name, kind, config, enabled, created_by, updated_at`)).
		WithArgs("ops").
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow("ops", notification.ChannelKindMattermost, []byte(cfg), true, "admin@example.com", time.Now()))

	ch, err := store.Get(context.Background(), "ops")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ch.Connection != "chat-bot" || ch.Target != "C1" || ch.Description != "ops alerts" {
		t.Errorf("channel = %+v, want the stored config decoded", *ch)
	}
	if ch.RepeatAfter != 30*time.Minute {
		t.Errorf("RepeatAfter = %v, want 30m; seconds are stored so the row is readable", ch.RepeatAfter)
	}
	if ch.MaxPerHour != 5 {
		t.Errorf("MaxPerHour = %d, want 5", ch.MaxPerHour)
	}
}

func TestGet_ReportsAnAbsentChannelAsNotFound(t *testing.T) {
	// The worker matches on this to fail a row naming a deleted channel at
	// once rather than retrying it five times.
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT name, kind, config`)).
		WithArgs("gone").
		WillReturnRows(sqlmock.NewRows(channelColumns))

	_, err := store.Get(context.Background(), "gone")
	if !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("err = %v, want ErrChannelNotFound", err)
	}
}

func TestList_ReturnsChannelsInNameOrder(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery(regexp.QuoteMeta(`ORDER BY name`)).
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow("a-team", notification.ChannelKindWebhook, []byte(`{"connection":"hook"}`), true, "", time.Now()).
			AddRow("b-team", notification.ChannelKindEmail, []byte(`{"recipients":["x@example.com"]}`), false, "", time.Now()))

	channels, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(channels) != 2 || channels[0].Name != "a-team" || channels[1].Name != "b-team" {
		t.Fatalf("List returned %+v", channels)
	}
	if channels[1].Enabled {
		t.Error("a disabled channel was reported as enabled")
	}
	if len(channels[1].Recipients) != 1 {
		t.Errorf("an email channel's recipients did not survive the round trip: %+v", channels[1])
	}
}

func TestDelete_RemovesTheChannel(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM notification_channels WHERE name = $1`)).
		WithArgs("ops").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.Delete(context.Background(), "ops"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestStore_ReportsDatabaseFailures(t *testing.T) {
	// Each of these wraps with what it was doing, which is what an operator
	// reads when the pool is unhealthy.
	tests := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
		call  func(*PostgresStore) error
	}{
		{"list", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("SELECT name").WillReturnError(errors.New("pool down"))
		}, func(s *PostgresStore) error {
			_, err := s.List(context.Background())
			return err
		}},
		{"get", func(m sqlmock.Sqlmock) {
			m.ExpectQuery("SELECT name").WillReturnError(errors.New("pool down"))
		}, func(s *PostgresStore) error {
			_, err := s.Get(context.Background(), "ops")
			return err
		}},
		{"set", func(m sqlmock.Sqlmock) {
			m.ExpectExec("INSERT INTO").WillReturnError(errors.New("pool down"))
		}, func(s *PostgresStore) error {
			return s.Set(context.Background(), notification.Channel{Name: "ops", Kind: notification.ChannelKindEmail})
		}},
		{"delete", func(m sqlmock.Sqlmock) {
			m.ExpectExec("DELETE FROM").WillReturnError(errors.New("pool down"))
		}, func(s *PostgresStore) error {
			return s.Delete(context.Background(), "ops")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, mock, done := newMockStore(t)
			defer done()
			tc.setup(mock)
			if err := tc.call(store); err == nil {
				t.Fatal("a database failure was reported as success")
			}
		})
	}
}

func TestGet_ReportsAnUnreadableConfig(t *testing.T) {
	// A row whose JSONB is not what this package wrote is a defect worth
	// reporting, not a channel with empty fields.
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT name").
		WithArgs("ops").
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow("ops", notification.ChannelKindMattermost, []byte(`{not json`), true, "", time.Now()))

	if _, err := store.Get(context.Background(), "ops"); err == nil {
		t.Fatal("an undecodable config was accepted")
	}
}
