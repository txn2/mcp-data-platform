import { Search } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { BUCKET_TINT } from "./tints";
import type { Scope, StatusFilter } from "./types";

// SCOPE_NOUN names what a scope lists, for the search field's label. The scope
// value itself reads wrong in a sentence ("Search api…").
const SCOPE_NOUN: Record<Scope, string> = {
  tools: "tools",
  connections: "connections",
  api: "API endpoints",
};

// POSITIVE_LABEL names the non-denied half of a tally. The api scope says
// "reachable" because its non-denied half includes the operations no rule
// names, and calling those "allowed" would claim a rule admits them.
export const POSITIVE_LABEL: Record<Scope, string> = {
  tools: "allowed",
  connections: "allowed",
  api: "reachable",
};

export interface ExplorerCounts {
  allowed: number;
  denied: number;
  total: number;
}

// ExplorerToolbar is everything above the permissions list: what the preview
// answers, the running allow/deny tally, the tools-versus-connections scope,
// and the search and status filters that narrow the list below.
export function ExplorerToolbar({
  personaName,
  counts,
  scope,
  onScopeChange,
  toolCount,
  connectionCount,
  apiOperationCount,
  search,
  onSearchChange,
  statusFilter,
  onStatusFilterChange,
}: {
  personaName: string;
  counts: ExplorerCounts;
  scope: Scope;
  onScopeChange: (s: Scope) => void;
  toolCount: number;
  connectionCount: number;
  apiOperationCount: number;
  search: string;
  onSearchChange: (s: string) => void;
  statusFilter: StatusFilter;
  onStatusFilterChange: (f: StatusFilter) => void;
}) {
  return (
    <>
      <div className="border-b bg-muted/10 px-5 pb-3 pt-4">
        <div className="mb-3">
          <h3 className="text-base font-semibold leading-tight">
            What can {personaName || "this persona"} do?
          </h3>
          <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px]">
            <p className="text-muted-foreground">
              Live preview. Updates as you edit allow / deny patterns.
            </p>
            <div className="flex items-center gap-3">
              <span className="flex items-center gap-1.5">
                <span className="h-2 w-2 rounded-full bg-emerald-500" />
                <strong className="font-mono text-foreground">{counts.allowed}</strong>
                <span className="text-muted-foreground">{POSITIVE_LABEL[scope]}</span>
              </span>
              <span className="flex items-center gap-1.5">
                <span className="h-2 w-2 rounded-full bg-rose-500" />
                <strong className="font-mono text-foreground">{counts.denied}</strong>
                <span className="text-muted-foreground">denied</span>
              </span>
            </div>
          </div>
        </div>
        <Tabs
          value={scope}
          onValueChange={(v) => onScopeChange(v as Scope)}
          className="-mb-3"
        >
          <TabsList variant="line" className="border-b">
            <ScopeTrigger value="tools" label="Tools" count={toolCount} />
            <ScopeTrigger
              value="connections"
              label="Connections"
              count={connectionCount}
            />
            <ScopeTrigger
              value="api"
              label="API endpoints"
              count={apiOperationCount}
            />
          </TabsList>
        </Tabs>
      </div>

      <div className="flex items-center gap-2 border-b px-5 py-2.5">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            value={search}
            onChange={(e) => onSearchChange(e.target.value)}
            placeholder={`Search ${SCOPE_NOUN[scope]}…`}
            aria-label={`Search ${SCOPE_NOUN[scope]}`}
            className="h-8 pl-8 font-mono text-[11px]"
          />
        </div>
        <Tabs
          value={statusFilter}
          onValueChange={(v) => onStatusFilterChange(v as StatusFilter)}
        >
          <TabsList className="h-8">
            <TabsTrigger value="all" className="text-[11px]">
              All
            </TabsTrigger>
            <TabsTrigger value="allowed" className="text-[11px]">
              <span className={BUCKET_TINT.allow.text}>{counts.allowed}</span>{" "}
              {POSITIVE_LABEL[scope]}
            </TabsTrigger>
            <TabsTrigger value="denied" className="text-[11px]">
              <span className={BUCKET_TINT.deny.text}>{counts.denied}</span> denied
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </div>
    </>
  );
}

function ScopeTrigger({
  value,
  label,
  count,
}: {
  value: Scope;
  label: string;
  count: number;
}) {
  return (
    <TabsTrigger value={value} className="text-xs">
      {label}
      <Badge variant="muted" className="rounded px-1.5 font-mono text-[10px]">
        {count}
      </Badge>
    </TabsTrigger>
  );
}
