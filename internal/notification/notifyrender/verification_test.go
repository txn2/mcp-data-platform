package notifyrender

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// The reminder that a Verified badge expires (#2503) names the asset in its
// subject and carries the expiry sentence as its body, not as a quotation.
func TestRender_VerificationExpiring(t *testing.T) {
	n := notification.Notification{Recipient: "owner@b.io", Payload: notification.Payload{
		Kind: notification.KindVerificationExpiring, ItemID: "a1", ItemTitle: "Q3 revenue",
		Message: "The Verified badge on \"Q3 revenue\" expires on 2026-12-31. Verify it again to keep the badge.",
		Link:    "https://x.io/portal/assets/a1",
	}}
	assert.Equal(t, `The verification of "Q3 revenue" expires soon`, Subject(n))
	item := buildItem(n)
	assert.Contains(t, item.Body, "expires on 2026-12-31")
	assert.Empty(t, item.Message)
	assert.Equal(t, "Open the asset", item.LinkText)

	n.Payload.Link = ""
	assert.Empty(t, buildItem(n).LinkText, "no link, no link text")

	email, err := testRenderer(t).Render([]notification.Notification{n})
	assert.NoError(t, err)
	assert.Contains(t, email.Subject, "Q3 revenue")
}
