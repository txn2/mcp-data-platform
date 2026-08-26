import { useCallback, useMemo } from "react";
import { useAPIRouteConnections } from "@/api/admin/hooks";
import type {
  APIRouteConnection,
  APIRouteOperation,
  APIRouteRule,
} from "@/api/admin/types";
import {
  parseOperationKey,
  resolveRoute,
  ruleGoverns,
  withOperationRule,
  withoutRuleAt,
  type RouteResolution,
} from "./apiRoutes";
import type { Bucket } from "./tints";
import type { PersonaDraft, RouteFocus } from "./types";

// The API-endpoint scope's half of the persona editor's state (#1479): the
// operation inventory a rule can be written against, the decisions the unsaved
// draft produces for it, and the mutations the list and the rule editor call
// back into.
//
// Its own hook rather than more of usePersonaEditor, which is at its
// lines-per-function budget, and because the api axis shares almost nothing
// with the two pattern axes: its rules are objects, its decision has three
// outcomes, and its inventory comes from a different route.

export function useApiRouteScope({
  draft,
  onUpdate,
  scope,
  selected,
  hovered,
  highlightIndex,
}: {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  scope: string;
  selected: string | null;
  hovered: string | null;
  /** The rule chip the pointer is on, by position in the persona's list. */
  highlightIndex: number | null;
}) {
  const { data, isLoading } = useAPIRouteConnections();

  const connections = useMemo<APIRouteConnection[]>(
    () => data?.connections ?? [],
    [data],
  );

  // The persona's decision on one operation, computed from the UNSAVED draft so
  // the preview moves as the operator selects rather than only after a save.
  const resolveFor = useCallback(
    (connection: string, op: APIRouteOperation): RouteResolution =>
      resolveRoute(draft.apiRoutes, connection, op.method.toUpperCase(), op.path),
    [draft.apiRoutes],
  );

  const counts = useMemo(() => {
    let allowed = 0;
    let denied = 0;
    for (const conn of connections) {
      for (const op of conn.operations) {
        // An operation no rule names is reachable, so it is counted with the
        // allowed. The tally answers "what can this persona call"; the list
        // marks which of those are reachable by rule and which by default.
        if (resolveFor(conn.name, op).decision === "deny") denied++;
        else allowed++;
      }
    }
    return { allowed, denied, total: allowed + denied };
  }, [connections, resolveFor]);

  // A selection compiles to the operation's own method and the path its catalog
  // declares, which is the rule shape a file config writes, so a rule written
  // here and one written in YAML are the same rule.
  const setOperationRule = useCallback(
    (connection: string, op: APIRouteOperation, bucket: Bucket) => {
      onUpdate({
        apiRoutes: withOperationRule(draft.apiRoutes, connection, op, bucket),
      });
    },
    [draft.apiRoutes, onUpdate],
  );

  // A whole-connection rule leaves methods and paths empty, which the server
  // reads as "any" — the same rule an operator would write by hand.
  const setConnectionRule = useCallback(
    (connection: string, bucket: Bucket) => {
      onUpdate({ apiRoutes: [...draft.apiRoutes, { connection, action: bucket }] });
    },
    [draft.apiRoutes, onUpdate],
  );

  const addRule = useCallback(
    (rule: APIRouteRule) => {
      onUpdate({ apiRoutes: [...draft.apiRoutes, rule] });
    },
    [draft.apiRoutes, onUpdate],
  );

  const removeRule = useCallback(
    (index: number) => {
      onUpdate({ apiRoutes: withoutRuleAt(draft.apiRoutes, index) });
    },
    [draft.apiRoutes, onUpdate],
  );

  // focus resolves the row key the pointer is on back to the operation it
  // names. The key carries the connection, the method and the path, which is
  // everything the rail renders, so the inventory is not searched again.
  // operationKey writes it and parseOperationKey reads it, in one file.
  const focus = useMemo<RouteFocus | null>(() => {
    const key = selected ?? hovered;
    if (scope !== "api" || !key) return null;
    const parsed = parseOperationKey(key);
    if (!parsed) return null;
    return {
      ...parsed,
      resolution: resolveRoute(
        draft.apiRoutes,
        parsed.connection,
        parsed.method,
        parsed.path,
      ),
    };
  }, [scope, selected, hovered, draft.apiRoutes]);

  // governedBy marks every operation the rule under the pointer reaches, the
  // way hovering a pattern chip marks the items that pattern matches. It is
  // deliberately not "the rule that decided": a rule can cover an operation a
  // deny above it refuses, and the operator has to see that it does.
  const governedBy = useCallback(
    (connection: string, op: APIRouteOperation) => {
      if (highlightIndex === null) return false;
      const rule = draft.apiRoutes[highlightIndex];
      return !!rule && ruleGoverns(rule, connection, op.method.toUpperCase(), op.path);
    },
    [highlightIndex, draft.apiRoutes],
  );

  return {
    connections,
    isLoading,
    counts,
    resolveFor,
    setOperationRule,
    setConnectionRule,
    addRule,
    removeRule,
    focus,
    governedBy,
  };
}
