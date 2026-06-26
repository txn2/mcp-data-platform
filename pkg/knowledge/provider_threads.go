package knowledge

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// SourceFeedback is the provenance label for feedback-thread hits.
const SourceFeedback = "feedback"

// ThreadSearcher is the lexical feedback-thread search the provider needs. The
// postgres thread store implements it; declared here so the provider depends on
// the capability and tests can supply a fake.
type ThreadSearcher interface {
	SearchThreads(ctx context.Context, ownerEmail, intent string, limit int) ([]threads.Thread, error)
}

// ThreadsProvider federates a caller's feedback threads into unified search, so
// feedback they have given is discoverable knowledge alongside the catalog,
// insights, and pages. It closes the gap where feedback was the one knowledge
// source not in the search corpus (#686): the loop now reads feedback, not only
// writes it. Threads carry no embedding, so this is a lexical source; it is
// per-user (author email) and fails closed on an empty caller email, never
// surfacing another user's feedback.
type ThreadsProvider struct {
	store ThreadSearcher
}

// NewThreadsProvider builds the feedback provider over a thread searcher.
func NewThreadsProvider(store ThreadSearcher) *ThreadsProvider {
	return &ThreadsProvider{store: store}
}

// Name returns the provenance label.
func (*ThreadsProvider) Name() string { return SourceFeedback }

// Scope marks this provider per-user: a caller searches only their own feedback,
// because the thread store has no organization-wide visibility model and a
// shared scope would leak every user's feedback to every searcher.
func (*ThreadsProvider) Scope() Scope { return ScopePerUser }

// Search returns the caller's feedback threads relevant to the intent. It fails
// closed on a missing caller email rather than searching across all users, and a
// query with no intent yields nothing (feedback has no entity-keyed path).
func (p *ThreadsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Caller.Email == "" || q.Intent == "" {
		return nil, nil
	}
	found, err := p.store.SearchThreads(ctx, q.Caller.Email, q.Intent, q.Limit)
	if err != nil {
		return nil, fmt.Errorf("feedback search: %w", err)
	}
	hits := make([]Hit, 0, len(found))
	for i := range found {
		hits = append(hits, threadHit(found[i], positionalScore(i, len(found))))
	}
	return hits, nil
}

// threadHit maps a feedback thread to a knowledge hit, carrying its open/resolved
// status and author as provenance.
func threadHit(t threads.Thread, score float64) Hit {
	return Hit{
		Text:       threadHitText(t),
		Source:     SourceFeedback,
		Ref:        t.ID,
		Score:      score,
		Status:     t.Status,
		CapturedBy: t.AuthorEmail,
	}
}

// threadHitText renders a feedback thread as a knowledge snippet: its title (or
// kind when untitled) and the kind of entity it comments on.
func threadHitText(t threads.Thread) string {
	title := strings.TrimSpace(t.Title)
	if title == "" {
		title = t.Kind + " feedback"
	}
	if t.TargetType != "" {
		return title + "\nfeedback on " + t.TargetType
	}
	return title
}
