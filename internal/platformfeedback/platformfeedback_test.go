package platformfeedback

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/secretscan"
)

const plantedSecret = "zq8-Planted-Secret-Value"

func validPost() Post {
	return Post{
		TryingTo: "list the assets shared with me", BlockedBy: "manage_asset has no filter for shared items",
		Tool: "manage_asset", Calls: []string{"mcp:call:abc123"},
	}
}

// service is a Service over sqlmock whose stored settings are set.
func service(t *testing.T, set Settings) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := New(Deps{
		DB:             db,
		Section:        settingsjson.New[Settings](db, SectionName),
		Detector:       secretscan.Detector{Known: func(context.Context) ([]string, error) { return []string{plantedSecret}, nil }},
		ServiceAccount: func(p string) bool { return p == "ingest" },
	})
	raw, _ := json.Marshal(set)
	mock.ExpectQuery("FROM platform_settings").WillReturnRows(
		sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow(raw, "admin@example.com", time.Now()))
	return svc, mock
}

func TestSubmit_Refusals(t *testing.T) {
	on := Settings{Enabled: true}.withDefaults()
	cases := []struct {
		name   string
		set    Settings
		caller Caller
		post   Post
		code   string
		reason string
	}{
		{"off", Settings{}, Caller{Persona: "analyst"}, validPost(), CodeOff, ""},
		{"service account", on, Caller{Persona: "ingest"}, validPost(), CodePersona, reasonPersona},
		{"persona not named", Settings{Enabled: true, Personas: []string{"admin"}}.withDefaults(), Caller{Persona: "analyst"}, validPost(), CodePersona, reasonPersona},
		{"missing field", on, Caller{Persona: "analyst"}, Post{TryingTo: "x"}, CodeInvalid, reasonInvalid},
		{"data in calls", on, Caller{Persona: "analyst"}, Post{TryingTo: "x", BlockedBy: "y", Calls: []string{"the rows were 1,2,3"}}, CodeInvalid, reasonInvalid},
		{"too long", on, Caller{Persona: "analyst"}, Post{TryingTo: strings.Repeat("a", maxTryingTo+1), BlockedBy: "y"}, CodeInvalid, reasonInvalid},
		{"planted secret", on, Caller{Persona: "analyst"}, Post{TryingTo: "x", BlockedBy: "failed with " + plantedSecret}, CodeSecret, reasonSecret},
		{"pem key", on, Caller{Persona: "analyst"}, Post{TryingTo: "x", BlockedBy: "y", Suggestion: "-----BEGIN PRIVATE KEY-----"}, CodeSecret, reasonSecret},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, mock := service(t, tc.set)
			if tc.reason != "" {
				mock.ExpectExec("INSERT INTO platform_feedback_refusals").WithArgs(tc.reason, tc.caller.Persona).
					WillReturnResult(sqlmock.NewResult(1, 1))
			}
			_, err := svc.Submit(context.Background(), tc.caller, tc.post)
			var r *RefusedError
			if !errors.As(err, &r) || r.Code != tc.code {
				t.Fatalf("err = %v, want code %s", err, tc.code)
			}
			if strings.Contains(r.Msg, plantedSecret) || strings.Contains(r.Msg, "BEGIN PRIVATE") {
				t.Errorf("the refusal repeats the secret: %s", r.Msg)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestSubmit_Limits(t *testing.T) {
	on := Settings{Enabled: true, SessionLimit: 2, PersonaDailyLimit: 3}
	for name, counts := range map[string][2]int{"session": {2, 0}, "persona": {0, 3}} {
		t.Run(name, func(t *testing.T) {
			svc, mock := service(t, on)
			mock.ExpectQuery("FROM platform_feedback_reports").
				WillReturnRows(sqlmock.NewRows([]string{"s", "p"}).AddRow(counts[0], counts[1]))
			mock.ExpectExec("INSERT INTO platform_feedback_refusals").WillReturnResult(sqlmock.NewResult(1, 1))
			_, err := svc.Submit(context.Background(), Caller{Persona: "analyst", SessionID: "dps_x"}, validPost())
			var r *RefusedError
			if !errors.As(err, &r) || r.Code != CodeLimit || !strings.Contains(r.Msg, "limit") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestSettingsValidate(t *testing.T) {
	known := func(s string) bool { return s == "ok" }
	for name, s := range map[string]Settings{
		"session limit": {SessionLimit: 1000}, "persona limit": {PersonaDailyLimit: -1},
		"persona": {Personas: []string{"nope"}}, "channel": {Channels: []string{"nope"}},
	} {
		var inv *InvalidError
		if err := s.validate(known, known); !errors.As(err, &inv) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := (Settings{Personas: []string{"ok"}, Channels: []string{"ok"}}).validate(known, known); err != nil {
		t.Errorf("valid settings refused: %v", err)
	}
	d := Settings{}.withDefaults()
	if d.SessionLimit != DefaultSessionLimit || d.PersonaDailyLimit != DefaultPersonaDailyLimit || d.Channels == nil || d.Personas == nil {
		t.Errorf("defaults = %+v", d)
	}
}

func TestDocumentCarriesFieldsAndNoResultData(t *testing.T) {
	svc := New(Deps{PortalURL: "https://portal.example.com/"})
	doc := svc.document("pfb_1", validPost(), Caller{Persona: "analyst"})
	if doc.Type != "platform.feedback" || doc.Key != keyPrefix+"pfb_1" || doc.Link != "https://portal.example.com/portal/admin/platform-feedback/pfb_1" {
		t.Errorf("doc = %+v", doc)
	}
	for _, want := range []string{"list the assets shared with me", "mcp:call:abc123", `"persona":"analyst"`} {
		if !strings.Contains(doc.Body+string(doc.Data), want) {
			t.Errorf("document lacks %q", want)
		}
	}
	if !validStatus(StatusPlanned) || validStatus("bogus") {
		t.Error("validStatus misread")
	}
	long := svc.document("x", Post{TryingTo: strings.Repeat("a", 200), BlockedBy: "b"}, Caller{})
	if !strings.HasSuffix(long.Title, "...") {
		t.Errorf("title not bounded: %q", long.Title)
	}
}

func TestSameWords(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Export the weekly report", "export the weekly report.", true},
		{"export  the\nweekly, report", "Export the weekly report", true},
		{"export the weekly report", "export the monthly report", false},
		{"report weekly the export", "export the weekly report", false},
	} {
		if got := sameWords(c.a, c.b); got != c.want {
			t.Errorf("sameWords(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
