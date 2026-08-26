import { Ban, Check, Minus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/patterns/EmptyState";
import { MethodBadge } from "@/components/patterns/MethodBadge";
import { cn } from "@/lib/utils";
import type { APIRouteConnection, APIRouteOperation } from "@/api/admin/types";
import { operationKey, type RouteResolution } from "./apiRoutes";
import { BUCKET_TINT, type Bucket } from "./tints";
import type { StatusFilter } from "./types";

// The API-endpoint scope's list: every api-kind connection this deployment
// serves and, under each, the operations its catalog declares, with the
// persona's current decision on each one and the rule that produced it (#1479).
//
// It does not reuse ExplorerGroups because the unit differs. A tool or a
// connection is a name matched by a string pattern; an operation is a
// (connection, method, path) triple governed by a rule object, and selecting
// one writes that operation's own method and declared path rather than a glob.

export interface ApiScopeHandlers {
  /** Write the rule this operation compiles to. */
  setOperation: (connection: string, op: APIRouteOperation, bucket: Bucket) => void;
  /** Write one rule covering every method and path of a connection. */
  setConnection: (connection: string, bucket: Bucket) => void;
}

/** matchesSearch narrows an operation by the toolbar's free text. */
function matchesSearch(op: APIRouteOperation, q: string): boolean {
  if (!q) return true;
  return (
    op.path.toLowerCase().includes(q) ||
    op.method.toLowerCase().includes(q) ||
    (op.summary ?? "").toLowerCase().includes(q) ||
    (op.operation_id ?? "").toLowerCase().includes(q)
  );
}

/** matchesStatus applies the allowed/denied filter. An open operation is
 * reachable, so it counts as allowed here exactly as it does in the tally. */
function matchesStatus(decision: RouteResolution["decision"], f: StatusFilter): boolean {
  if (f === "allowed") return decision !== "deny";
  if (f === "denied") return decision === "deny";
  return true;
}

export function ApiScopeGroups({
  connections,
  resolve,
  statusFilter,
  search,
  selected,
  setSelected,
  setHovered,
  handlers,
  isLoading,
  governedBy,
}: {
  connections: APIRouteConnection[];
  resolve: (connection: string, op: APIRouteOperation) => RouteResolution;
  statusFilter: StatusFilter;
  search: string;
  selected: string | null;
  setSelected: React.Dispatch<React.SetStateAction<string | null>>;
  setHovered: React.Dispatch<React.SetStateAction<string | null>>;
  handlers: ApiScopeHandlers;
  isLoading: boolean;
  /** Whether the rule the pointer is on in the rail governs this operation. */
  governedBy: (connection: string, op: APIRouteOperation) => boolean;
}) {
  const q = search.toLowerCase();

  if (isLoading) {
    return (
      <div className="px-5 py-3 xl:flex-1 xl:overflow-y-auto">
        <EmptyState>Loading API connections…</EmptyState>
      </div>
    );
  }

  if (connections.length === 0) {
    return (
      <div className="px-5 py-3 xl:flex-1 xl:overflow-y-auto">
        <EmptyState>
          No API connections are configured. Route rules apply to `api` kind
          connections only.
        </EmptyState>
      </div>
    );
  }

  return (
    <div className="px-5 py-3 xl:flex-1 xl:overflow-y-auto">
      {connections.map((conn) => {
        const visible = conn.operations.filter(
          (op) =>
            matchesSearch(op, q) &&
            matchesStatus(resolve(conn.name, op).decision, statusFilter),
        );
        return (
          <div key={conn.name} className="mb-5 last:mb-0">
            <ConnectionHeader
              conn={conn}
              resolve={resolve}
              handlers={handlers}
            />
            {conn.operations.length === 0 ? (
              <p className="py-2 text-[11px] italic text-muted-foreground">
                No catalog is loaded for this connection, so it exposes no
                operation to select. Write a path pattern in the rule editor
                instead.
              </p>
            ) : (
              visible.length === 0 && (
                <p className="py-2 text-[11px] italic text-muted-foreground">
                  No operation matches the current filter.
                </p>
              )
            )}
            <div className="grid grid-cols-1 gap-1">
              {visible.map((op) => (
                <OperationRow
                  key={`${conn.name}:${op.method}:${op.path}`}
                  connection={conn.name}
                  op={op}
                  resolution={resolve(conn.name, op)}
                  selected={selected === operationKey(conn.name, op)}
                  onSelect={() =>
                    setSelected((cur) =>
                      cur === operationKey(conn.name, op) ? null : operationKey(conn.name, op),
                    )
                  }
                  onHover={(h) => setHovered(h ? operationKey(conn.name, op) : null)}
                  handlers={handlers}
                  highlighted={governedBy(conn.name, op)}
                />
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function ConnectionHeader({
  conn,
  resolve,
  handlers,
}: {
  conn: APIRouteConnection;
  resolve: (connection: string, op: APIRouteOperation) => RouteResolution;
  handlers: ApiScopeHandlers;
}) {
  const reachable = conn.operations.filter(
    (op) => resolve(conn.name, op).decision !== "deny",
  ).length;

  return (
    <div className="mb-1.5 flex items-baseline justify-between gap-2 border-b pb-1">
      {/* The name never truncates: it is what every rule in the rail names,
          and a header reading "ACME-BI…" cannot be matched to a rule chip.
          The upstream URL gives up its width first. */}
      <div className="flex min-w-0 items-baseline gap-2">
        <h4 className="shrink-0 text-xs font-semibold uppercase tracking-wider">
          {conn.name}
        </h4>
        <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
          {reachable}/{conn.operations.length} reachable
        </span>
        {conn.base_url && (
          <span className="hidden min-w-0 truncate font-mono text-[10px] text-muted-foreground lg:inline">
            {conn.base_url}
          </span>
        )}
      </div>
      <div className="flex shrink-0 gap-1">
        {(["allow", "deny"] as const).map((bucket) => (
          <Button
            key={bucket}
            type="button"
            variant="ghost"
            size="xs"
            onClick={() => handlers.setConnection(conn.name, bucket)}
            title={`Add a rule that ${bucket === "allow" ? "allows" : "denies"} every method and path on ${conn.name}`}
            className={cn("h-5 px-1.5 font-mono text-[10px]", BUCKET_TINT[bucket].text)}
          >
            + {bucket} all
          </Button>
        ))}
      </div>
    </div>
  );
}

/** DECISION_LABEL names each outcome for the row and its title text. */
const DECISION_LABEL = {
  allow: "Allowed by a rule",
  deny: "Denied",
  open: "Reachable: no rule names this connection",
} as const;

function OperationRow({
  connection,
  op,
  resolution,
  selected,
  onSelect,
  onHover,
  handlers,
  highlighted,
}: {
  connection: string;
  op: APIRouteOperation;
  resolution: RouteResolution;
  selected: boolean;
  onSelect: () => void;
  onHover: (hovered: boolean) => void;
  handlers: ApiScopeHandlers;
  highlighted: boolean;
}) {
  const { decision, rule } = resolution;
  const denied = decision === "deny";
  const tint = denied ? BUCKET_TINT.deny : BUCKET_TINT.allow;

  return (
    <div
      role="button"
      tabIndex={0}
      aria-label={`${op.method} ${op.path}`}
      title={DECISION_LABEL[decision]}
      onClick={onSelect}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect();
        }
      }}
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      className={cn(
        "group flex cursor-pointer items-center gap-2 rounded border-l-2 px-2 py-1 text-left transition-colors hover:bg-muted/50",
        denied ? tint.edge : decision === "allow" ? tint.edge : "border-l-transparent",
        selected && "bg-muted/60",
        highlighted && tint.surface,
      )}
    >
      <DecisionIcon decision={decision} />
      <MethodBadge method={op.method} className="w-14" />
      {/* The path is what a rule names, so it gets the row's width. The
          summary yields first, and the rule that decided is not repeated on
          every row: the icon carries the decision and the rail carries the
          rule. The one thing the icon cannot say is that a denial came from
          the rule set rather than from a rule, so that keeps its note. */}
      <span className="min-w-0 flex-[2] truncate font-mono text-[11px]">{op.path}</span>
      {op.summary && (
        <span className="hidden min-w-0 flex-1 truncate text-[11px] text-muted-foreground lg:inline">
          {op.summary}
        </span>
      )}
      {decision === "deny" && !rule && (
        <span className="hidden shrink-0 text-[10px] italic text-muted-foreground lg:inline">
          no rule admits it
        </span>
      )}
      <div className="flex shrink-0 gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
        {(["allow", "deny"] as const).map((bucket) => (
          <Button
            key={bucket}
            type="button"
            variant="ghost"
            size="xs"
            aria-label={`${bucket} ${op.method} ${op.path}`}
            onClick={(e) => {
              e.stopPropagation();
              handlers.setOperation(connection, op, bucket);
            }}
            className={cn("h-5 px-1.5 font-mono text-[10px]", BUCKET_TINT[bucket].action)}
          >
            {bucket}
          </Button>
        ))}
      </div>
    </div>
  );
}

function DecisionIcon({ decision }: { decision: RouteResolution["decision"] }) {
  if (decision === "deny") {
    return <Ban className={cn("size-3.5 shrink-0", BUCKET_TINT.deny.icon)} />;
  }
  if (decision === "allow") {
    return <Check className={cn("size-3.5 shrink-0", BUCKET_TINT.allow.icon)} />;
  }
  // Open is reachable but not by a rule, so it gets its own mark rather than
  // the allow tick: an operator reading a page of ticks would conclude rules
  // are in force where none are.
  return <Minus className="size-3.5 shrink-0 text-muted-foreground" />;
}
