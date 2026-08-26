import { useState, useMemo, useCallback } from "react";
import {
  useTools,
  useConnections,
  useCreatePersona,
  useUpdatePersona,
} from "@/api/admin/hooks";
import type { ConnectionInfo } from "@/api/admin/types";
import type { ApiScopeState } from "./PermissionsExplorer";
import { useApiRouteScope } from "./useApiRouteScope";
import { resolve, aggregateTools, type Resolution } from "./resolve";
import type { Bucket } from "./tints";
import type { PersonaDraft, Scope, StatusFilter, Item } from "./types";

// toPayload converts the flat editor draft into the admin API body. Empty
// strings and empty connection lists are sent as undefined rather than as
// empty values: the API treats an absent field as "unset", and a persona with
// no connection rules must not be saved as one that denies every connection.
function toPayload(draft: PersonaDraft) {
  return {
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
    // Sent only when the persona has rules. An absent field leaves the persona
    // with none, which is the same "no rule names this connection" state a
    // persona that never had any is in — and is what a save must produce for a
    // connection the operator did not touch.
    api_routes: draft.apiRoutes.length > 0 ? draft.apiRoutes : undefined,
    priority: draft.priority,
    description_prefix: draft.descriptionPrefix || undefined,
    description_override: draft.descriptionOverride || undefined,
    agent_instructions_suffix: draft.agentInstructionsSuffix || undefined,
    agent_instructions_override: draft.agentInstructionsOverride || undefined,
  };
}

// usePersonaEditor owns everything the persona editor knows that is not
// layout: the explorer's view state, the tool/connection catalogue it resolves
// against, and the rule mutations the facets call back into. Extracted from
// PersonaEditor.tsx (#1206) so the component is composition only.
export function usePersonaEditor({
  draft,
  onUpdate,
  onSave,
  isCreate,
  selectedName,
}: {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  onSave: () => void;
  isCreate: boolean;
  selectedName: string | null;
}) {
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
    bucket: Bucket;
    pattern: string;
  } | null>(null);
  const [rolesDraft, setRolesDraft] = useState("");
  // The rule chip the pointer is on, so the operation list can mark the rows
  // that rule governs. Indexed rather than matched by value: two rules can be
  // written identically and each still has its own chip.
  const [highlightRoute, setHighlightRoute] = useState<number | null>(null);
  const [mainTab, setMainTab] = useState<"permissions" | "behavior">("permissions");

  // --- derived ---------------------------------------------------------
  const uniqueTools = useMemo(() => aggregateTools(toolsData?.tools), [toolsData]);
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
          tertiary: connCount === 1 ? "1 connection" : `${connCount} connections`,
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

  const api = useApiRouteScope({
    draft,
    onUpdate,
    scope,
    selected,
    hovered,
    highlightIndex: highlightRoute,
  });

  // The api scope has no allow/deny string buckets — its rules are objects —
  // so it borrows the connection lists for the two panes it does not render.
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

  const patternCounts = useMemo(() => {
    let allowed = 0;
    let denied = 0;
    for (const r of resolved.values()) {
      if (r.decision === "allow") allowed++;
      else denied++;
    }
    return { allowed, denied, total: allowed + denied };
  }, [resolved]);

  const counts = scope === "api" ? api.counts : patternCounts;

  // The bundle the explorer takes, assembled once so the component's signature
  // does not grow a prop per field of it.
  const apiScope = useMemo<ApiScopeState>(
    () => ({
      connections: api.connections,
      isLoading: api.isLoading,
      operationCount: api.counts.total,
      resolveFor: api.resolveFor,
      handlers: {
        setOperation: api.setOperationRule,
        setConnection: api.setConnectionRule,
      },
      focus: api.focus,
      governedBy: api.governedBy,
    }),
    [api],
  );

  // Clearing the highlight is the editor's business, not the scope's: the
  // index the rail is pointing at names a rule that is about to be gone.
  const removeRouteRule = useCallback(
    (index: number) => {
      setHighlightRoute(null);
      api.removeRule(index);
    },
    [api],
  );

  const apiConnectionNames = useMemo(
    () => api.connections.map((c) => c.name),
    [api.connections],
  );

  const grouped = useMemo(() => {
    const groups = new Map<string, Item[]>();
    for (const it of items) {
      const arr = groups.get(it.kind) ?? [];
      arr.push(it);
      groups.set(it.kind, arr);
    }
    return Array.from(groups.entries()).sort(([a], [b]) => a.localeCompare(b));
  }, [items]);

  // --- rule mutations --------------------------------------------------
  // bucketKey names the draft field a (scope, bucket) pair writes to, so every
  // mutation below addresses one field rather than branching twice.
  const bucketKey = useCallback(
    (bucket: Bucket): keyof PersonaDraft => {
      if (scope === "tools") return bucket === "allow" ? "allowTools" : "denyTools";
      return bucket === "allow" ? "allowConnections" : "denyConnections";
    },
    [scope],
  );

  const addPattern = useCallback(
    (bucket: Bucket, pattern: string) => {
      const p = pattern.trim();
      if (!p) return;
      const key = bucketKey(bucket);
      const cur = draft[key] as string[];
      if (cur.includes(p)) return;
      onUpdate({ [key]: [...cur, p] });
    },
    [bucketKey, draft, onUpdate],
  );

  const removePattern = useCallback(
    (bucket: Bucket, pattern: string) => {
      const key = bucketKey(bucket);
      const cur = draft[key] as string[];
      onUpdate({ [key]: cur.filter((p) => p !== pattern) });
    },
    [bucketKey, draft, onUpdate],
  );

  // addMany adds every given pattern in a single update. Looping addPattern
  // would not work: each call reads the same pre-update draft, so only the
  // last addition would survive. Used by the group "allow/deny all" buttons
  // when a kind glob does not match every item (gateway tool names like
  // "mcptest__echo" and connection names like "Test API" are not
  // kind-prefixed, so a `${kind}_*` pattern matches none of them).
  const addMany = useCallback(
    (bucket: Bucket, patterns: string[]) => {
      const clean = patterns.map((n) => n.trim()).filter(Boolean);
      const key = bucketKey(bucket);
      const cur = draft[key] as string[];
      const next = cur.slice();
      for (const p of clean) if (!next.includes(p)) next.push(p);
      if (next.length !== cur.length) onUpdate({ [key]: next });
    },
    [bucketKey, draft, onUpdate],
  );

  const addRole = useCallback(
    (role: string) => {
      const v = role.trim();
      setRolesDraft("");
      if (!v || draft.roles.includes(v)) return;
      onUpdate({ roles: [...draft.roles, v] });
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
    const payload = toPayload(draft);
    const mutation = isCreate ? createMut : updateMut;
    mutation.mutate(isCreate ? payload : { ...payload, name: selectedName ?? "" }, {
      onSuccess: () => {
        setSaveSuccess(true);
        setTimeout(() => setSaveSuccess(false), 2000);
        onSave();
      },
      onError: (err) => {
        setSaveError(err instanceof Error ? err.message : "Failed to save");
      },
    });
  }, [draft, isCreate, selectedName, createMut, updateMut, onSave]);

  return {
    scope,
    setScope,
    statusFilter,
    setStatusFilter,
    search,
    setSearch,
    selected,
    setSelected,
    hovered,
    setHovered,
    highlightRule,
    setHighlightRule,
    rolesDraft,
    setRolesDraft,
    mainTab,
    setMainTab,
    toolCount: uniqueTools.length,
    connectionCount: connections.length,
    api: apiScope,
    apiConnectionNames,
    addRouteRule: api.addRule,
    removeRouteRule,
    highlightRoute,
    setHighlightRoute,
    items,
    allowList,
    denyList,
    resolved,
    counts,
    grouped,
    addAllow: useCallback((p: string) => addPattern("allow", p), [addPattern]),
    addDeny: useCallback((p: string) => addPattern("deny", p), [addPattern]),
    removeAllow: useCallback((p: string) => removePattern("allow", p), [removePattern]),
    removeDeny: useCallback((p: string) => removePattern("deny", p), [removePattern]),
    addMany,
    addRole,
    removeRole,
    handleSave,
    isPending: createMut.isPending || updateMut.isPending,
    saveSuccess,
    saveError,
  };
}
