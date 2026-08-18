package notification

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/auth"
)

func TestNormalizeAddress(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare address", "user@example.com", "user@example.com"},
		{"case folded", "User@Example.COM", "user@example.com"},
		{"surrounding space", "  user@example.com  ", "user@example.com"},
		{"display name", "Display Name <User@Example.com>", "user@example.com"},
		{"quoted display name", `"Doe, Jane" <jane@example.com>`, "jane@example.com"},
		{"angle brackets only", "<user@example.com>", "user@example.com"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"unparseable falls back to lowered", "Not An Address", "not an address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeAddress(tt.in); got != tt.want {
				t.Errorf("NormalizeAddress(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The address shapes a store may hold must all resolve to the one person, so
// an owner recorded as "Display Name <addr>" is still recognized as the actor
// and is not also emitted a second time alongside their bare address (#1100).
func TestRecipientsExcluding_NormalizesBothSides(t *testing.T) {
	tests := []struct {
		name       string
		actor      string
		candidates []string
		want       []string
	}{
		{
			name:       "display-form candidate matches bare actor",
			actor:      "me@example.com",
			candidates: []string{"Me Myself <me@example.com>", "other@example.com"},
			want:       []string{"other@example.com"},
		},
		{
			name:       "display-form actor matches bare candidate",
			actor:      "Me Myself <me@example.com>",
			candidates: []string{"me@example.com", "other@example.com"},
			want:       []string{"other@example.com"},
		},
		{
			name:       "same person in two shapes yields one recipient",
			actor:      "actor@example.com",
			candidates: []string{"Owner <owner@example.com>", "owner@example.com"},
			want:       []string{"owner@example.com"},
		},
		{
			name:       "empty actor still drops empty candidates only",
			actor:      "",
			candidates: []string{"", "owner@example.com"},
			want:       []string{"owner@example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RecipientsExcluding(tt.actor, tt.candidates...)
			if len(got) != len(tt.want) {
				t.Fatalf("RecipientsExcluding() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("RecipientsExcluding() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// The enqueue seam is the single place every trigger passes through, so the
// self-drop must hold there for every address shape rather than depending on
// each caller having filtered first (#1100).
func TestEnqueue_DropsActorInEveryAddressShape(t *testing.T) {
	tests := []struct {
		name      string
		recipient string
		actor     string
	}{
		{"identical", "me@example.com", "me@example.com"},
		{"recipient in display form", "Me Myself <me@example.com>", "me@example.com"},
		{"actor in display form", "me@example.com", "Me Myself <me@example.com>"},
		{"both in display form, different names", "Owner <me@example.com>", "Me <ME@Example.com>"},
		{"recipient padded and mixed case", "  ME@Example.com ", "me@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := &fakeQueueStore{}
			enq := NewEnqueuer(&fakePrefsStore{}, queue, 13)
			defer enq.Close()

			queued, err := enq.Notify(context.Background(), tt.recipient, CategoryComment,
				Payload{Kind: KindComment, Actor: tt.actor})
			if err != nil {
				t.Fatalf("Notify: %v", err)
			}
			if queued || len(queue.enqueued) != 0 {
				t.Fatalf("the actor must never be queued their own event, got %+v", queue.enqueued)
			}
		})
	}
}

// Narrowing by the actor must not narrow by anyone else: a different person
// whose address is stored in display form still gets their notification, with
// the row keyed by the bare address the preference store is keyed by.
func TestEnqueue_DisplayFormRecipientIsQueuedNormalized(t *testing.T) {
	queue := &fakeQueueStore{}
	enq := NewEnqueuer(&fakePrefsStore{}, queue, 13)
	defer enq.Close()

	queued, err := enq.Notify(context.Background(), "Team Mate <TeamMate@Example.com>", CategoryComment,
		Payload{Kind: KindComment, Actor: "me@example.com"})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !queued || len(queue.enqueued) != 1 {
		t.Fatalf("expected one queued row, got %+v", queue.enqueued)
	}
	if queue.enqueued[0].Recipient != "teammate@example.com" {
		t.Errorf("Recipient = %q, want the normalized bare address", queue.enqueued[0].Recipient)
	}
}

func TestDeliverable(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "ordinary address", in: "user@example.com", want: true},
		{name: "empty", in: "", want: false},
		{name: "whitespace only", in: "   ", want: false},
		{name: "synthetic api-key address", in: "ci@apikey.local", want: false},
		{name: "synthetic address in display form", in: "CI Runner <ci@apikey.local>", want: false},
		{name: "synthetic address with mixed case", in: "CI@APIKey.Local", want: false},
		// The check is on the domain, not on a substring: a real domain that
		// merely contains the synthetic one is a real mailbox.
		{name: "domain that only ends similarly", in: "user@notapikey.local", want: true},
		{name: "synthetic name as a subdomain of a real domain", in: "user@apikey.local.example.com", want: true},
		{name: "synthetic domain as a local part", in: "apikey.local@example.com", want: true},
		// A value that does not parse is compared as-is by NormalizeAddress; it
		// is not this predicate's job to reject it.
		{name: "unparseable value", in: "not an address", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Deliverable(tc.in); got != tc.want {
				t.Errorf("Deliverable(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// The synthetic domain is spelled in two packages: pkg/auth mints addresses in
// it, pkg/notification refuses to deliver to it. pkg/notification deliberately
// does not import pkg/auth at runtime -- it is the lower-level domain and must
// not depend on the auth layer for a string -- so this test is what keeps the
// two copies equal. If it fails, the platform is minting addresses in a domain
// the mail path no longer recognizes, and undeliverable rows are queued again.
func TestSyntheticDomainMatchesTheOneAuthMints(t *testing.T) {
	if syntheticAPIKeyDomain != auth.SyntheticEmailDomain {
		t.Fatalf("synthetic domain drifted: notification has %q, auth mints %q",
			syntheticAPIKeyDomain, auth.SyntheticEmailDomain)
	}
	// And the address auth actually builds must be one this package refuses,
	// which is the property that matters rather than the constants alone.
	if Deliverable(auth.SyntheticEmail("ci-pipeline")) {
		t.Errorf("Deliverable(%q) = true; the address auth mints for a keyless "+
			"API key must never be treated as a mailbox", auth.SyntheticEmail("ci-pipeline"))
	}
}
