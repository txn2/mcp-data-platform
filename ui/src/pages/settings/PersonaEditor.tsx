import { useState, useMemo, useCallback } from "react";
import {
  Plus,
  X,
  Search,
  Check,
  Ban,
  ChevronDown,
  ChevronRight,
  Save,
  AlertCircle,
  Info,
  Trash2,
} from "lucide-react";
import {
  useTools,
  useConnections,
  useCreatePersona,
  useUpdatePersona,
} from "@/api/admin/hooks";
import type { ToolInfo, ConnectionInfo } from "@/api/admin/types";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { cn } from "@/lib/utils";

// ---------------------------------------------------------------------------
// Draft shape (mirrors PersonasPanel.PersonaDraft)
// ---------------------------------------------------------------------------

export interface PersonaDraft {
  name: string;
  displayName: string;
  description: string;
  roles: string[];
  allowTools: string[];
  denyTools: string[];
  allowConnections: string[];
  denyConnections: string[];
  priority: number;
  descriptionPrefix: string;
  descriptionOverride: string;
  agentInstructionsSuffix: string;
  agentInstructionsOverride: string;
}

// ---------------------------------------------------------------------------
// Pattern matching mirrors backend semantics in pkg/persona/filter.go:
//   1. Deny rules checked first  → match means DENIED
//   2. Allow rules checked second → match means ALLOWED
//   3. No match → DENIED (default)
// ---------------------------------------------------------------------------

// Port of Go's filepath.Match for persona scope names (which never contain
// path separators). Backend uses filepath.Match (pkg/persona/filter.go), so
// the live preview must apply the same wildcard semantics: `*` is any
// sequence of chars, `?` is a single char, `[abc]` / `[a-z]` / `[^abc]` are
// character classes, and `\c` escapes a literal c.
function matchPattern(pattern: string, name: string): boolean {
  if (!pattern) return false;
  if (pattern === "*") return true;

  let regex = "";
  let i = 0;
  while (i < pattern.length) {
    const c = pattern[i];
    if (c === "*") {
      regex += ".*";
      i++;
    } else if (c === "?") {
      regex += ".";
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

type Decision = "allow" | "deny" | "default-deny";

interface TraceStep {
  bucket: "deny" | "allow";
  pattern: string;
  matched: boolean;
  decisive: boolean;
}

interface Resolution {
  decision: Decision;
  matchedPattern: string;
  steps: TraceStep[];
}

// emptyAllowMeansAllow mirrors backend asymmetry between tool and connection
// gating: pkg/persona/filter.go evaluates tools with default-deny on empty
// allow, but IsConnectionAllowed permits all connections when the allow list
// is empty (backward-compatible default). The UI must match.
function resolve(
  name: string,
  allow: string[],
  deny: string[],
  emptyAllowMeansAllow: boolean,
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

  if (decisiveIdx === -1 && allow.length === 0 && emptyAllowMeansAllow) {
    decision = "allow";
    matchedPattern = "(empty allow list)";
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

interface UniqueTool {
  name: string;
  title?: string;
  kinds: string[];
  connections: string[];
  primaryKind: string;
}

function aggregateTools(tools: ToolInfo[] | undefined): UniqueTool[] {
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

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface PersonaEditorProps {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  onSave: () => void;
  onCancel: () => void;
  isCreate: boolean;
  dirty: boolean;
  selectedName: string | null;
  canDelete?: boolean;
  onDelete?: () => void;
  sourceNote?: string | null;
  isReadOnly?: boolean;
}

type Scope = "tools" | "connections";
type StatusFilter = "all" | "allowed" | "denied";

export function PersonaEditor({
  draft,
  onUpdate,
  onSave,
  onCancel,
  isCreate,
  dirty,
  selectedName,
  canDelete = false,
  onDelete,
  sourceNote = null,
  isReadOnly = false,
}: PersonaEditorProps) {
  const { data: toolsData } = useTools();
  const { data: connsData } = useConnections();
  const createMut = useCreatePersona();
  const updateMut = useUpdatePersona();
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // --- ui state --------------------------------------------------------
  const [scope, setScope] = useState<Scope>("tools");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [hovered, setHovered] = useState<string | null>(null);
  const [highlightRule, setHighlightRule] = useState<{
    bucket: "allow" | "deny";
    pattern: string;
  } | null>(null);
  const [rolesDraft, setRolesDraft] = useState("");
  const [mainTab, setMainTab] = useState<"permissions" | "behavior">(
    "permissions",
  );

  // --- derived ---------------------------------------------------------
  const uniqueTools = useMemo(
    () => aggregateTools(toolsData?.tools),
    [toolsData],
  );
  const connections = useMemo<ConnectionInfo[]>(
    () => connsData?.connections ?? [],
    [connsData],
  );

  interface Item {
    key: string;
    primary: string;
    secondary: string;
    tertiary: string;
    kind: string;
  }

  const items = useMemo<Item[]>(() => {
    if (scope === "tools") {
      return uniqueTools.map((t) => ({
        key: t.name,
        primary: t.name,
        secondary: t.kinds.join(" · "),
        tertiary:
          t.connections.length === 1
            ? "1 connection"
            : `${t.connections.length} connections`,
        kind: t.primaryKind,
      }));
    }
    return connections.map((c) => ({
      // The backend authorizes against toolkit.Connection() (see
      // pkg/registry/registry.go GetToolkitForTool → match.Connection,
      // consumed by pkg/persona/filter.go IsConnectionAllowed). The toolkit
      // instance "name" is unrelated and may diverge when connection_name
      // is configured explicitly, so allow/deny patterns must match against
      // the connection identifier, not the toolkit name.
      key: c.connection,
      primary: c.connection,
      secondary: c.kind,
      tertiary: `${c.tools.length} tools`,
      kind: c.kind,
    }));
  }, [scope, uniqueTools, connections]);

  const allowList = scope === "tools" ? draft.allowTools : draft.allowConnections;
  const denyList = scope === "tools" ? draft.denyTools : draft.denyConnections;

  const resolved = useMemo(() => {
    const map = new Map<string, Resolution>();
    for (const it of items) {
      map.set(
        it.key,
        resolve(it.primary, allowList, denyList, scope === "connections"),
      );
    }
    return map;
  }, [items, allowList, denyList]);

  const counts = useMemo(() => {
    let allowed = 0;
    let denied = 0;
    for (const r of resolved.values()) {
      if (r.decision === "allow") allowed++;
      else denied++;
    }
    return { allowed, denied, total: allowed + denied };
  }, [resolved]);

  const grouped = useMemo(() => {
    const groups = new Map<string, Item[]>();
    for (const it of items) {
      const arr = groups.get(it.kind) ?? [];
      arr.push(it);
      groups.set(it.kind, arr);
    }
    return Array.from(groups.entries()).sort(([a], [b]) =>
      a.localeCompare(b),
    );
  }, [items]);

  // --- rule mutations --------------------------------------------------
  const addAllow = useCallback(
    (pattern: string) => {
      const p = pattern.trim();
      if (!p) return;
      if (scope === "tools") {
        if (!draft.allowTools.includes(p))
          onUpdate({ allowTools: [...draft.allowTools, p] });
      } else if (!draft.allowConnections.includes(p)) {
        onUpdate({ allowConnections: [...draft.allowConnections, p] });
      }
    },
    [scope, draft.allowTools, draft.allowConnections, onUpdate],
  );

  const addDeny = useCallback(
    (pattern: string) => {
      const p = pattern.trim();
      if (!p) return;
      if (scope === "tools") {
        if (!draft.denyTools.includes(p))
          onUpdate({ denyTools: [...draft.denyTools, p] });
      } else if (!draft.denyConnections.includes(p)) {
        onUpdate({ denyConnections: [...draft.denyConnections, p] });
      }
    },
    [scope, draft.denyTools, draft.denyConnections, onUpdate],
  );

  const removeAllow = useCallback(
    (pattern: string) => {
      if (scope === "tools")
        onUpdate({
          allowTools: draft.allowTools.filter((p) => p !== pattern),
        });
      else
        onUpdate({
          allowConnections: draft.allowConnections.filter(
            (p) => p !== pattern,
          ),
        });
    },
    [scope, draft.allowTools, draft.allowConnections, onUpdate],
  );

  const removeDeny = useCallback(
    (pattern: string) => {
      if (scope === "tools")
        onUpdate({
          denyTools: draft.denyTools.filter((p) => p !== pattern),
        });
      else
        onUpdate({
          denyConnections: draft.denyConnections.filter(
            (p) => p !== pattern,
          ),
        });
    },
    [scope, draft.denyTools, draft.denyConnections, onUpdate],
  );

  const addRole = useCallback(
    (role: string) => {
      const v = role.trim();
      if (!v || draft.roles.includes(v)) return;
      onUpdate({ roles: [...draft.roles, v] });
      setRolesDraft("");
    },
    [draft.roles, onUpdate],
  );

  const removeRole = useCallback(
    (role: string) => {
      onUpdate({ roles: draft.roles.filter((r) => r !== role) });
    },
    [draft.roles, onUpdate],
  );

  // --- save ------------------------------------------------------------
  const handleSave = useCallback(() => {
    setSaveError(null);
    const payload = {
      name: draft.name,
      display_name: draft.displayName,
      description: draft.description || undefined,
      roles: draft.roles,
      allow_tools: draft.allowTools,
      deny_tools: draft.denyTools,
      allow_connections:
        draft.allowConnections.length > 0 ? draft.allowConnections : undefined,
      deny_connections:
        draft.denyConnections.length > 0 ? draft.denyConnections : undefined,
      priority: draft.priority,
      description_prefix: draft.descriptionPrefix || undefined,
      description_override: draft.descriptionOverride || undefined,
      agent_instructions_suffix: draft.agentInstructionsSuffix || undefined,
      agent_instructions_override: draft.agentInstructionsOverride || undefined,
    };
    const mutation = isCreate ? createMut : updateMut;
    mutation.mutate(
      isCreate ? payload : { ...payload, name: selectedName ?? "" },
      {
        onSuccess: () => {
          setSaveSuccess(true);
          setTimeout(() => setSaveSuccess(false), 2000);
          onSave();
        },
        onError: (err) => {
          setSaveError(
            err instanceof Error ? err.message : "Failed to save",
          );
        },
      },
    );
  }, [draft, isCreate, selectedName, createMut, updateMut, onSave]);

  const isPending = createMut.isPending || updateMut.isPending;

  const focusItem = selected ?? hovered;
  const focusResolution = focusItem ? resolved.get(focusItem) : null;
  const focusItemMeta = focusItem
    ? items.find((i) => i.key === focusItem)
    : null;

  return (
    <div className="flex h-full flex-col">
      {/* ─── HEADER ─── */}
      <div className="flex items-center justify-between border-b bg-muted/10 px-6 py-3">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold">
            {isCreate
              ? "New Persona"
              : draft.displayName || selectedName}
          </h2>
          {isReadOnly && (
            <span className="flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
              Read only
            </span>
          )}
          {dirty && (
            <span className="flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400">
              <AlertCircle className="h-3 w-3" />
              Unsaved
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          {canDelete && onDelete && (
            <button
              type="button"
              onClick={onDelete}
              aria-label="Delete persona"
              className="rounded-md border px-2 py-1.5 text-xs font-medium text-muted-foreground hover:border-destructive/30 hover:bg-destructive/10 hover:text-destructive"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
          <button
            type="button"
            onClick={onCancel}
            className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted"
          >
            {isCreate ? "Cancel" : "Revert"}
          </button>
          <button
            type="button"
            onClick={handleSave}
            disabled={
              isReadOnly ||
              isPending ||
              (!dirty && !isCreate) ||
              !draft.name ||
              !draft.displayName
            }
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-all disabled:opacity-50",
              saveSuccess
                ? "bg-green-600 text-white"
                : "bg-primary text-primary-foreground hover:bg-primary/90",
            )}
          >
            {saveSuccess ? (
              <>
                <Check className="h-3 w-3" />
                Saved
              </>
            ) : isPending ? (
              "Saving..."
            ) : (
              <>
                <Save className="h-3 w-3" />
                {isCreate ? "Create" : "Save"}
              </>
            )}
          </button>
        </div>
      </div>

      {sourceNote && (
        <div className="flex items-start gap-2 border-b bg-muted/30 px-6 py-2 text-xs text-muted-foreground">
          <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>{sourceNote}</span>
        </div>
      )}

      {saveError && (
        <div className="flex items-center gap-2 border-b bg-red-50 px-6 py-2 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-400">
          <AlertCircle className="h-3.5 w-3.5" />
          {saveError}
        </div>
      )}

      {/* ─── MAIN: left identity/rules + tabbed right area ─── */}
      <div className="grid min-h-0 flex-1 grid-cols-[300px_minmax(0,1fr)]">
        {/* ── LEFT: Identity + Rules + Context ── */}
        <aside className="overflow-y-auto border-r">
          <fieldset disabled={isReadOnly} className="contents">
          <Section title="Identity">
            <Field label="Name" required>
              <input
                type="text"
                value={draft.name}
                onChange={(e) => onUpdate({ name: e.target.value })}
                disabled={!isCreate}
                required
                placeholder="analyst"
                className="w-full rounded-md border bg-background px-2.5 py-1.5 font-mono text-xs outline-none ring-ring focus:ring-2 disabled:cursor-not-allowed disabled:opacity-60"
              />
            </Field>
            <Field label="Display Name" required>
              <input
                type="text"
                value={draft.displayName}
                onChange={(e) => onUpdate({ displayName: e.target.value })}
                required
                placeholder="Data Analyst"
                className="w-full rounded-md border bg-background px-2.5 py-1.5 text-xs outline-none ring-ring focus:ring-2"
              />
            </Field>
            <Field label="Description">
              <textarea
                value={draft.description}
                onChange={(e) => onUpdate({ description: e.target.value })}
                rows={2}
                placeholder="What this persona is for…"
                className="w-full resize-none rounded-md border bg-background px-2.5 py-1.5 text-xs outline-none ring-ring focus:ring-2"
              />
            </Field>
            <Field label="Roles">
              <ChipInput
                values={draft.roles}
                onAdd={addRole}
                onRemove={removeRole}
                draft={rolesDraft}
                onDraftChange={setRolesDraft}
                placeholder="add role + Enter"
              />
            </Field>
            <Field label="Priority">
              <input
                type="number"
                value={draft.priority}
                onChange={(e) =>
                  onUpdate({ priority: parseInt(e.target.value, 10) || 0 })
                }
                className="w-24 rounded-md border bg-background px-2.5 py-1.5 text-xs outline-none ring-ring focus:ring-2"
              />
              <p className="mt-1 text-[10px] text-muted-foreground">
                Higher wins when a user matches multiple personas.
              </p>
            </Field>
          </Section>

          <Section
            title="Allow Patterns"
            meta={
              <span className="font-mono text-[10px] text-muted-foreground">
                {allowList.length}
              </span>
            }
            description={
              scope === "tools"
                ? "Tools must match at least one allow pattern to be reachable."
                : "Connections must match at least one allow pattern. Empty list permits all connections."
            }
          >
            <RuleList
              bucket="allow"
              patterns={allowList}
              items={items}
              highlightRule={highlightRule}
              onHover={(p) =>
                setHighlightRule(p ? { bucket: "allow", pattern: p } : null)
              }
              onRemove={removeAllow}
            />
            <AddPatternButton
              bucket="allow"
              onAdd={addAllow}
              items={items}
              existing={allowList}
              scope={scope}
            />
            {allowList.length === 0 && scope === "tools" && (
              <p className="mt-2 flex items-start gap-1 text-[10px] text-amber-700 dark:text-amber-400">
                <Info className="mt-0.5 h-3 w-3 shrink-0" />
                <span>No allow patterns means no tools are reachable (default deny).</span>
              </p>
            )}
          </Section>

          <Section
            title="Deny Patterns"
            meta={
              <span className="font-mono text-[10px] text-muted-foreground">
                {denyList.length}
              </span>
            }
            description="Deny is absolute. A match blocks access even if an allow pattern also matches."
          >
            <RuleList
              bucket="deny"
              patterns={denyList}
              items={items}
              highlightRule={highlightRule}
              onHover={(p) =>
                setHighlightRule(p ? { bucket: "deny", pattern: p } : null)
              }
              onRemove={removeDeny}
            />
            <AddPatternButton
              bucket="deny"
              onAdd={addDeny}
              items={items}
              existing={denyList}
              scope={scope}
            />
          </Section>
          </fieldset>

        </aside>

        {/* ── RIGHT AREA: tabbed (Permissions / AI Assistant Behavior) ── */}
        <div className="flex min-h-0 flex-col overflow-hidden">
          <div className="flex shrink-0 border-b bg-muted/10 px-5">
            <MainTab
              active={mainTab === "permissions"}
              label="Permissions"
              onClick={() => setMainTab("permissions")}
            />
            <MainTab
              active={mainTab === "behavior"}
              label="AI Assistant Behavior"
              onClick={() => setMainTab("behavior")}
            />
          </div>

          {mainTab === "behavior" ? (
            <div className="flex-1 overflow-y-auto px-6 py-5">
              <p className="mb-5 text-xs text-muted-foreground">
                Inject persona-specific guidance into the platform description and
                agent instructions that MCP clients see. Prefix/suffix variants
                append to the platform defaults; override variants replace them
                entirely.
              </p>
              <fieldset disabled={isReadOnly} className="contents">
                <div className="space-y-5">
                  <CtxField
                    label="Description Prefix"
                    value={draft.descriptionPrefix}
                    onChange={(v) => onUpdate({ descriptionPrefix: v })}
                    minHeight="160px"
                    readOnly={isReadOnly}
                  />
                  <CtxField
                    label="Description Override"
                    value={draft.descriptionOverride}
                    onChange={(v) => onUpdate({ descriptionOverride: v })}
                    minHeight="160px"
                    readOnly={isReadOnly}
                  />
                  <CtxField
                    label="Agent Instructions Suffix"
                    value={draft.agentInstructionsSuffix}
                    onChange={(v) => onUpdate({ agentInstructionsSuffix: v })}
                    minHeight="200px"
                    readOnly={isReadOnly}
                  />
                  <CtxField
                    label="Agent Instructions Override"
                    value={draft.agentInstructionsOverride}
                    onChange={(v) => onUpdate({ agentInstructionsOverride: v })}
                    minHeight="200px"
                    readOnly={isReadOnly}
                  />
                </div>
              </fieldset>
            </div>
          ) : (
            <div className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_340px]">

        {/* ── CENTER: Tool / Connection explorer ── */}
        <section className="flex min-h-0 flex-col overflow-hidden">
          <div className="border-b bg-muted/10 px-5 pt-4 pb-3">
            <div className="mb-3">
              <h3 className="text-base font-semibold leading-tight">
                What can {draft.displayName || "this persona"} do?
              </h3>
              <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px]">
                <p className="text-muted-foreground">
                  Live preview. Updates as you edit allow / deny patterns.
                </p>
                <div className="flex items-center gap-3">
                  <span className="flex items-center gap-1.5">
                    <span className="h-2 w-2 rounded-full bg-emerald-500" />
                    <strong className="font-mono text-foreground">
                      {counts.allowed}
                    </strong>
                    <span className="text-muted-foreground">allowed</span>
                  </span>
                  <span className="flex items-center gap-1.5">
                    <span className="h-2 w-2 rounded-full bg-rose-500" />
                    <strong className="font-mono text-foreground">
                      {counts.denied}
                    </strong>
                    <span className="text-muted-foreground">denied</span>
                  </span>
                </div>
              </div>
            </div>
            <div className="flex border-b -mb-3">
              <ScopeTab
                active={scope === "tools"}
                count={uniqueTools.length}
                label="Tools"
                onClick={() => {
                  setScope("tools");
                  setSelected(null);
                  setHovered(null);
                }}
              />
              <ScopeTab
                active={scope === "connections"}
                count={connections.length}
                label="Connections"
                onClick={() => {
                  setScope("connections");
                  setSelected(null);
                  setHovered(null);
                }}
              />
            </div>
          </div>

          <div className="flex items-center gap-2 border-b px-5 py-2.5">
            <div className="relative flex-1">
              <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={`Search ${scope}…`}
                className="w-full rounded-md border bg-background py-1.5 pr-2.5 pl-8 font-mono text-[11px] outline-none ring-ring focus:ring-2"
              />
            </div>
            <div className="flex rounded-md border bg-background p-0.5">
              {(["all", "allowed", "denied"] as const).map((f) => (
                <button
                  key={f}
                  onClick={() => setStatusFilter(f)}
                  className={cn(
                    "rounded px-2 py-0.5 text-[11px] transition-colors",
                    statusFilter === f
                      ? "bg-muted font-medium text-foreground"
                      : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {f === "all"
                    ? "All"
                    : f === "allowed"
                      ? counts.allowed
                      : counts.denied}
                  {f !== "all" && " "}
                  {f !== "all" && f}
                </button>
              ))}
            </div>
          </div>

          <fieldset disabled={isReadOnly} className="contents">
          <div className="flex-1 overflow-y-auto px-5 py-3">
            {grouped.length === 0 && (
              <div className="py-12 text-center text-xs text-muted-foreground">
                No {scope} match
              </div>
            )}
            {grouped.map(([kind, kindItems]) => {
              const filtered = kindItems.filter((it) => {
                const res = resolved.get(it.key);
                if (!res) return false;
                if (statusFilter === "allowed" && res.decision !== "allow")
                  return false;
                if (statusFilter === "denied" && res.decision === "allow")
                  return false;
                if (search) {
                  const q = search.toLowerCase();
                  if (
                    !it.primary.toLowerCase().includes(q) &&
                    !it.secondary.toLowerCase().includes(q)
                  )
                    return false;
                }
                return true;
              });
              if (filtered.length === 0) return null;
              const kindAllow = kindItems.filter(
                (it) => resolved.get(it.key)?.decision === "allow",
              ).length;
              return (
                <div key={kind} className="mb-5 last:mb-0">
                  <div className="mb-1.5 flex items-baseline justify-between border-b pb-1">
                    <div className="flex items-baseline gap-2">
                      <h4 className="text-xs font-semibold uppercase tracking-wider">
                        {kind}
                      </h4>
                      <span className="font-mono text-[10px] text-muted-foreground">
                        {kindAllow}/{kindItems.length} allowed
                      </span>
                    </div>
                    <div className="flex gap-1">
                      <button
                        onClick={() => addAllow(`${kind}_*`)}
                        className="rounded px-1.5 py-0.5 font-mono text-[10px] text-emerald-700 hover:bg-emerald-50 dark:text-emerald-400 dark:hover:bg-emerald-950/40"
                        title={`Add allow rule: ${kind}_*`}
                      >
                        + allow {kind}_*
                      </button>
                      <button
                        onClick={() => addDeny(`${kind}_*`)}
                        className="rounded px-1.5 py-0.5 font-mono text-[10px] text-rose-700 hover:bg-rose-50 dark:text-rose-400 dark:hover:bg-rose-950/40"
                        title={`Add deny rule: ${kind}_*`}
                      >
                        + deny {kind}_*
                      </button>
                    </div>
                  </div>
                  <div className="grid grid-cols-1 gap-1">
                    {filtered.map((it) => {
                      const res = resolved.get(it.key)!;
                      const isAllowed = res.decision === "allow";
                      const isHighlighted =
                        highlightRule &&
                        matchPattern(highlightRule.pattern, it.primary);
                      const isSelected = selected === it.key;
                      return (
                        <ItemRow
                          key={it.key}
                          name={it.primary}
                          secondary={it.secondary}
                          tertiary={it.tertiary}
                          allowed={isAllowed}
                          highlighted={!!isHighlighted}
                          highlightBucket={highlightRule?.bucket}
                          selected={isSelected}
                          matchedPattern={res.matchedPattern}
                          decision={res.decision}
                          onHover={(h) => {
                            if (h) setHovered(it.key);
                          }}
                          onClick={() =>
                            setSelected((cur) =>
                              cur === it.key ? null : it.key,
                            )
                          }
                          onAddPattern={(bucket) => {
                            if (bucket === "allow") addAllow(it.primary);
                            else addDeny(it.primary);
                          }}
                        />
                      );
                    })}
                  </div>
                </div>
              );
            })}
          </div>
          </fieldset>
        </section>

        {/* ── RIGHT: Summary + Trace + Templates ── */}
        <aside className="flex flex-col overflow-y-auto border-l">
          <div className="grid grid-cols-2 border-b">
            <div className="border-r px-4 py-3">
              <div className="text-2xl font-semibold text-emerald-600 dark:text-emerald-400">
                {counts.allowed}
              </div>
              <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
                allowed
              </div>
            </div>
            <div className="px-4 py-3">
              <div className="text-2xl font-semibold text-rose-600 dark:text-rose-400">
                {counts.denied}
              </div>
              <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
                denied
              </div>
            </div>
            <div className="col-span-2 px-4 pb-3">
              <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full bg-emerald-500 transition-all"
                  style={{
                    width:
                      counts.total === 0
                        ? "0%"
                        : `${(counts.allowed / counts.total) * 100}%`,
                  }}
                />
              </div>
            </div>
          </div>

          <div className="border-b p-4">
            <div className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
              Resolution Trace
            </div>
            {!focusItem || !focusResolution ? (
              <p className="py-4 text-center text-[11px] text-muted-foreground">
                Hover or click an item to trace its decision.
              </p>
            ) : (
              <Trace
                name={focusItem}
                meta={focusItemMeta}
                resolution={focusResolution}
                hasAllow={allowList.length > 0}
                hasDeny={denyList.length > 0}
              />
            )}
          </div>

          {scope === "tools" && (
            <fieldset disabled={isReadOnly} className="block p-4">
              <div className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                Quick Templates
              </div>
              <div className="space-y-1">
                <TemplateRow
                  name="Administrator"
                  hint="Allow everything"
                  onApply={() =>
                    onUpdate({ allowTools: ["*"], denyTools: [] })
                  }
                />
                <TemplateRow
                  name="Read Only"
                  hint="search, browse, get_*, list_*"
                  onApply={() =>
                    onUpdate({
                      allowTools: [
                        "*_search",
                        "*_browse",
                        "*_get_*",
                        "*_list_*",
                        "*_describe_*",
                      ],
                      denyTools: [],
                    })
                  }
                />
                <TemplateRow
                  name="Analyst"
                  hint="Query + catalog, no mutations"
                  onApply={() =>
                    onUpdate({
                      allowTools: [
                        "trino_*",
                        "datahub_*",
                        "s3_get_*",
                        "s3_list_*",
                      ],
                      denyTools: ["*_delete_*", "*_execute"],
                    })
                  }
                />
                <TemplateRow
                  name="Engineer"
                  hint="Everything except destructive"
                  onApply={() =>
                    onUpdate({ allowTools: ["*"], denyTools: ["*_delete_*"] })
                  }
                />
              </div>
            </fieldset>
          )}
        </aside>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function Section({
  title,
  meta,
  description,
  children,
  collapsible,
  open,
  onToggle,
}: {
  title: string;
  meta?: React.ReactNode;
  description?: string;
  children: React.ReactNode;
  collapsible?: boolean;
  open?: boolean;
  onToggle?: () => void;
}) {
  return (
    <div className="border-b px-4 py-3 last:border-b-0">
      <div
        className={cn(
          "mb-2 flex items-center justify-between",
          collapsible && "cursor-pointer select-none",
        )}
        onClick={collapsible ? onToggle : undefined}
      >
        <div className="flex items-center gap-1.5">
          {collapsible &&
            (open ? (
              <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
            ))}
          <h3 className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
            {title}
          </h3>
        </div>
        {meta}
      </div>
      {description && (!collapsible || open) && (
        <p className="mb-2 text-[11px] leading-snug text-muted-foreground">
          {description}
        </p>
      )}
      {(!collapsible || open) && children}
    </div>
  );
}

function Field({
  label,
  required,
  children,
}: {
  label: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="mb-2.5 last:mb-0">
      <label className="mb-1 block text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
        {required && <span className="ml-0.5 text-rose-600">*</span>}
      </label>
      {children}
    </div>
  );
}

function CtxField({
  label,
  value,
  onChange,
  minHeight = "80px",
  readOnly = false,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  minHeight?: string;
  readOnly?: boolean;
}) {
  return (
    <div>
      <label className="mb-1 block text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </label>
      <MarkdownEditor
        value={value}
        onChange={onChange}
        minHeight={minHeight}
        readOnly={readOnly}
      />
    </div>
  );
}

function MainTab({
  active,
  label,
  onClick,
}: {
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "border-b-2 px-4 py-2.5 text-xs font-medium transition-colors",
        active
          ? "border-primary text-foreground"
          : "border-transparent text-muted-foreground hover:text-foreground",
      )}
    >
      {label}
    </button>
  );
}

function ChipInput({
  values,
  onAdd,
  onRemove,
  draft,
  onDraftChange,
  placeholder,
}: {
  values: string[];
  onAdd: (v: string) => void;
  onRemove: (v: string) => void;
  draft: string;
  onDraftChange: (s: string) => void;
  placeholder?: string;
}) {
  return (
    <div className="flex flex-wrap gap-1 rounded-md border bg-background p-1.5 focus-within:ring-2 focus-within:ring-ring">
      {values.map((v) => (
        <span
          key={v}
          className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px]"
        >
          {v}
          <button
            type="button"
            onClick={() => onRemove(v)}
            className="text-muted-foreground hover:text-foreground"
          >
            <X className="h-2.5 w-2.5" />
          </button>
        </span>
      ))}
      <input
        type="text"
        value={draft}
        onChange={(e) => onDraftChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            if (draft.trim()) onAdd(draft);
          } else if (
            e.key === "Backspace" &&
            !draft &&
            values.length > 0
          ) {
            onRemove(values[values.length - 1]!);
          }
        }}
        onBlur={() => {
          if (draft.trim()) onAdd(draft);
        }}
        placeholder={values.length === 0 ? placeholder : ""}
        className="flex-1 min-w-[80px] bg-transparent text-[11px] outline-none placeholder:text-muted-foreground"
      />
    </div>
  );
}

interface RuleListItem {
  key: string;
  primary: string;
}

function RuleList({
  bucket,
  patterns,
  items,
  highlightRule,
  onHover,
  onRemove,
}: {
  bucket: "allow" | "deny";
  patterns: string[];
  items: RuleListItem[];
  highlightRule: { bucket: "allow" | "deny"; pattern: string } | null;
  onHover: (p: string | null) => void;
  onRemove: (p: string) => void;
}) {
  if (patterns.length === 0) {
    return (
      <p className="text-[11px] italic text-muted-foreground">No patterns.</p>
    );
  }
  const color =
    bucket === "allow"
      ? "border-emerald-200 bg-emerald-50/40 text-emerald-900 hover:bg-emerald-50 dark:border-emerald-900 dark:bg-emerald-950/20 dark:text-emerald-300 dark:hover:bg-emerald-950/40"
      : "border-rose-200 bg-rose-50/40 text-rose-900 hover:bg-rose-50 dark:border-rose-900 dark:bg-rose-950/20 dark:text-rose-300 dark:hover:bg-rose-950/40";
  return (
    <div className="space-y-1">
      {patterns.map((p) => {
        const matches = items.filter((it) => matchPattern(p, it.primary)).length;
        const isHovered =
          highlightRule?.bucket === bucket && highlightRule.pattern === p;
        return (
          <div
            key={p}
            onMouseEnter={() => onHover(p)}
            onMouseLeave={() => onHover(null)}
            className={cn(
              "group flex items-center gap-2 rounded border px-2 py-1 transition-colors",
              color,
              isHovered && "ring-1 ring-offset-1 ring-offset-background",
            )}
          >
            <span className="flex-1 truncate font-mono text-[11px]">
              {renderPattern(p)}
            </span>
            <span className="font-mono text-[10px] text-muted-foreground">
              {matches}
            </span>
            <button
              onClick={() => onRemove(p)}
              className="rounded p-0.5 opacity-0 transition-opacity hover:bg-background group-hover:opacity-100"
              aria-label={`remove ${p}`}
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        );
      })}
    </div>
  );
}

function renderPattern(p: string) {
  return p.split("*").map((part, i, arr) => (
    <span key={i}>
      {part}
      {i < arr.length - 1 && (
        <span className="text-violet-600 dark:text-violet-400">*</span>
      )}
    </span>
  ));
}

function AddPatternButton({
  bucket,
  onAdd,
  items,
  existing,
  scope,
}: {
  bucket: "allow" | "deny";
  onAdd: (p: string) => void;
  items: { key: string; primary: string; kind: string }[];
  existing: string[];
  scope: Scope;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState("");

  const preview = useMemo(
    () =>
      draft ? items.filter((it) => matchPattern(draft, it.primary)) : [],
    [draft, items],
  );

  const kinds = useMemo(
    () => Array.from(new Set(items.map((i) => i.kind))).sort(),
    [items],
  );

  return (
    <div className="mt-2">
      {!open ? (
        <button
          type="button"
          onClick={() => setOpen(true)}
          className={cn(
            "flex w-full items-center justify-center gap-1.5 rounded-md border border-dashed py-1.5 text-[11px] transition-colors",
            bucket === "allow"
              ? "border-emerald-300 text-emerald-700 hover:bg-emerald-50 dark:border-emerald-800 dark:text-emerald-400 dark:hover:bg-emerald-950/40"
              : "border-rose-300 text-rose-700 hover:bg-rose-50 dark:border-rose-800 dark:text-rose-400 dark:hover:bg-rose-950/40",
          )}
        >
          <Plus className="h-3 w-3" />
          Add {bucket} pattern
        </button>
      ) : (
        <div
          className={cn(
            "rounded-md border p-2",
            bucket === "allow"
              ? "border-emerald-200 dark:border-emerald-900"
              : "border-rose-200 dark:border-rose-900",
          )}
        >
          <input
            type="text"
            autoFocus
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                if (draft.trim() && !existing.includes(draft.trim())) {
                  onAdd(draft);
                  setDraft("");
                  setOpen(false);
                }
              } else if (e.key === "Escape") {
                setOpen(false);
                setDraft("");
              }
            }}
            placeholder={
              scope === "tools" ? "e.g. trino_* or *_delete_*" : "e.g. acme-*"
            }
            className="w-full rounded border bg-background px-2 py-1 font-mono text-[11px] outline-none ring-ring focus:ring-2"
          />
          <div className="mt-1.5 flex flex-wrap items-center gap-1">
            <span className="text-[10px] text-muted-foreground">presets:</span>
            {kinds.map((k) => (
              <button
                key={k}
                onClick={() => setDraft(`${k}_*`)}
                type="button"
                className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] hover:bg-muted-foreground/10"
              >
                {k}_*
              </button>
            ))}
            <button
              onClick={() => setDraft("*_delete_*")}
              type="button"
              className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] hover:bg-muted-foreground/10"
            >
              *_delete_*
            </button>
            <button
              onClick={() => setDraft("*_get_*")}
              type="button"
              className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] hover:bg-muted-foreground/10"
            >
              *_get_*
            </button>
          </div>
          <div className="mt-2 text-[11px]">
            {draft ? (
              <span className="text-muted-foreground">
                Will{" "}
                <strong
                  className={
                    bucket === "allow"
                      ? "text-emerald-700 dark:text-emerald-400"
                      : "text-rose-700 dark:text-rose-400"
                  }
                >
                  {bucket}
                </strong>{" "}
                {preview.length} {preview.length === 1 ? "item" : "items"}
                {preview.length > 0 && preview.length <= 5 && (
                  <span className="ml-1 font-mono text-[10px]">
                    ({preview.map((p) => p.primary).join(", ")})
                  </span>
                )}
              </span>
            ) : (
              <span className="italic text-muted-foreground">
                Type a pattern or pick a preset
              </span>
            )}
          </div>
          <div className="mt-2 flex justify-end gap-1.5">
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                setDraft("");
              }}
              className="rounded border px-2 py-0.5 text-[11px] hover:bg-muted"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => {
                if (draft.trim() && !existing.includes(draft.trim())) {
                  onAdd(draft);
                  setDraft("");
                  setOpen(false);
                }
              }}
              disabled={!draft.trim()}
              className={cn(
                "rounded px-2 py-0.5 text-[11px] text-white disabled:opacity-50",
                bucket === "allow"
                  ? "bg-emerald-600 hover:bg-emerald-700"
                  : "bg-rose-600 hover:bg-rose-700",
              )}
            >
              Add
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function ScopeTab({
  active,
  count,
  label,
  onClick,
}: {
  active: boolean;
  count: number;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "flex items-center gap-2 px-3 py-1.5 text-xs font-medium transition-colors",
        active
          ? "border-b-2 border-primary text-foreground"
          : "text-muted-foreground hover:text-foreground",
      )}
    >
      {label}
      <span
        className={cn(
          "rounded px-1.5 py-0.5 font-mono text-[10px]",
          active
            ? "bg-muted text-foreground"
            : "bg-muted/50 text-muted-foreground",
        )}
      >
        {count}
      </span>
    </button>
  );
}

function ItemRow({
  name,
  secondary,
  tertiary,
  allowed,
  highlighted,
  highlightBucket,
  selected,
  decision,
  matchedPattern,
  onHover,
  onClick,
  onAddPattern,
}: {
  name: string;
  secondary: string;
  tertiary: string;
  allowed: boolean;
  highlighted: boolean;
  highlightBucket?: "allow" | "deny";
  selected: boolean;
  decision: Decision;
  matchedPattern: string;
  onHover: (h: boolean) => void;
  onClick: () => void;
  onAddPattern: (bucket: "allow" | "deny") => void;
}) {
  const statusBorder = allowed
    ? "border-l-emerald-500"
    : decision === "deny"
      ? "border-l-rose-500"
      : "border-l-muted-foreground/30";

  const bg = allowed
    ? "bg-gradient-to-r from-emerald-50/60 to-transparent dark:from-emerald-950/30"
    : decision === "deny"
      ? "bg-gradient-to-r from-rose-50/60 to-transparent dark:from-rose-950/30"
      : "bg-gradient-to-r from-muted/40 to-transparent";

  const ring = highlighted
    ? highlightBucket === "allow"
      ? "ring-2 ring-emerald-400"
      : "ring-2 ring-rose-400"
    : "";

  return (
    <div
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      onClick={onClick}
      className={cn(
        "group relative cursor-pointer rounded-md border border-l-4 px-2.5 py-1.5 transition-all",
        statusBorder,
        bg,
        ring,
        selected ? "ring-2 ring-primary" : "hover:border-foreground/20",
      )}
    >
      <div className="flex items-center gap-2">
        <div className="flex h-4 w-4 shrink-0 items-center justify-center">
          {allowed ? (
            <Check className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400" />
          ) : (
            <Ban className="h-3.5 w-3.5 text-rose-600 dark:text-rose-400" />
          )}
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-[11px] font-medium">
            {name}
          </div>
          <div className="mt-0.5 flex items-center gap-1.5 text-[10px] text-muted-foreground">
            <span>{secondary}</span>
            <span>·</span>
            <span>{tertiary}</span>
            {matchedPattern && (
              <>
                <span>·</span>
                <span className="font-mono italic">
                  matched {matchedPattern}
                </span>
              </>
            )}
          </div>
        </div>
        <div className="flex shrink-0 gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
          <button
            onClick={(e) => {
              e.stopPropagation();
              onAddPattern("allow");
            }}
            className="rounded p-1 text-emerald-700 hover:bg-emerald-100 dark:text-emerald-400 dark:hover:bg-emerald-950/60"
            title={`Allow ${name}`}
          >
            <Check className="h-3 w-3" />
          </button>
          <button
            onClick={(e) => {
              e.stopPropagation();
              onAddPattern("deny");
            }}
            className="rounded p-1 text-rose-700 hover:bg-rose-100 dark:text-rose-400 dark:hover:bg-rose-950/60"
            title={`Deny ${name}`}
          >
            <Ban className="h-3 w-3" />
          </button>
        </div>
      </div>
    </div>
  );
}

function Trace({
  name,
  meta,
  resolution,
  hasAllow,
  hasDeny,
}: {
  name: string;
  meta?: { secondary: string; tertiary: string } | null;
  resolution: Resolution;
  hasAllow: boolean;
  hasDeny: boolean;
}) {
  const result = resolution.decision;
  return (
    <div>
      <div className="mb-3">
        <div className="font-mono text-xs font-semibold break-all">{name}</div>
        {meta && (
          <div className="mt-0.5 text-[10px] text-muted-foreground">
            {meta.secondary} · {meta.tertiary}
          </div>
        )}
      </div>

      <div className="space-y-2">
        <TraceBucket
          label="1. Deny patterns"
          empty="no deny patterns"
          steps={resolution.steps.filter((s) => s.bucket === "deny")}
          present={hasDeny}
        />
        <TraceBucket
          label="2. Allow patterns"
          empty="no allow patterns"
          steps={resolution.steps.filter((s) => s.bucket === "allow")}
          present={hasAllow}
          dim={result === "deny"}
        />
      </div>

      <div
        className={cn(
          "mt-3 flex items-center gap-2 rounded-md border px-3 py-2 font-mono text-[11px]",
          result === "allow"
            ? "border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-300"
            : "border-rose-200 bg-rose-50 text-rose-800 dark:border-rose-900 dark:bg-rose-950/30 dark:text-rose-300",
        )}
      >
        {result === "allow" ? (
          <Check className="h-4 w-4" />
        ) : (
          <Ban className="h-4 w-4" />
        )}
        <span>
          {result === "allow"
            ? `ALLOWED via ${resolution.matchedPattern}`
            : result === "deny"
              ? `DENIED via ${resolution.matchedPattern}`
              : "DENIED: no allow pattern matched"}
        </span>
      </div>
    </div>
  );
}

function TraceBucket({
  label,
  empty,
  steps,
  present,
  dim,
}: {
  label: string;
  empty: string;
  steps: TraceStep[];
  present: boolean;
  dim?: boolean;
}) {
  return (
    <div className={dim ? "opacity-50" : ""}>
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      {!present || steps.length === 0 ? (
        <div className="pl-2 text-[10px] italic text-muted-foreground">
          {empty}
        </div>
      ) : (
        <ul className="space-y-0.5">
          {steps.map((s, idx) => (
            <li
              key={idx}
              className={cn(
                "flex items-center gap-1.5 rounded py-0.5 pl-2 pr-1.5 font-mono text-[10px]",
                s.decisive
                  ? s.bucket === "allow"
                    ? "bg-emerald-100 text-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300"
                    : "bg-rose-100 text-rose-900 dark:bg-rose-950/40 dark:text-rose-300"
                  : s.matched
                    ? "text-foreground"
                    : "text-muted-foreground",
              )}
            >
              <span className="text-muted-foreground">
                {s.matched ? "▸" : "·"}
              </span>
              <span className="flex-1">{renderPattern(s.pattern)}</span>
              {s.decisive && (
                <span className="text-[8px] uppercase tracking-wider">
                  final
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function TemplateRow({
  name,
  hint,
  onApply,
}: {
  name: string;
  hint: string;
  onApply: () => void;
}) {
  return (
    <button
      onClick={onApply}
      className="flex w-full items-center justify-between rounded-md border bg-background px-2.5 py-1.5 text-left transition-colors hover:border-primary/40 hover:bg-muted/40"
    >
      <div>
        <div className="text-[11px] font-semibold">{name}</div>
        <div className="mt-0.5 text-[10px] text-muted-foreground">{hint}</div>
      </div>
      <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
    </button>
  );
}
