package notifyqueue

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func TestDecideKey(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	first := now.Add(-50 * time.Minute)
	cases := []struct {
		name   string
		st     keyState
		window time.Duration
		want   keyDecision
	}{
		{"a key never sent goes through", keyState{}, time.Hour, keyDecision{deliver: true}},
		{
			"inside the window is absorbed",
			keyState{found: true, lastSentAt: now.Add(-30 * time.Minute), window: time.Hour},
			time.Hour,
			keyDecision{},
		},
		{
			"past the window goes through with the count",
			keyState{found: true, lastSentAt: now.Add(-2 * time.Hour), window: time.Hour, suppressed: 4, firstSuppressed: &first},
			time.Hour,
			keyDecision{deliver: true, suppressed: 4, since: &first},
		},
		{
			"the stored longer window holds",
			keyState{found: true, lastSentAt: now.Add(-2 * time.Hour), window: 6 * time.Hour},
			time.Hour,
			keyDecision{},
		},
		{
			"a send's longer window holds",
			keyState{found: true, lastSentAt: now.Add(-2 * time.Hour), window: time.Hour},
			3 * time.Hour,
			keyDecision{},
		},
		{
			"the window's end is the first moment through",
			keyState{found: true, lastSentAt: now.Add(-time.Hour), window: time.Hour},
			time.Hour,
			keyDecision{deliver: true},
		},
	}
	for _, tc := range cases {
		if got := decideKey(tc.st, now, tc.window); got != tc.want {
			t.Errorf("%s: decideKey = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestWithSuppression_StampsACopyOfTheDocument(t *testing.T) {
	since := time.Now()
	doc := notification.Document{Title: "t"}
	rows := []notification.Notification{{Payload: notification.Payload{Document: &doc}}, {Payload: notification.Payload{}}}
	out := withSuppression(rows, keyDecision{deliver: true, suppressed: 2, since: &since})
	if out[0].Payload.Document.Suppressed != 2 || out[0].Payload.Document.SuppressedSince != &since {
		t.Errorf("stamped document = %+v", out[0].Payload.Document)
	}
	if doc.Suppressed != 0 {
		t.Error("the caller's document was modified")
	}
	if same := withSuppression(rows, keyDecision{deliver: true}); &same[0] != &rows[0] {
		t.Error("a decision with nothing suppressed copied the rows")
	}
}

// The statements a channel send issues, as sqlmock matches them. The
// real-Postgres tests prove the SQL; these prove the control flow around it.
var (
	sqlLock      = regexp.QuoteMeta(`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`)
	sqlCount     = regexp.QuoteMeta(`SELECT COUNT(DISTINCT created_at) FROM notifications`)
	sqlKeyRead   = regexp.QuoteMeta(`SELECT NOW(), last_sent_at, window_seconds, suppressed, first_suppressed_at`)
	sqlNow       = regexp.QuoteMeta(`SELECT NOW()`)
	sqlKeyInsert = regexp.QuoteMeta(`INSERT INTO notification_channel_keys`)
	sqlKeyBump   = regexp.QuoteMeta(`UPDATE notification_channel_keys`)
	sqlRow       = regexp.QuoteMeta(`INSERT INTO notifications`)
	sqlNotify    = regexp.QuoteMeta(`SELECT pg_notify($1, '')`)
)

func chanRow() notification.Notification {
	doc := notification.Document{Title: "t"}
	return notification.Notification{
		Recipient: "channel:ops", Channel: "ops", Category: notification.CategoryChannel,
		Payload: notification.Payload{Kind: notification.KindChannel, Document: &doc},
	}
}

func TestEnqueueChannel_UnderTheCapWithNoKeyWritesTheRows(t *testing.T) {
	s, mock, done := newMockQueueStore(t)
	defer done()
	mock.ExpectBegin()
	mock.ExpectExec(sqlLock).WithArgs("notification-channel:ops").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(sqlCount).WithArgs("ops").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(3))
	mock.ExpectExec(sqlRow).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectExec(sqlNotify).WillReturnResult(sqlmock.NewResult(0, 0))

	out, err := s.EnqueueChannel(context.Background(), []notification.Notification{chanRow()},
		notification.ChannelGate{Channel: "ops", MaxPerHour: 60})
	if err != nil || out.Suppressed {
		t.Fatalf("out %+v err %v", out, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestEnqueueChannel_OverTheCapIsRefused(t *testing.T) {
	s, mock, done := newMockQueueStore(t)
	defer done()
	mock.ExpectBegin()
	mock.ExpectExec(sqlLock).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(sqlCount).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(60))
	mock.ExpectRollback()
	_, err := s.EnqueueChannel(context.Background(), []notification.Notification{chanRow()},
		notification.ChannelGate{Channel: "ops", MaxPerHour: 60})
	if !errors.Is(err, notification.ErrChannelOverCap) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnqueueChannel_AKeyInsideItsWindowIsAbsorbed(t *testing.T) {
	s, mock, done := newMockQueueStore(t)
	defer done()
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectExec(sqlLock).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(sqlKeyRead).WithArgs("ops", "k").WillReturnRows(
		sqlmock.NewRows([]string{"now", "last", "win", "sup", "first"}).AddRow(now, now.Add(-time.Minute), 3600, 1, now))
	mock.ExpectExec(sqlKeyBump).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	out, err := s.EnqueueChannel(context.Background(), []notification.Notification{chanRow()},
		notification.ChannelGate{Channel: "ops", Key: "k", Window: time.Hour})
	if err != nil || !out.Suppressed {
		t.Fatalf("out %+v err %v", out, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestEnqueueChannel_ANewKeyGoesThrough(t *testing.T) {
	s, mock, done := newMockQueueStore(t)
	defer done()
	mock.ExpectBegin()
	mock.ExpectExec(sqlLock).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(sqlKeyRead).WillReturnRows(sqlmock.NewRows([]string{"now", "last", "win", "sup", "first"}))
	mock.ExpectQuery(sqlNow).WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Now()))
	mock.ExpectExec(sqlKeyInsert).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(sqlRow).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectExec(sqlNotify).WillReturnResult(sqlmock.NewResult(0, 0))
	out, err := s.EnqueueChannel(context.Background(), []notification.Notification{chanRow()},
		notification.ChannelGate{Channel: "ops", Key: "k", Window: time.Hour})
	if err != nil || out.Suppressed {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestEnqueueChannel_EachFailureIsReported(t *testing.T) {
	boom := errors.New("boom")
	gate := notification.ChannelGate{Channel: "ops", MaxPerHour: 60, Key: "k", Window: time.Hour}
	cases := map[string]func(m sqlmock.Sqlmock){
		"begin": func(m sqlmock.Sqlmock) { m.ExpectBegin().WillReturnError(boom) },
		"lock": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec(sqlLock).WillReturnError(boom)
		},
		"count": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec(sqlLock).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery(sqlCount).WillReturnError(boom)
		},
		"key read": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec(sqlLock).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery(sqlCount).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
			m.ExpectQuery(sqlKeyRead).WillReturnError(boom)
		},
		"clock": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec(sqlLock).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery(sqlCount).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
			m.ExpectQuery(sqlKeyRead).WillReturnRows(sqlmock.NewRows([]string{"now", "last", "win", "sup", "first"}))
			m.ExpectQuery(sqlNow).WillReturnError(boom)
		},
		"key write": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec(sqlLock).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery(sqlCount).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
			m.ExpectQuery(sqlKeyRead).WillReturnRows(sqlmock.NewRows([]string{"now", "last", "win", "sup", "first"}))
			m.ExpectQuery(sqlNow).WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Now()))
			m.ExpectExec(sqlKeyInsert).WillReturnError(boom)
		},
		"row": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec(sqlLock).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery(sqlCount).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
			m.ExpectQuery(sqlKeyRead).WillReturnRows(sqlmock.NewRows([]string{"now", "last", "win", "sup", "first"}))
			m.ExpectQuery(sqlNow).WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Now()))
			m.ExpectExec(sqlKeyInsert).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec(sqlRow).WillReturnError(boom)
		},
		"commit": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec(sqlLock).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery(sqlCount).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
			m.ExpectQuery(sqlKeyRead).WillReturnRows(sqlmock.NewRows([]string{"now", "last", "win", "sup", "first"}))
			m.ExpectQuery(sqlNow).WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(time.Now()))
			m.ExpectExec(sqlKeyInsert).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec(sqlRow).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectCommit().WillReturnError(boom)
		},
	}
	for name, setup := range cases {
		s, mock, done := newMockQueueStore(t)
		setup(mock)
		if _, err := s.EnqueueChannel(context.Background(), []notification.Notification{chanRow()}, gate); err == nil {
			t.Errorf("%s: a failure was not reported", name)
		}
		done()
	}
	s, _, done := newMockQueueStore(t)
	defer done()
	if out, err := s.EnqueueChannel(context.Background(), nil, gate); err != nil || out.Suppressed {
		t.Errorf("no rows: %+v %v", out, err)
	}
}

func TestPurgeChannelKeys(t *testing.T) {
	s, mock, done := newMockQueueStore(t)
	defer done()
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM notification_channel_keys`)).WithArgs(int64(3600)).WillReturnResult(sqlmock.NewResult(0, 4))
	if n, err := s.PurgeChannelKeys(context.Background(), time.Hour); err != nil || n != 4 {
		t.Errorf("n %d err %v", n, err)
	}
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM notification_channel_keys`)).WillReturnError(errors.New("boom"))
	if _, err := s.PurgeChannelKeys(context.Background(), time.Hour); err == nil {
		t.Error("a failure was not reported")
	}
}

func TestChannelActivity(t *testing.T) {
	s, mock, done := newMockQueueStore(t)
	defer done()
	now := time.Now()
	q := regexp.QuoteMeta(`SELECT`)
	mock.ExpectQuery(q).WithArgs("ops").WillReturnRows(sqlmock.NewRows([]string{"d", "e", "ea", "h", "p", "s"}).
		AddRow(now, "HTTP 403", now, 4, 1, 2))
	act, err := s.ChannelActivity(context.Background(), "ops")
	if err != nil || act.LastDeliveredAt == nil || act.LastError != "HTTP 403" || act.SentLastHour != 4 || act.Pending != 1 || act.Suppressed != 2 {
		t.Errorf("activity %+v err %v", act, err)
	}
	mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"d", "e", "ea", "h", "p", "s"}).AddRow(nil, nil, nil, 0, 0, 0))
	if act, err := s.ChannelActivity(context.Background(), "ops"); err != nil || act.LastDeliveredAt != nil || act.LastError != "" {
		t.Errorf("quiet channel %+v err %v", act, err)
	}
	mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
	if _, err := s.ChannelActivity(context.Background(), "ops"); err == nil {
		t.Error("a failure was not reported")
	}
}

func TestRunPosts(t *testing.T) {
	s, mock, done := newMockQueueStore(t)
	defer done()
	now := time.Now()
	q := regexp.QuoteMeta(`SELECT channel, created_at`)
	cols := []string{"channel", "created_at", "title", "n", "sent", "failed", "sent_at", "err"}
	mock.ExpectQuery(q).WithArgs("run_1").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("ops", now, "Feed stale", 1, 1, 0, now, "").
		AddRow("list", now, nil, 2, 0, 0, nil, ""))
	posts, err := s.RunPosts(context.Background(), "run_1")
	if err != nil || len(posts) != 2 || posts[0].LastSentAt == nil || posts[1].Title != "" {
		t.Fatalf("posts %+v err %v", posts, err)
	}
	mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
	if _, err := s.RunPosts(context.Background(), "r"); err == nil {
		t.Error("a query failure was not reported")
	}
	mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"channel"}).AddRow("x"))
	if _, err := s.RunPosts(context.Background(), "r"); err == nil {
		t.Error("a scan failure was not reported")
	}
}
