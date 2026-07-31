package notifyrender

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func TestRenderGuestLink(t *testing.T) {
	r, err := NewRenderer(Branding{Name: "ACME Data", BaseURL: "https://platform.example.com"})
	require.NoError(t, err)

	link := "https://platform.example.com/portal/view/tok1/guest?otk=abc123"
	email, err := r.RenderGuestLink("bob@example.com", link)
	require.NoError(t, err)

	assert.Equal(t, "bob@example.com", email.To)
	assert.Equal(t, "ACME Data: your one-time view link", email.Subject)
	for _, body := range []string{email.HTML, email.Text} {
		assert.Contains(t, body, link)
		assert.Contains(t, body, "expires in 15 minutes")
	}
	assert.Contains(t, email.HTML, "Open the shared item", "the guest link button carries its own label")
	assert.NotContains(t, email.HTML, "Unsubscribe", "a transactional send carries no unsubscribe footer")
	assert.Empty(t, email.UnsubURL, "a transactional send must not trigger List-Unsubscribe headers")
}

func TestRenderIncludesUnsubscribeFooter(t *testing.T) {
	r, err := NewRenderer(Branding{Name: "ACME Data", BaseURL: "https://platform.example.com"})
	require.NoError(t, err)
	r.SetUnsubscribeURLFn(func(email string) string {
		return "https://platform.example.com/portal/notifications/unsubscribe?tok=TOK-" + email
	})

	email, err := r.Render([]notification.Notification{{
		Recipient: "bob@example.com",
		Category:  notification.CategoryShare,
		CreatedAt: time.Now(),
		Payload: notification.Payload{
			Kind: notification.KindAsset, ItemID: "a1", ItemTitle: "Report",
			Actor: "alice@example.com", Link: "https://platform.example.com/portal/view/tok1",
		},
	}})
	require.NoError(t, err)

	for _, body := range []string{email.HTML, email.Text} {
		assert.Contains(t, body, "/portal/notifications/unsubscribe?tok=TOK-bob@example.com")
	}
	assert.Equal(t, "https://platform.example.com/portal/notifications/unsubscribe?tok=TOK-bob@example.com",
		email.UnsubURL, "the sender needs the link to emit List-Unsubscribe headers")
}

func TestRenderOmitsUnsubscribeFooterWithoutBuilder(t *testing.T) {
	r, err := NewRenderer(Branding{Name: "ACME Data"})
	require.NoError(t, err)

	email, err := r.Render([]notification.Notification{{
		Recipient: "bob@example.com",
		Category:  notification.CategoryShare,
		Payload:   notification.Payload{Kind: notification.KindAsset, ItemTitle: "Report", Actor: "alice@example.com"},
	}})
	require.NoError(t, err)
	assert.NotContains(t, email.HTML, "unsubscribe")
	assert.NotContains(t, email.Text, "Unsubscribe")
	assert.Empty(t, email.UnsubURL)
}
