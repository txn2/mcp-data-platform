import { useMemo, useState } from "react";
import { Globe } from "lucide-react";
import { useAdminEndpoints } from "@/api/admin/hooks";
import type { AdminEndpoint, EndpointRange } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { FilterSelect } from "@/components/patterns/FilterSelect";
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
import { ACCESS_LABELS, endpointPath, formatMs } from "./endpointLabels";

// EndpointsTab is every endpoint binding on the deployment with its traffic
// (#2208): what the deployment exposes, under whose automation, and who is
// calling it, without opening automations one at a time. A public endpoint
// answers anyone, so its access is marked plainly. A row opens the endpoint.

export const RANGE_OPTIONS: { value: EndpointRange; label: string }[] = [
  { value: "day", label: "Last 24 hours" },
  { value: "week", label: "Last 7 days" },
];

const ACCESS_OPTIONS = [
  { value: "", label: "All access" },
  { value: "public", label: "Public" },
  { value: "signed_in", label: "Signed in" },
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
  const [access, setAccess] = useState("");
  const [status, setStatus] = useState("");
  const [binding, setBinding] = useState(false);
  const { data, isLoading, isError } = useAdminEndpoints(range);

  const rows = useMemo(
    () =>
      (data?.endpoints ?? []).filter(
        (e) =>
          (access === "" || e.auth_mode === access) &&
          (status === "" || (status === "on") === e.enabled),
      ),
    [data, access, status],
  );

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <FilterSelect
          label="Range"
          value={range}
          onChange={(v) => setRange((v || "day") as EndpointRange)}
          options={RANGE_OPTIONS}
        />
        <FilterSelect
          label="Filter by access"
          value={access}
          onChange={setAccess}
          options={ACCESS_OPTIONS}
        />
        <FilterSelect
          label="Filter by status"
          value={status}
          onChange={setStatus}
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
      {data && data.endpoints.length === 0 && (
        <EmptyState icon={Globe}>
          No automation answers HTTP requests yet. Bind one that defines
          handle(req) to give it an address under /endpoints/.
        </EmptyState>
      )}
      {data && data.endpoints.length > 0 && (
        <EndpointTable
          rows={rows}
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
                <AccessBadge mode={e.auth_mode} />
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

/** AccessBadge marks a public endpoint apart from a signed-in one. */
export function AccessBadge({ mode }: { mode: AdminEndpoint["auth_mode"] }) {
  return (
    <Badge variant={mode === "public" ? "warning" : "muted"} data-access={mode}>
      {ACCESS_LABELS[mode]}
    </Badge>
  );
}
