import { useState } from "react";
import { Zap } from "lucide-react";
import { useAdminHandlers, type AdminHandlerList, type AdminHandlerListParams } from "@/api/admin/hooks/handlers";
import type { HandlerRow } from "@/api/portal/hooks/scriptHandlers";
import { EmptyState } from "@/components/patterns/EmptyState";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { Pager } from "@/components/patterns/Pager";
import { SearchInput } from "@/components/patterns/SearchInput";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { sortNames } from "@/lib/sortByName";
import { useDebounced } from "@/lib/useDebounced";
import { describeCron } from "./cadence";
import { HandlerTypeBadge } from "./HandlerTypeBadge";
import { agoText } from "./listingFormat";
import { formatWhen } from "./runFormat";

// HandlersTab is every handler of every script (#2435): what starts each
// automation on the deployment, filtered by type, script, owner and state. A
// row opens its script, whose Handlers section acts on it. The server filters
// and pages the list.

export const HANDLERS_PER_PAGE = 50;

/** HANDLERS_PATH is the tab's own address, so a back link lands on it. */
export const HANDLERS_PATH = "/admin/automations/handlers";

const TYPE_OPTIONS = [
  { value: "", label: "All types" },
  { value: "endpoint", label: "Endpoint" },
  { value: "form", label: "Form" },
  { value: "schedule", label: "Schedule" },
  { value: "webhook", label: "Webhook" },
  { value: "policy", label: "Policy" },
  { value: "transform", label: "Transform" },
  { value: "watch", label: "Watch" },
];

const STATE_OPTIONS = [
  { value: "", label: "On and off" },
  { value: "on", label: "On" },
  { value: "off", label: "Off" },
];

export function HandlersTab({ onNavigate }: { onNavigate: (path: string) => void }) {
  const [type, setType] = useState<AdminHandlerListParams["type"]>("");
  const [state, setState] = useState<AdminHandlerListParams["state"]>("");
  const [owner, setOwner] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const debounced = useDebounced(search, 250);
  const { data, isLoading, isError } = useAdminHandlers({
    type,
    state,
    owner,
    search: debounced,
    page,
    perPage: HANDLERS_PER_PAGE,
  });
  // A filter change starts again at the first page.
  const refilter = <T,>(set: (v: T) => void) => (v: T) => {
    set(v);
    setPage(1);
  };
  const owners = sortNames(new Set((data?.data ?? []).map((r) => r.owner_email).filter(Boolean)));
  const filtered = type !== "" || state !== "" || owner !== "" || debounced.trim() !== "";

  return (
    <div className="space-y-3">
      <HandlerFilters
        search={search}
        type={type}
        owner={owner}
        state={state}
        owners={owners}
        onSearch={refilter(setSearch)}
        onType={refilter(setType)}
        onOwner={refilter(setOwner)}
        onState={refilter(setState)}
      />
      {isError && (
        <Alert variant="destructive">
          <AlertDescription>The handlers could not be read.</AlertDescription>
        </Alert>
      )}
      {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}
      {data && (
        <HandlerResults
          data={data}
          filtered={filtered}
          page={page}
          onPage={setPage}
          onOpen={(r) => onNavigate(`/admin/automations/${encodeURIComponent(r.script_id)}`)}
        />
      )}
    </div>
  );
}

// HandlerFilters is the tab's filter bar: the search and the type, owner and
// state facets, each read by the server.
function HandlerFilters({
  search,
  type,
  owner,
  state,
  owners,
  onSearch,
  onType,
  onOwner,
  onState,
}: {
  search: string;
  type: AdminHandlerListParams["type"];
  owner: string;
  state: AdminHandlerListParams["state"];
  owners: string[];
  onSearch: (v: string) => void;
  onType: (v: AdminHandlerListParams["type"]) => void;
  onOwner: (v: string) => void;
  onState: (v: AdminHandlerListParams["state"]) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <SearchInput
        aria-label="Search by script or target"
        placeholder="Search by script or target"
        value={search}
        onChange={(e) => onSearch(e.target.value)}
        className="w-full sm:w-64"
      />
      <FilterSelect
        label="Filter by type"
        value={type}
        onChange={(v) => onType(v as AdminHandlerListParams["type"])}
        options={TYPE_OPTIONS}
      />
      <FilterSelect
        label="Filter by owner"
        value={owner}
        onChange={onOwner}
        options={[{ value: "", label: "All owners" }, ...owners.map((o) => ({ value: o, label: o }))]}
      />
      <FilterSelect
        label="Filter by state"
        value={state}
        onChange={(v) => onState(v as AdminHandlerListParams["state"])}
        options={STATE_OPTIONS}
      />
    </div>
  );
}

// HandlerResults is one page of the handlers the filters match, with the
// pager, or the sentence saying no automation has a handler yet.
function HandlerResults({
  data,
  filtered,
  page,
  onPage,
  onOpen,
}: {
  data: AdminHandlerList;
  filtered: boolean;
  page: number;
  onPage: (page: number) => void;
  onOpen: (r: HandlerRow) => void;
}) {
  if (data.total === 0 && !filtered) {
    return (
      <EmptyState icon={Zap}>
        No automation has a handler yet. A script runs only when someone asks until it is given an endpoint, a
        schedule or a webhook source, on its own page.
      </EmptyState>
    );
  }
  return (
    <>
      <HandlerTable rows={data.data} onOpen={onOpen} />
      {data.total > HANDLERS_PER_PAGE && (
        <Pager page={page} perPage={HANDLERS_PER_PAGE} total={data.total} onPage={onPage} />
      )}
    </>
  );
}

function HandlerTable({ rows, onOpen }: { rows: HandlerRow[]; onOpen: (r: HandlerRow) => void }) {
  if (rows.length === 0) {
    return <p className="text-sm text-muted-foreground">No handler matches these filters.</p>;
  }
  return (
    <div className="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Type</TableHead>
            <TableHead>Target</TableHead>
            <TableHead>Automation</TableHead>
            <TableHead>Owner</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Created</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((r) => (
            <TableRow
              key={r.id}
              className="cursor-pointer"
              onClick={() => onOpen(r)}
              data-testid="admin-handler-row"
            >
              <TableCell>
                <HandlerTypeBadge type={r.type} />
              </TableCell>
              <TableCell className="max-w-[18rem] truncate text-sm" title={r.target}>
                {targetText(r)}
              </TableCell>
              <TableCell className="max-w-[16rem] truncate text-sm" title={r.script_name}>
                <span className="font-mono text-xs">{r.script_name}</span>
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">{r.owner_email || "nobody"}</TableCell>
              <TableCell>
                <Badge variant={r.enabled ? "success" : "muted"}>{r.enabled ? "On" : "Off"}</Badge>
              </TableCell>
              <TableCell
                className="text-xs whitespace-nowrap text-muted-foreground"
                title={`${formatWhen(r.created_at)}${r.created_by ? ` by ${r.created_by}` : ""}`}
              >
                {agoText(r.created_at)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

// targetText is what a handler answers to, in words: an endpoint's address, a
// schedule's cadence, a webhook handler's source.
function targetText(r: HandlerRow): string {
  switch (r.kind) {
    case "endpoint":
      return `/endpoints/${r.target}`;
    case "schedule": {
      // The target is the cron expression and the zone, which holds no space.
      const at = r.target.lastIndexOf(" ");
      return at < 0 ? r.target : describeCron(r.target.slice(0, at), r.target.slice(at + 1));
    }
    case "webhook":
      return `Each event of ${r.target}`;
    default:
      // A policy, transform or watch: its connection and what it matches.
      return r.target;
  }
}
