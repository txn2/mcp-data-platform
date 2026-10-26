import { useState } from "react";
import { Globe } from "lucide-react";
import { useAdminEndpoints } from "@/api/admin/hooks";
import type {
  BotCheckProvider,
  AdminEndpoint,
  AdminEndpointListParams,
  EndpointRange,
} from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { Pager } from "@/components/patterns/Pager";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatWhen } from "../runFormat";
import { BindAutomationDialog } from "./BindAutomationDialog";
import { ACCESS_LABELS, PROVIDER_LABELS, PUBLIC_NOTE, accessLabel, endpointPath, formatMs } from "./endpointLabels";

// EndpointsTab is every endpoint binding on the deployment with its traffic
// (#2208): what the deployment exposes, under whose automation, and who is
// calling it, without opening automations one at a time. A public endpoint
// answers anyone, so its access is marked plainly. A row opens the endpoint.
// The server filters and pages the list, so a deployment with more endpoints
// than a page holds reaches every one (#2251).

export const ENDPOINTS_PER_PAGE = 50;

export const RANGE_OPTIONS: { value: EndpointRange; label: string }[] = [
  { value: "day", label: "Last 24 hours" },
  { value: "week", label: "Last 7 days" },
];

const ACCESS_OPTIONS = [
  { value: "", label: "All access" },
  { value: "authenticated", label: ACCESS_LABELS.authenticated },
  { value: "public", label: ACCESS_LABELS.public },
  { value: "bot_check", label: ACCESS_LABELS.bot_check },
];

const STATUS_OPTIONS = [
  { value: "", label: "On and off" },
  { value: "on", label: "On" },
  { value: "off", label: "Off" },
];

export function EndpointsTab({
  onNavigate,
}: {
  onNavigate: (path: string) => void;
}) {
  const [range, setRange] = useState<EndpointRange>("day");
  const [access, setAccess] = useState<NonNullable<AdminEndpointListParams["access"]>>("");
  const [status, setStatus] = useState<NonNullable<AdminEndpointListParams["status"]>>("");
  const [page, setPage] = useState(1);
  const [binding, setBinding] = useState(false);
  const { data, isLoading, isError } = useAdminEndpoints({
    range,
    access,
    status,
    page,
    perPage: ENDPOINTS_PER_PAGE,
  });
  // A filter change starts again at the first page: page 4 of the old list
  // is rarely a page of the new one.
  const refilter = <T,>(set: (v: T) => void) => (v: T) => {
    set(v);
    setPage(1);
  };

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <FilterSelect
          label="Range"
          value={range}
          onChange={(v) => refilter(setRange)((v || "day") as EndpointRange)}
          options={RANGE_OPTIONS}
        />
        <FilterSelect
          label="Filter by access"
          value={access}
          onChange={(v) => refilter(setAccess)(v as typeof access)}
          options={ACCESS_OPTIONS}
        />
        <FilterSelect
          label="Filter by status"
          value={status}
          onChange={(v) => refilter(setStatus)(v as typeof status)}
          options={STATUS_OPTIONS}
        />
        <div className="ml-auto">
          <Button size="sm" onClick={() => setBinding(true)}>
            Bind an automation
          </Button>
        </div>
      </div>
      {isError && (
        <Alert variant="destructive">
          <AlertDescription>The endpoints could not be read.</AlertDescription>
        </Alert>
      )}
      {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}
      {data && (
        <EndpointPage
          rows={data.endpoints}
          total={data.total}
          filtered={access !== "" || status !== ""}
          page={page}
          onPage={setPage}
          onOpen={(e) => onNavigate(endpointPath(e.name))}
        />
      )}
      <BindAutomationDialog
        open={binding}
        onOpenChange={setBinding}
        onNavigate={onNavigate}
      />
    </div>
  );
}

/**
 * EndpointPage is one page of the list: the table and, when the filters match
 * more than a page holds, the pager; with nothing bound and no filter, the
 * empty state.
 */
function EndpointPage({
  rows,
  total,
  filtered,
  page,
  onPage,
  onOpen,
}: {
  rows: AdminEndpoint[];
  total: number;
  filtered: boolean;
  page: number;
  onPage: (page: number) => void;
  onOpen: (e: AdminEndpoint) => void;
}) {
  if (total === 0 && !filtered) {
    return (
      <EmptyState icon={Globe}>
        No automation answers HTTP requests yet. Bind one that defines
        handle(req) to give it an address under /endpoints/.
      </EmptyState>
    );
  }
  return (
    <>
      <EndpointTable rows={rows} onOpen={onOpen} />
      {total > ENDPOINTS_PER_PAGE && (
        <Pager page={page} perPage={ENDPOINTS_PER_PAGE} total={total} onPage={onPage} />
      )}
    </>
  );
}

function EndpointTable({
  rows,
  onOpen,
}: {
  rows: AdminEndpoint[];
  onOpen: (e: AdminEndpoint) => void;
}) {
  if (rows.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No endpoint matches these filters.
      </p>
    );
  }
  return (
    <div className="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Automation</TableHead>
            <TableHead>Owner</TableHead>
            <TableHead>Access</TableHead>
            <TableHead>Methods</TableHead>
            <TableHead className="text-right">Requests</TableHead>
            <TableHead className="text-right">Failures</TableHead>
            <TableHead className="text-right">Refused</TableHead>
            <TableHead className="text-right">p95</TableHead>
            <TableHead>Last request</TableHead>
            <TableHead>Status</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((e) => (
            <TableRow
              key={e.id}
              className="cursor-pointer"
              onClick={() => onOpen(e)}
            >
              <TableCell className="font-mono text-xs">{e.name}</TableCell>
              <TableCell>
                {e.script.display_name || e.script.name || e.script.id}
              </TableCell>
              <TableCell className="text-xs">{e.script.owner || "-"}</TableCell>
              <TableCell>
                <AccessBadge endpoint={e} />
              </TableCell>
              <TableCell className="font-mono text-xs">
                {e.methods.join(", ")}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {e.traffic.requests.toLocaleString()}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {e.traffic.failures.toLocaleString()}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {e.refused.toLocaleString()}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {formatMs(e.traffic.p95_ms)}
              </TableCell>
              <TableCell className="text-xs">
                {e.traffic.last_request_at
                  ? formatWhen(e.traffic.last_request_at)
                  : "-"}
              </TableCell>
              <TableCell>{e.enabled ? "On" : "Off"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

/**
 * AccessBadge is an endpoint's access mode (#2259): a public endpoint, which
 * answers anyone with no check, is marked apart; a bot_check one names its
 * check and that check's provider.
 */
export function AccessBadge({
  endpoint: e,
}: {
  endpoint: Pick<AdminEndpoint, "auth_mode" | "bot_check" | "bot_check_provider">;
}) {
  const title =
    e.auth_mode === "public"
      ? PUBLIC_NOTE
      : e.auth_mode === "bot_check" && e.bot_check_provider
        ? `Provider: ${PROVIDER_LABELS[e.bot_check_provider as BotCheckProvider] ?? e.bot_check_provider}`
        : undefined;
  return (
    <Badge variant={e.auth_mode === "public" ? "warning" : "muted"} data-access={e.auth_mode} title={title}>
      {accessLabel(e)}
    </Badge>
  );
}
