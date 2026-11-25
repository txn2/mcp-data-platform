package platformfeedback

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/secretscan"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// Query fragments the store's statements are matched by.
const (
	qSettings     = "FROM platform_settings"
	qSaveSettings = "INSERT INTO platform_settings"
	qReportsSince = "COUNT\\(\\*\\) FILTER"
	qOpen         = "ORDER BY last_reported_at DESC LIMIT \\$1"
	qCreate       = "INSERT INTO platform_feedback \\(id"
	qReport       = "INSERT INTO platform_feedback_reports"
	qCount        = "UPDATE platform_feedback SET report_count"
	qRefusal      = "INSERT INTO platform_feedback_refusals"
	qList         = "COUNT\\(\\*\\) OVER \\(\\)"
	qStats        = "SELECT \\(SELECT COUNT"
	qGet          = "FROM platform_feedback WHERE id = \\$1"
	qReports      = "SELECT persona, calls, reported_at"
	qDeliveries   = "FROM notifications"
	qSetStatus    = "UPDATE platform_feedback SET status"
)

var errDown = errors.New("database down")

// fakeChannels is the notification channel store: the channels it holds by
// name, any other name not found.
type fakeChannels struct{ names map[string]bool }

func (fakeChannels) List(context.Context) ([]notification.Channel, error) { return nil, nil }

func (f fakeChannels) Get(_ context.Context, name string) (*notification.Channel, error) {
	if !f.names[name] {
		return nil, errors.New("channel not found")
	}
	return &notification.Channel{Name: name, Kind: "webhook"}, nil
}

func (fakeChannels) Set(context.Context, notification.Channel) error { return nil }
func (fakeChannels) Delete(context.Context, string) error            { return nil }

// fakeSender queues a send to "ops", refuses "broken" and absorbs "quiet"
// without queueing, recording each document it was handed.
type fakeSender struct{ sent []notification.ChannelSend }

func (f *fakeSender) NotifyChannel(_ context.Context, s notification.ChannelSend) (notification.ChannelResult, error) {
	f.sent = append(f.sent, s)
	switch s.Channel.Name {
	case "broken":
		return notification.ChannelResult{}, errors.New("queue full")
	case "quiet":
		return notification.ChannelResult{Suppressed: true}, nil
	}
	return notification.ChannelResult{Queued: 1}, nil
}

var allChannels = fakeChannels{names: map[string]bool{"ops": true, "broken": true, "quiet": true}}

// wired is a Service over sqlmock with channels, a sender and an embedder.
func wired(t *testing.T, emb []float32) (*Service, sqlmock.Sqlmock, *fakeSender) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sender := &fakeSender{}
	svc := New(Deps{
		DB:       db,
		Section:  settingsjson.New[Settings](db, SectionName),
		Detector: secretscan.Detector{Known: func(context.Context) ([]string, error) { return []string{plantedSecret}, nil }},
		Embed: func(context.Context, string) ([]float32, error) {
			if emb == nil {
				return nil, errors.New("no embedder answering")
			}
			return emb, nil
		},
		Channels:     allChannels,
		Sender:       sender,
		KnownPersona: func(p string) bool { return p == "analyst" },
		PortalURL:    "https://portal.example.com",
		Now:          func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) },
	})
	return svc, mock, sender
}

func expectSettings(mock sqlmock.Sqlmock, set Settings) {
	raw, _ := json.Marshal(set)
	mock.ExpectQuery(qSettings).WillReturnRows(
		sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow(raw, "admin@example.com", time.Now()))
}

func openRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "text", "embedding"})
}

func met(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// TestSubmit_ANewPostIsStoredAndSentToEachChannel: a post repeating nothing is
// stored with its first report and sent to each channel the settings name; a
// channel that is gone, refuses or absorbs the send is not counted.
func TestSubmit_ANewPostIsStoredAndSentToEachChannel(t *testing.T) {
	svc, mock, sender := wired(t, []float32{1, 0, 0})
	expectSettings(mock, Settings{Enabled: true, Channels: []string{"ops", "gone", "broken", "quiet"}})
	mock.ExpectQuery(qReportsSince).WillReturnRows(sqlmock.NewRows([]string{"s", "p"}).AddRow(1, 4))
	mock.ExpectQuery(qOpen).WillReturnRows(openRows().AddRow("pfb_old", "something else\nentirely\ns3_list", pq.Float32Array{0, 1, 0}))
	mock.ExpectBegin()
	mock.ExpectExec(qCreate).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(qReport).WithArgs(sqlmock.AnyArg(), "analyst", "u-1", "dps_1", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	res, err := svc.Submit(context.Background(), Caller{UserID: "u-1", Persona: "analyst", SessionID: "dps_1"}, validPost())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.ID, "pfb_") || res.ReportCount != 1 || res.Folded || res.Channels != 1 {
		t.Errorf("result = %+v; want a new post reaching one channel", res)
	}
	if !strings.Contains(res.Message, "queued for 1 channel") {
		t.Errorf("message = %q", res.Message)
	}
	if len(sender.sent) != 3 {
		t.Fatalf("sends tried = %d, want 3 (the missing channel is skipped)", len(sender.sent))
	}
	doc := sender.sent[0].Document
	if doc.Key != keyPrefix+res.ID || !sender.sent[0].Platform || !strings.HasSuffix(doc.Link, "/portal/admin/platform-feedback/"+res.ID) {
		t.Errorf("document = %+v", doc)
	}
	met(t, mock)
}

// TestSubmit_ARepeatIsCountedOnTheOpenPost: the same words, in another case,
// or a near-identical embedding fold onto the open post: counted, not stored
// again and not sent.
func TestSubmit_ARepeatIsCountedOnTheOpenPost(t *testing.T) {
	p := validPost()
	for name, open := range map[string]*sqlmock.Rows{
		"same words":     openRows().AddRow("pfb_same", strings.ToUpper(p.text()), nil),
		"same embedding": openRows().AddRow("pfb_same", "worded quite differently", pq.Float32Array{0.6, 0.8, 0}),
	} {
		t.Run(name, func(t *testing.T) {
			svc, mock, sender := wired(t, []float32{0.6, 0.8, 0})
			expectSettings(mock, Settings{Enabled: true, Channels: []string{"ops"}})
			mock.ExpectQuery(qReportsSince).WillReturnRows(sqlmock.NewRows([]string{"s", "p"}).AddRow(0, 0))
			mock.ExpectQuery(qOpen).WillReturnRows(open)
			mock.ExpectBegin()
			mock.ExpectQuery(qCount).WithArgs("pfb_same").WillReturnRows(sqlmock.NewRows([]string{"report_count"}).AddRow(3))
			mock.ExpectExec(qReport).WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()

			res, err := svc.Submit(context.Background(), Caller{Persona: "analyst", SessionID: "dps_2"}, p)
			if err != nil {
				t.Fatal(err)
			}
			if res.ID != "pfb_same" || !res.Folded || res.ReportCount != 3 || len(sender.sent) != 0 {
				t.Errorf("result = %+v, sends %d; want folded onto pfb_same with 3 reports and nothing sent", res, len(sender.sent))
			}
			met(t, mock)
		})
	}
}

// TestSubmit_StoreFailuresReachTheCaller: a failed count, an unreadable open
// list (the post is then stored as new), and each failed write of a new post
// or a repeat are answered as errors, never as a post.
func TestSubmit_StoreFailuresReachTheCaller(t *testing.T) {
	on := Settings{Enabled: true}
	start := func(t *testing.T) (*Service, sqlmock.Sqlmock) {
		t.Helper()
		svc, mock, _ := wired(t, nil)
		expectSettings(mock, on)
		mock.ExpectQuery(qReportsSince).WillReturnRows(sqlmock.NewRows([]string{"s", "p"}).AddRow(0, 0))
		return svc, mock
	}
	cases := map[string]func(mock sqlmock.Sqlmock){
		"open unreadable, begin fails": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qOpen).WillReturnError(errDown)
			m.ExpectBegin().WillReturnError(errDown)
		},
		"insert fails": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qOpen).WillReturnRows(openRows())
			m.ExpectBegin()
			m.ExpectExec(qCreate).WillReturnError(errDown)
			m.ExpectRollback()
		},
		"report insert fails": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qOpen).WillReturnRows(openRows())
			m.ExpectBegin()
			m.ExpectExec(qCreate).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectExec(qReport).WillReturnError(errDown)
			m.ExpectRollback()
		},
		"commit fails": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qOpen).WillReturnRows(openRows())
			m.ExpectBegin()
			m.ExpectExec(qCreate).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectExec(qReport).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectCommit().WillReturnError(errDown)
		},
		"open row unreadable": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qOpen).WillReturnRows(openRows().AddRow("pfb_x", "t", "not an array"))
			m.ExpectBegin().WillReturnError(errDown)
		},
		"repeat begin fails": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qOpen).WillReturnRows(openRows().AddRow("pfb_same", validPost().text(), nil))
			m.ExpectBegin().WillReturnError(errDown)
		},
		"repeat count fails": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qOpen).WillReturnRows(openRows().AddRow("pfb_same", validPost().text(), nil))
			m.ExpectBegin()
			m.ExpectQuery(qCount).WillReturnError(errDown)
			m.ExpectRollback()
		},
		"repeat report fails": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qOpen).WillReturnRows(openRows().AddRow("pfb_same", validPost().text(), nil))
			m.ExpectBegin()
			m.ExpectQuery(qCount).WillReturnRows(sqlmock.NewRows([]string{"report_count"}).AddRow(2))
			m.ExpectExec(qReport).WillReturnError(errDown)
			m.ExpectRollback()
		},
		"repeat commit fails": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(qOpen).WillReturnRows(openRows().AddRow("pfb_same", validPost().text(), nil))
			m.ExpectBegin()
			m.ExpectQuery(qCount).WillReturnRows(sqlmock.NewRows([]string{"report_count"}).AddRow(2))
			m.ExpectExec(qReport).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectCommit().WillReturnError(errDown)
		},
	}
	for name, expect := range cases {
		t.Run(name, func(t *testing.T) {
			svc, mock := start(t)
			expect(mock)
			res, err := svc.Submit(context.Background(), Caller{Persona: "analyst", SessionID: "dps_3"}, validPost())
			var r *RefusedError
			if err == nil || errors.As(err, &r) || res.ID != "" {
				t.Errorf("res %+v err %v; want a plain error and no post", res, err)
			}
		})
	}

	t.Run("count fails", func(t *testing.T) {
		svc, mock, _ := wired(t, nil)
		expectSettings(mock, on)
		mock.ExpectQuery(qReportsSince).WillReturnError(errDown)
		if _, err := svc.Submit(context.Background(), Caller{Persona: "analyst"}, validPost()); err == nil {
			t.Error("a failed limit count let the post through")
		}
		// A failed count is an error, not a refusal: no refusal row is
		// written, so the week's refusals count only refusals.
		met(t, mock)
	})
}

// TestSubmit_AnUncheckablePostIsRefused: when the stored secrets cannot be
// read the post is refused as unchecked, never stored unchecked.
func TestSubmit_AnUncheckablePostIsRefused(t *testing.T) {
	svc, mock, _ := wired(t, nil)
	svc.d.Detector = secretscan.Detector{Known: func(context.Context) ([]string, error) { return nil, errDown }}
	expectSettings(mock, Settings{Enabled: true})
	mock.ExpectExec(qRefusal).WithArgs(reasonSecret, "analyst").WillReturnResult(sqlmock.NewResult(1, 1))
	_, err := svc.Submit(context.Background(), Caller{Persona: "analyst"}, validPost())
	var r *RefusedError
	if !errors.As(err, &r) || r.Code != CodeSecret || !strings.Contains(r.Msg, "could not be checked") {
		t.Fatalf("err = %v", err)
	}
	met(t, mock)
}

// TestSettings_RoundTrip: unsaved settings read as off with the default
// limits; a save validates against the channels and personas that exist,
// stores what was sent with its author, and answers what is in force.
func TestSettings_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := (*Service)(nil).Settings(ctx).Value; got.Enabled || got.SessionLimit != DefaultSessionLimit {
		t.Errorf("a nil service's settings = %+v", got)
	}
	svc, mock, _ := wired(t, nil)
	mock.ExpectQuery(qSettings).WillReturnError(errDown)
	if got := svc.Settings(ctx).Value; got.Enabled || got.PersonaDailyLimit != DefaultPersonaDailyLimit || got.Channels == nil {
		t.Errorf("unreadable settings = %+v", got)
	}

	var inv *InvalidError
	if _, err := svc.SaveSettings(ctx, Settings{Channels: []string{"gone"}}, "admin@example.com"); !errors.As(err, &inv) {
		t.Errorf("a missing channel was saved: %v", err)
	}
	if _, err := svc.SaveSettings(ctx, Settings{Personas: []string{"nobody"}}, "admin@example.com"); !errors.As(err, &inv) {
		t.Errorf("a missing persona was saved: %v", err)
	}

	want := Settings{Enabled: true, Channels: []string{"ops"}, Personas: []string{"analyst"}, SessionLimit: 3}
	stored, _ := json.Marshal(want.withDefaults())
	mock.ExpectExec(qSaveSettings).WithArgs(SectionName, stored, "admin@example.com").WillReturnResult(sqlmock.NewResult(1, 1))
	expectSettings(mock, want.withDefaults())
	got, err := svc.SaveSettings(ctx, want, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Value.Enabled || got.Value.SessionLimit != 3 || got.Value.PersonaDailyLimit != DefaultPersonaDailyLimit || got.UpdatedBy != "admin@example.com" {
		t.Errorf("saved = %+v", got)
	}

	mock.ExpectExec(qSaveSettings).WillReturnError(errDown)
	if _, err := svc.SaveSettings(ctx, Settings{}, "admin@example.com"); err == nil {
		t.Error("a failed write was reported saved")
	}
	met(t, mock)

	unstored := New(Deps{Channels: allChannels})
	if _, err := unstored.SaveSettings(ctx, Settings{}, "a"); err == nil || !strings.Contains(err.Error(), "not stored") {
		t.Errorf("a deployment with no section saved: %v", err)
	}
	noChannels := New(Deps{})
	if _, err := noChannels.SaveSettings(ctx, Settings{Channels: []string{"ops"}}, "a"); !errors.As(err, &inv) {
		t.Errorf("a channel was accepted with no channel store: %v", err)
	}
}

// TestSend_NoChannelStoreSendsNothing: a deployment without notifications
// stores the post and sends it nowhere.
func TestSend_NoChannelStoreSendsNothing(t *testing.T) {
	if n := New(Deps{}).send(context.Background(), sending{channels: []string{"ops"}, key: "k", id: "pfb_1", post: validPost()}); n != 0 {
		t.Errorf("sent = %d", n)
	}
}

// TestEmbed_NoEmbedder: with no embedder a post folds only on its words.
func TestEmbed_NoEmbedder(t *testing.T) {
	if v := New(Deps{}).embed(context.Background(), "x"); v != nil {
		t.Errorf("embedding = %v", v)
	}
}
