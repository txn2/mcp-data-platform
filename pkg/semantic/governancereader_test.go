package semantic

import (
	"context"
	"testing"
)

// governanceProvider embeds the picker stub and adds the two reads that complete
// the governance capability (tag search and the by-URN term read), standing in
// for the real DataHub adapter. SearchTables comes from the embedded Provider.
type governanceProvider struct {
	*pickerProvider
	tags []EntityRef
	term *GlossaryTerm
}

func (p *governanceProvider) SearchTags(context.Context, string, int) ([]EntityRef, error) {
	return p.tags, nil
}

func (p *governanceProvider) GetGlossaryTerm(context.Context, string) (*GlossaryTerm, error) {
	return p.term, nil
}

func newGovernanceProvider() *governanceProvider {
	return &governanceProvider{
		pickerProvider: &pickerProvider{Provider: NewNoopProvider()},
		tags:           []EntityRef{{URN: "urn:li:tag:pii", Name: "pii"}},
		term:           &GlossaryTerm{URN: "urn:li:glossaryTerm:8f3c", Name: "Net Revenue"},
	}
}

func TestGovernanceReaderFrom(t *testing.T) {
	t.Run("bare governance provider", func(t *testing.T) {
		reader, ok := GovernanceReaderFrom(newGovernanceProvider())
		if !ok {
			t.Fatal("expected governance capability")
		}
		tags, _ := reader.SearchTags(context.Background(), "", 10)
		if len(tags) != 1 || tags[0].Name != "pii" {
			t.Fatalf("unexpected tags %v", tags)
		}
	})

	t.Run("through caching decorator", func(t *testing.T) {
		// CachedProvider forwards GetGlossaryTerm and SearchTables but none of the
		// vocabulary lists, so a decorated provider would satisfy the interface
		// while its tag and domain reads went nowhere: the probe must reach the
		// inner provider, as it does for the picker.
		cached := NewCachedProvider(newGovernanceProvider(), CacheConfig{})
		reader, ok := GovernanceReaderFrom(cached)
		if !ok {
			t.Fatal("expected governance capability through cache decorator")
		}
		term, _ := reader.GetGlossaryTerm(context.Background(), "urn:li:glossaryTerm:8f3c")
		if term == nil || term.Name != "Net Revenue" {
			t.Fatalf("unexpected term %v", term)
		}
	})

	t.Run("picker alone is not a governance reader", func(t *testing.T) {
		// A provider that can list domains and terms still cannot read a tag or a
		// term by URN, so it registers no governance source rather than a partial
		// one that reports every tag missing.
		if _, ok := GovernanceReaderFrom(&pickerProvider{Provider: NewNoopProvider()}); ok {
			t.Fatal("a picker-only provider must not report the governance capability")
		}
	})

	t.Run("noop provider", func(t *testing.T) {
		if _, ok := GovernanceReaderFrom(NewNoopProvider()); ok {
			t.Fatal("noop provider must not report the governance capability")
		}
	})
}
