import type { ToolInfo } from "@/api/admin/types";

// Pure allow/deny resolution logic for the persona editor's live preview.
// Extracted from PersonaEditor.tsx (#766) so the wildcard matcher and the
// deny-by-default decision engine can be unit-tested (and re-used by the
// rule/trace facets) without importing the React component tree.

// ---------------------------------------------------------------------------
// Pattern matching mirrors backend semantics in pkg/persona/filter.go:
//   1. Deny rules checked first  → match means DENIED
//   2. Allow rules checked second → match means ALLOWED
//   3. No match → DENIED (default)
// ---------------------------------------------------------------------------

// Port of Go's filepath.Match. The backend matches every persona pattern with
// it (pkg/persona/filter.go), so the live preview must apply the same wildcard
// semantics: `?` is a single character, `[abc]` / `[a-z]` / `[^abc]` are
// character classes, and `\c` escapes a literal c.
//
// `*` and `?` do not cross a `/`, which is Go's rule and not JavaScript's. It
// makes no difference to a tool or a connection name, neither of which
// contains a separator, and all the difference to an API route path: a rule
// written `/v1/orders/*` governs `/v1/orders/42` and not `/v1/orders/42/items`,
// and a preview that disagreed would show an operation as governed by a rule
// the server leaves it untouched by. `**` is not a second syntax — it is two
// stars, and it stops at a separator like one.
//
// A character class is left alone: Go does match a separator inside one.
export function matchPattern(pattern: string, name: string): boolean {
  if (!pattern) return false;
  if (pattern === "*") return !name.includes("/");

  let regex = "";
  let i = 0;
  while (i < pattern.length) {
    const c = pattern[i];
    if (c === "*") {
      regex += "[^/]*";
      i++;
    } else if (c === "?") {
      regex += "[^/]";
      i++;
    } else if (c === "[") {
      const end = pattern.indexOf("]", i + 1);
      if (end === -1) {
        // Malformed: Go returns ErrBadPattern; mirror that as no match.
        return false;
      }
      regex += pattern.substring(i, end + 1);
      i = end + 1;
    } else if (c === "\\" && i + 1 < pattern.length) {
      // Escape the escaped char as a regex literal. The class includes
      // `*` and `?` because filepath.Match treats `\*` / `\?` as literal
      // matches, and JS regex needs them backslash-escaped to be literal.
      regex += (pattern[i + 1] ?? "").replace(/[.+*?^${}()|[\]\\]/g, "\\$&");
      i += 2;
    } else if (c !== undefined) {
      regex += c.replace(/[.+*?^${}()|[\]\\]/g, "\\$&");
      i++;
    }
  }

  try {
    return new RegExp("^" + regex + "$").test(name);
  } catch {
    return false;
  }
}

export type Decision = "allow" | "deny" | "default-deny";

export interface TraceStep {
  bucket: "deny" | "allow";
  pattern: string;
  matched: boolean;
  decisive: boolean;
}

export interface Resolution {
  decision: Decision;
  matchedPattern: string;
  steps: TraceStep[];
}

// resolve mirrors pkg/persona/filter.go: both tools (evaluateToolAccess) and
// connections (IsConnectionAllowed) are deny-by-default. Deny patterns take
// precedence; otherwise an explicit allow match is required, and anything
// unmatched falls through to default-deny. There is no "empty allow means
// allow all" shortcut on either axis.
export function resolve(
  name: string,
  allow: string[],
  deny: string[],
): Resolution {
  const steps: TraceStep[] = [];
  let decision: Decision = "default-deny";
  let matchedPattern = "";
  let decisiveIdx = -1;

  for (const p of deny) {
    const matched = matchPattern(p, name);
    steps.push({ bucket: "deny", pattern: p, matched, decisive: false });
    if (matched && decisiveIdx === -1) {
      decision = "deny";
      matchedPattern = p;
      decisiveIdx = steps.length - 1;
    }
  }

  for (const p of allow) {
    const matched = matchPattern(p, name);
    steps.push({ bucket: "allow", pattern: p, matched, decisive: false });
    if (matched && decisiveIdx === -1) {
      decision = "allow";
      matchedPattern = p;
      decisiveIdx = steps.length - 1;
    }
  }

  if (decisiveIdx >= 0) steps[decisiveIdx]!.decisive = true;

  return { decision, matchedPattern, steps };
}

export interface UniqueTool {
  name: string;
  title?: string;
  kinds: string[];
  connections: string[];
  primaryKind: string;
}

export function aggregateTools(tools: ToolInfo[] | undefined): UniqueTool[] {
  if (!tools) return [];
  const map = new Map<string, UniqueTool>();
  for (const t of tools) {
    const existing = map.get(t.name);
    if (existing) {
      if (!existing.kinds.includes(t.kind)) existing.kinds.push(t.kind);
      if (!existing.connections.includes(t.connection))
        existing.connections.push(t.connection);
    } else {
      map.set(t.name, {
        name: t.name,
        title: t.title,
        kinds: [t.kind],
        connections: [t.connection],
        primaryKind: t.kind,
      });
    }
  }
  return Array.from(map.values()).sort((a, b) => a.name.localeCompare(b.name));
}
