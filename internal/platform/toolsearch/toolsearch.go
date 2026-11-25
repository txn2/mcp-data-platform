// Package toolsearch ranks the platform's own tools by what a caller says it
// wants to do, for search's tools source (#2809).
//
// It is what platform_find_tools did, without the separate tool: agents call
// search first and stop there, so in three production deployments the
// find-tools tool was reached 23 times against 2,701 searches, and every query
// it did get was a capability question ("publish a static site", "assets
// shared with me") that search could not answer. Ranked here, those questions
// are answered where agents already ask.
//
// The ranking is the tools index's embedding similarity when an embedder and
// the index are both available, and otherwise a lexical match of the intent's
// words against each tool's name and description. Either way it is filtered by
// the same persona predicate tools/list applies, so a caller is never shown a
// tool it cannot call.
package toolsearch

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/toolhelp"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// Ranker is what ranking reads, all late-bound: the server's tool list does not
// exist when search is assembled.
type Ranker struct {
	// Enumerate lists the deployment's tools, as the tools index sees them.
	Enumerate func(ctx context.Context) ([]*mcp.Tool, error)
	// Embedder embeds the intent; nil or unconfigured ranks lexically.
	Embedder func() embedding.Provider
	// Index is the tools index; nil ranks lexically.
	Index func() *toolsindex.Store
	// Allowed reports whether a caller with roles may call a tool; nil
	// allows every tool. With no roles every tool is allowed, as tools/list
	// does for a caller no persona resolved.
	Allowed func(ctx context.Context, roles []string, tool string) bool
	// Exclude is the tools never returned: search itself.
	Exclude []string
}

// Match is one ranked tool, with an example call fitting the intent: the
// command whose words best match it, and every parameter that command requires.
type Match = knowledge.ToolMatch

// RankTools returns up to limit tools the caller may call, best first. It is
// search's knowledge.ToolRanker.
func (r *Ranker) RankTools(ctx context.Context, intent string, roles []string, limit int) []Match {
	intent = strings.TrimSpace(intent)
	if r == nil || r.Enumerate == nil || intent == "" || limit <= 0 {
		return nil
	}
	byName := r.callable(ctx, roles)
	if byName == nil {
		return nil
	}
	scored := r.semantic(ctx, intent, byName)
	if scored == nil {
		scored = lexical(intent, byName)
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}
	for i := range scored {
		scored[i].Example = toolhelp.Example(schemaOf(byName[scored[i].Name]), intent)
	}
	return scored
}

// callable is the tools the caller may call, by name, search excluded; nil
// when they cannot be listed.
func (r *Ranker) callable(ctx context.Context, roles []string) map[string]*mcp.Tool {
	tools, err := r.Enumerate(ctx)
	if err != nil {
		return nil
	}
	byName := make(map[string]*mcp.Tool, len(tools))
	for _, t := range tools {
		if !slices.Contains(r.Exclude, t.Name) && r.allowed(ctx, roles, t.Name) {
			byName[t.Name] = t
		}
	}
	return byName
}

// intentVector is the intent's embedding and the index to rank it in, or
// false when either is not available.
func (r *Ranker) intentVector(ctx context.Context, intent string) ([]float32, *toolsindex.Store, bool) {
	if r.Embedder == nil || r.Index == nil {
		return nil, nil, false
	}
	emb, idx := r.Embedder(), r.Index()
	if !embedding.IsConfigured(emb) || idx == nil {
		return nil, nil, false
	}
	vec, err := emb.Embed(ctx, intent)
	if err != nil || zero(vec) {
		return nil, nil, false
	}
	return vec, idx, true
}

// semantic ranks by the tools index, or nil when it is not available.
func (r *Ranker) semantic(ctx context.Context, intent string, byName map[string]*mcp.Tool) []Match {
	vec, idx, ok := r.intentVector(ctx, intent)
	if !ok {
		return nil
	}
	ranked, err := idx.RankBySimilarity(ctx, toolsindex.SourceID, vec)
	if err != nil || len(ranked) == 0 {
		return nil
	}
	out := make([]Match, 0, len(ranked))
	for _, s := range ranked {
		if t, ok := byName[s.ToolName]; ok {
			out = append(out, Match{Name: t.Name, Description: t.Description, Score: s.Score})
		}
	}
	return out
}

// lexical ranks by how many of the intent's words a tool's name and
// description hold, the name counting twice; a tool holding none is left out.
func lexical(intent string, byName map[string]*mcp.Tool) []Match {
	words := strings.Fields(strings.ToLower(intent))
	var out []Match
	for name, t := range byName {
		if score := lexicalScore(words, name, t.Description); score > 0 {
			out = append(out, Match{Name: name, Description: t.Description, Score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// minWordLen is the shortest intent word the lexical ranking reads; shorter
// ones ("a", "to") match everything.
const minWordLen = 3

// nameWeight is how much more a word in a tool's name counts than one in its
// description.
const nameWeight = 2

// lexicalScore counts the intent's words a tool's name and description hold.
func lexicalScore(words []string, name, description string) float64 {
	lname, ldesc := strings.ToLower(name), strings.ToLower(description)
	var score float64
	for _, w := range words {
		if len(w) < minWordLen {
			continue
		}
		if strings.Contains(lname, w) {
			score += nameWeight
		}
		if strings.Contains(ldesc, w) {
			score++
		}
	}
	return score
}

func (r *Ranker) allowed(ctx context.Context, roles []string, name string) bool {
	if r.Allowed == nil || len(roles) == 0 {
		return true
	}
	return r.Allowed(ctx, roles, name)
}

// schemaOf is a tool's input schema as decoded JSON.
func schemaOf(t *mcp.Tool) map[string]any {
	if t == nil || t.InputSchema == nil {
		return nil
	}
	if m, ok := t.InputSchema.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(t.InputSchema)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// zero reports an all-zero vector, which an unconfigured embedder returns.
func zero(v []float32) bool {
	for _, f := range v {
		if f != 0 {
			return false
		}
	}
	return true
}
