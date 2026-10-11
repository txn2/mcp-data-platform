package notifypost

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// Bulletin composes a daily channel's batch into the one document it posts: a
// line per document with its title, its first paragraph and its link, so a
// day's findings read as one summary rather than as a burst of messages.
//
// Attachments are not carried. A bulletin is a summary, and each item's link
// is where its files are; carrying five files from each of a day's documents
// would make the bulletin the burst it exists to replace.
func Bulletin(ch notification.Channel, ds []notification.Delivery) notification.Delivery {
	if len(ds) == 0 {
		return notification.Delivery{}
	}
	var b strings.Builder
	for i, d := range ds {
		if i > 0 {
			_, _ = b.WriteString("\n\n")
		}
		_, _ = b.WriteString("**" + oneLine(d.Title) + "**")
		if note := d.SuppressionNote(); note != "" {
			_, _ = b.WriteString("\n" + note)
		}
		if para := firstParagraph(d.Body); para != "" {
			_, _ = b.WriteString("\n" + para)
		}
		if d.Link != "" {
			_, _ = b.WriteString("\n" + d.Link)
		}
	}
	noun := "updates"
	if len(ds) == 1 {
		noun = "update"
	}
	return notification.Delivery{
		ID:         ds[0].ID,
		OccurredAt: ds[0].OccurredAt,
		Document: notification.Document{
			Title: fmt.Sprintf("%s: %d %s today", ch.Name, len(ds), noun),
			Body:  b.String(),
			Type:  ds[0].Type,
		},
	}
}

// firstParagraph returns a markdown body's first paragraph.
func firstParagraph(body string) string {
	head, _, _ := strings.Cut(strings.TrimSpace(body), "\n\n")
	return strings.TrimSpace(head)
}
