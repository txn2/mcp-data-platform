import { useState, useMemo, useCallback } from "react";
import { Check, Save, AlertCircle, Info, Trash2 } from "lucide-react";
import {
  useTools,
  useConnections,
  useCreatePersona,
  useUpdatePersona,
} from "@/api/admin/hooks";
import type { ConnectionInfo } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import { resolve, aggregateTools, type Resolution } from "./persona/resolve";
import type { PersonaDraft, Scope, StatusFilter, Item } from "./persona/types";
import { MainTab } from "./persona/primitives";
import { IdentityRulesAside } from "./persona/IdentityRulesAside";
import { BehaviorTab } from "./persona/BehaviorTab";
import { PermissionsExplorer } from "./persona/PermissionsExplorer";

// Public re-exports keep the import surface stable for existing consumers:
// PersonasPanel imports { PersonaEditor, type PersonaDraft } and the
// PersonaEditor.resolve.test.ts suite imports { resolve } from this module.
export { resolve } from "./persona/resolve";
export type { PersonaDraft } from "./persona/types";

// PersonaEditor is the master/detail editor for a single persona: identity +
// allow/deny rules on the left, and a tabbed right area (live Permissions
// preview / AI Assistant Behavior). The pure resolution engine, presentational
// primitives, and the three facets (identity+rules, permissions explorer,
// behavior) live under ./persona/ (#766) so this file owns only the shared
// state, derived data, mutation handlers, and layout composition.

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

  const toolConnectionCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const conn of connections) {
      for (const toolName of conn.tools ?? []) {
        counts.set(toolName, (counts.get(toolName) ?? 0) + 1);
      }
    }
    return counts;
  }, [connections]);

  const items = useMemo<Item[]>(() => {
    if (scope === "tools") {
      return uniqueTools.map((t) => {
        const connCount = toolConnectionCounts.get(t.name) ?? t.connections.length;
        return {
          key: t.name,
          primary: t.name,
          secondary: t.kinds.join(" · "),
          tertiary:
            connCount === 1
              ? "1 connection"
              : `${connCount} connections`,
          kind: t.primaryKind,
        };
      });
    }
    return connections.map((c) => {
      // The backend authorizes against toolkit.Connection() (see
      // pkg/registry/registry.go GetToolkitForTool → match.Connection,
      // consumed by pkg/persona/filter.go IsConnectionAllowed). The toolkit
      // instance "name" is unrelated and may diverge when connection_name
      // is configured explicitly, so allow/deny patterns must match against
      // the connection identifier, not the toolkit name.
      const toolCount = c.tools?.length ?? 0;
      return {
        key: c.connection,
        primary: c.connection,
        secondary: c.kind,
        tertiary: `${toolCount} tools`,
        kind: c.kind,
      };
    });
  }, [scope, uniqueTools, connections, toolConnectionCounts]);

  const allowList = scope === "tools" ? draft.allowTools : draft.allowConnections;
  const denyList = scope === "tools" ? draft.denyTools : draft.denyConnections;

  const resolved = useMemo(() => {
    // Both tools and connections are deny-by-default (see resolve / backend
    // pkg/persona/filter.go): an empty allow-list grants nothing.
    const map = new Map<string, Resolution>();
    for (const it of items) {
      map.set(it.key, resolve(it.primary, allowList, denyList));
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

  // addMany adds every given pattern in a single update. Looping addAllow/
  // addDeny would not work: each call reads the same pre-update draft, so only
  // the last addition would survive. Used by the group "allow/deny all"
  // buttons when a kind glob does not match every item (gateway tool names
  // like "mcptest__echo" and connection names like "Test API" are not
  // kind-prefixed, so a `${kind}_*` pattern matches none of them).
  const addMany = useCallback(
    (bucket: "allow" | "deny", patterns: string[]) => {
      const clean = patterns.map((n) => n.trim()).filter(Boolean);
      const key = (
        scope === "tools"
          ? bucket === "allow"
            ? "allowTools"
            : "denyTools"
          : bucket === "allow"
            ? "allowConnections"
            : "denyConnections"
      ) as keyof PersonaDraft;
      const cur = draft[key] as string[];
      const next = cur.slice();
      for (const p of clean) if (!next.includes(p)) next.push(p);
      if (next.length !== cur.length) onUpdate({ [key]: next });
    },
    [scope, draft, onUpdate],
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
      {/*
        Stack vertically below lg (1024px) so the scope tabs in the center
        column remain reachable on narrow viewports — without this, the
        fixed 300px left rail starves the right column of usable width and
        users can't switch the Allow/Deny editor between tools and
        connections scope.
      */}
      <div className="flex min-h-0 flex-1 flex-col lg:grid lg:grid-cols-[300px_minmax(0,1fr)]">
        {/* ── LEFT: Identity + Rules + Context ── */}
        <IdentityRulesAside
          isReadOnly={isReadOnly}
          isCreate={isCreate}
          draft={draft}
          onUpdate={onUpdate}
          scope={scope}
          allowList={allowList}
          denyList={denyList}
          items={items}
          highlightRule={highlightRule}
          setHighlightRule={setHighlightRule}
          rolesDraft={rolesDraft}
          setRolesDraft={setRolesDraft}
          addRole={addRole}
          removeRole={removeRole}
          addAllow={addAllow}
          addDeny={addDeny}
          removeAllow={removeAllow}
          removeDeny={removeDeny}
        />

        {/* ── RIGHT AREA: tabbed (Permissions / AI Assistant Behavior) ── */}
        <div className="flex flex-col lg:min-h-0 lg:overflow-hidden">
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
            <BehaviorTab
              draft={draft}
              onUpdate={onUpdate}
              isReadOnly={isReadOnly}
            />
          ) : (
            <PermissionsExplorer
              draft={draft}
              onUpdate={onUpdate}
              isReadOnly={isReadOnly}
              scope={scope}
              setScope={setScope}
              statusFilter={statusFilter}
              setStatusFilter={setStatusFilter}
              search={search}
              setSearch={setSearch}
              selected={selected}
              setSelected={setSelected}
              hovered={hovered}
              setHovered={setHovered}
              toolCount={uniqueTools.length}
              connectionCount={connections.length}
              items={items}
              resolved={resolved}
              counts={counts}
              grouped={grouped}
              highlightRule={highlightRule}
              allowList={allowList}
              denyList={denyList}
              addAllow={addAllow}
              addDeny={addDeny}
              addMany={addMany}
            />
          )}
        </div>
      </div>
    </div>
  );
}
