package notifysend

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyrender"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/notification/smtp"
)

func TestBuildMessage_AttachesEachFile(t *testing.T) {
	email := notifyrender.Email{
		To: "a@b.io", Subject: "Weekly", Text: "t", HTML: "<p>h</p>",
		Attachments: []notification.File{
			{Name: "chart.png", ContentType: "image/png", Data: []byte("PNGDATA")},
			{Name: "../raw.csv", Data: []byte("a,b")},
		},
	}
	msg, err := buildMessage(smtp.Settings{From: "p@example.com"}, email)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var out strings.Builder
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	raw := out.String()
	for _, want := range []string{"multipart/mixed", `filename="chart.png"`, "image/png", `filename=".._raw.csv"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("message missing %q", want)
		}
	}
}

func TestAttachmentName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"", "attachment"}, {" a/b.txt ", "a_b.txt"}, {`c\d`, "c_d"}} {
		in, want := tc.in, tc.want
		if got := attachmentName(in); got != want {
			t.Errorf("attachmentName(%q) = %q, want %q", in, got, want)
		}
	}
}
