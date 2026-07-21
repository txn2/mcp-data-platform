package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
}

func TestRenderIncludesUnsubscribeFooter(t *testing.T) {
	r, err := NewRenderer(Branding{Name: "ACME Data", BaseURL: "https://platform.example.com"})
	require.NoError(t, err)
	r.SetUnsubscribeURLFn(func(email string) string {
		return "https://platform.example.com/portal/notifications/unsubscribe?tok=TOK-" + email
	})

	email, err := r.Render([]Notification{{
		Recipient: "bob@example.com",
		Category:  CategoryShare,
		CreatedAt: time.Now(),
		Payload: Payload{
			Kind: KindAsset, ItemID: "a1", ItemTitle: "Report",
			Actor: "alice@example.com", Link: "https://platform.example.com/portal/view/tok1",
		},
	}})
	require.NoError(t, err)

	for _, body := range []string{email.HTML, email.Text} {
		assert.Contains(t, body, "/portal/notifications/unsubscribe?tok=TOK-bob@example.com")
	}
}

func TestRenderOmitsUnsubscribeFooterWithoutBuilder(t *testing.T) {
	r, err := NewRenderer(Branding{Name: "ACME Data"})
	require.NoError(t, err)

	email, err := r.Render([]Notification{{
		Recipient: "bob@example.com",
		Category:  CategoryShare,
		Payload:   Payload{Kind: KindAsset, ItemTitle: "Report", Actor: "alice@example.com"},
	}})
	require.NoError(t, err)
	assert.NotContains(t, email.HTML, "unsubscribe")
	assert.NotContains(t, email.Text, "Unsubscribe")
}
