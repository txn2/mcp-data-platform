import { useState } from "react";
import { Plug } from "lucide-react";
import {
  useBindIntegration,
  useClearIntegration,
  useIntegrations,
  useIntegrationScripts,
  useRetryIntegration,
} from "@/api/admin/hooks";
import type { IntegrationCandidate, IntegrationItem, IntegrationOverview } from "@/api/admin/types";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { compareOrdinal } from "@/lib/sortByName";
import { SettingsCard } from "./panels";

// IntegrationsCard is Admin > Settings > Integrations (#2659): each operation
// only the deployment's environment knows how to do, the script chosen for
// it, how its last run went and each item's status. With no script chosen the
// platform does nothing more than it does today. The run error is shown here,
// to administrators, and never to the owners of the items.

const ITEMS_SHOWN = 3;

const STATUS: Record<IntegrationItem["status"], { label: string; variant: "muted" | "success" | "warning" }> = {
  pending: { label: "Pending", variant: "muted" },
  ready: { label: "Ready", variant: "success" },
  failed: { label: "Failed", variant: "warning" },
};

export function IntegrationsCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data, isLoading, error } = useIntegrations();
  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold">Integrations</h2>
        <p className="max-w-3xl text-sm text-muted-foreground">
          Work only your environment knows how to do. Choose a script for an operation and the platform calls it; with none chosen the platform does nothing more than it does today.
        </p>
      </div>
      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {error && <p className="text-sm text-destructive">The integrations could not be read.</p>}
      {data?.integrations.map((op) => <OperationCard key={op.operation} op={op} isReadOnly={isReadOnly} />)}
    </div>
  );
}

function OperationCard({ op, isReadOnly }: { op: IntegrationOverview; isReadOnly: boolean }) {
  const [picking, setPicking] = useState(false);
  const failed = op.binding?.last_status === "failed" && !op.binding.current_run_id;
  return (
    <SettingsCard
      icon={Plug}
      title={op.label}
      description={describe(op)}
      action={<StateBadge op={op} failed={failed} />}
      notices={failed && op.binding ? <LastRunFailed op={op} /> : undefined}
    >
      <div className="space-y-4">
        {op.binding ? <BoundFacts op={op} /> : <UnboundFacts op={op} />}
        {op.binding && op.items.length > 0 && <ItemsTable items={op.items} total={op.total} keyField={op.key_field} />}
        {!isReadOnly && <Actions op={op} failed={failed} onPick={() => setPicking(true)} />}
      </div>
      {picking && <PickerDialog op={op} onClose={() => setPicking(false)} />}
    </SettingsCard>
  );
}

function describe(op: IntegrationOverview): string {
  if (op.operation === "site_routing") {
    return "Called with every hostname the platform serves for sites and applications, when one is added or removed and on a timer, so your ingress can route it.";
  }
  return `Called with every ${op.items_field} entry when one changes and on a timer.`;
}

function StateBadge({ op, failed }: { op: IntegrationOverview; failed: boolean }) {
  if (!op.binding) return <Badge variant="muted">No script</Badge>;
  if (failed) return <Badge variant="warning">Last run failed</Badge>;
  return <Badge variant="info">On</Badge>;
}

function LastRunFailed({ op }: { op: IntegrationOverview }) {
  const b = op.binding!;
  return (
    <Alert className="rounded-none border-x-0 border-t-0 border-amber-200 bg-amber-50 dark:border-amber-500/30 dark:bg-amber-500/10">
      <AlertTitle>The script failed{b.last_finished_at ? ` at ${when(b.last_finished_at)}` : ""}. Every item in that call is marked failed.</AlertTitle>
      <AlertDescription className="space-y-1">
        {b.last_error && <pre className="whitespace-pre-wrap break-words font-mono text-xs">{b.last_error}</pre>}
        <p className="text-xs">
          Only administrators see this error. Owners see &ldquo;The site routing script failed&rdquo; and when.{" "}
          {b.last_run_id && (
            <a className="text-primary underline-offset-4 hover:underline" href={`/portal/automations/${b.script_id}/runs/${b.last_run_id}`}>
              Open the run
            </a>
          )}
        </p>
      </AlertDescription>
    </Alert>
  );
}

function UnboundFacts({ op }: { op: IntegrationOverview }) {
  return (
    <dl className="grid grid-cols-[10rem_1fr] gap-x-4 gap-y-2 text-sm">
      <dt className="text-muted-foreground">Script declares</dt>
      <dd>
        <code className="font-mono" data-testid="integration-declaration">
          INTEGRATION = &quot;{op.operation}&quot;
        </code>
      </dd>
      <dt className="text-muted-foreground">Script defines</dt>
      <dd>
        <code className="font-mono">{op.function}(req)</code>
      </dd>
      <dt className="text-muted-foreground">Items today</dt>
      <dd>{op.total}</dd>
    </dl>
  );
}

function BoundFacts({ op }: { op: IntegrationOverview }) {
  const b = op.binding!;
  return (
    <dl className="grid grid-cols-[10rem_1fr] gap-x-4 gap-y-2 text-sm">
      <dt className="text-muted-foreground">Script</dt>
      <dd>
        <a className="font-medium text-primary underline-offset-4 hover:underline" href={`/portal/automations/${b.script_id}`}>
          {op.script_name || b.script_id}
        </a>
        {op.running_version ? (
          <span className="text-muted-foreground">
            {" "}
            · version {op.running_version}, approved for handlers{op.approved_by ? ` by ${op.approved_by}` : ""}
          </span>
        ) : (
          <span className="text-muted-foreground"> · no version is approved for handlers</span>
        )}
      </dd>
      <dt className="text-muted-foreground">Runs</dt>
      <dd>every {minutes(b.interval_seconds)}, and whenever an item is added or removed</dd>
      <dt className="text-muted-foreground">Last run</dt>
      <dd>{lastRun(op)}</dd>
      <dt className="text-muted-foreground">Items</dt>
      <dd className="flex flex-wrap gap-1.5">
        {(["ready", "pending", "failed"] as const).map((st) =>
          op.counts[st] ? (
            <Badge key={st} variant={STATUS[st].variant}>
              {op.counts[st]} {STATUS[st].label.toLowerCase()}
            </Badge>
          ) : null,
        )}
      </dd>
    </dl>
  );
}

function lastRun(op: IntegrationOverview): string {
  const b = op.binding!;
  if (b.current_run_id) return "Running now";
  if (!b.last_status) return "Not run yet";
  const at = b.last_finished_at ? ` ${when(b.last_finished_at)}` : "";
  const why = b.last_reason === "timer" ? ", on the timer" : b.last_reason === "change" ? ", after an item changed" : "";
  return `${b.last_status === "succeeded" ? "Succeeded" : "Failed"}${at}${why}`;
}

function ItemsTable({ items, total, keyField }: { items: IntegrationItem[]; total: number; keyField: string }) {
  const [all, setAll] = useState(false);
  const failedFirst = [...items].sort((a, b) => order(a) - order(b) || compareOrdinal(a.key, b.key));
  const shown = all ? failedFirst : failedFirst.slice(0, ITEMS_SHOWN);
  return (
    <div className="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{keyField === "hostname" ? "Hostname" : "Item"}</TableHead>
            <TableHead>For</TableHead>
            <TableHead>Status</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {shown.map((it) => (
            <TableRow key={it.key}>
              <TableCell className="break-all font-mono text-xs">{it.key}</TableCell>
              <TableCell className="text-sm text-muted-foreground">{forText(it)}</TableCell>
              <TableCell>
                <span className="flex flex-wrap items-baseline gap-2">
                  <Badge variant={STATUS[it.status].variant}>{STATUS[it.status].label}</Badge>
                  <span className="break-words text-sm">{it.message}</span>
                </span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {!all && items.length > ITEMS_SHOWN && (
        <div className="border-t px-3 py-2 text-sm">
          <Button variant="link" className="h-auto p-0" onClick={() => setAll(true)}>
            Show all {Math.max(total, items.length)}
          </Button>
        </div>
      )}
    </div>
  );
}

const order = (it: IntegrationItem): number => ({ failed: 0, pending: 1, ready: 2 })[it.status];

function forText(it: IntegrationItem): string {
  const e = it.entry;
  const parts = [e.site, e.environment, e.application].filter((x): x is string => typeof x === "string" && x !== "");
  const kind = e.kind === "domain" ? (e.public ? "public domain" : "domain") : e.kind === "application" ? "page origin" : "";
  return [...parts, kind].filter(Boolean).join(" · ");
}

function Actions({ op, failed, onPick }: { op: IntegrationOverview; failed: boolean; onPick: () => void }) {
  const retry = useRetryIntegration(op.operation);
  const clear = useClearIntegration(op.operation);
  if (!op.binding) {
    return (
      <div className="space-y-3 border-t pt-4">
        <p className="text-sm text-muted-foreground">
          A script declares INTEGRATION = &quot;{op.operation}&quot; at its top level and defines {op.function}(req), which receives the reason (change or timer), the entries added and removed since its last run, and every entry under {op.items_field}. It returns a dict keyed by {op.key_field}, each value {"{"}&quot;status&quot;: &quot;ready&quot; or &quot;failed&quot;, &quot;message&quot;: &quot;…&quot;{"}"}.
        </p>
        <Button onClick={onPick}>Choose script</Button>
      </div>
    );
  }
  return (
    <div className="flex flex-wrap gap-2 border-t pt-4">
      <Button variant={failed ? "default" : "outline"} onClick={() => retry.mutate()} disabled={retry.isPending}>
        Retry now
      </Button>
      <Button variant="outline" onClick={onPick}>
        Change script
      </Button>
      <Button variant="outline" className="text-destructive" onClick={() => clear.mutate()} disabled={clear.isPending}>
        Clear
      </Button>
    </div>
  );
}

// usePicker is the picker's state: the search, the script picked, the
// interval, whether to approve its version, and the request it sends.
function usePicker(op: IntegrationOverview, onClose: () => void) {
  const start = initial(op);
  const [search, setSearch] = useState("");
  const [picked, setPicked] = useState(start.picked);
  const [every, setEvery] = useState(start.every);
  const [approve, setApprove] = useState(true);
  const scripts = useIntegrationScripts(op.operation, search, true);
  const bind = useBindIntegration(op.operation);
  const list = scripts.data?.scripts ?? [];
  const state = pickerState({ list, picked, search, loaded: scripts.isSuccess, approve, pending: bind.isPending });
  const submit = () =>
    bind.mutate({ script_id: picked, interval_seconds: Math.round(Number(every) * 60), approve: state.needsApproval && approve }, { onSuccess: onClose });
  return { search, setSearch, picked, setPicked, every, setEvery, approve, setApprove, list, ...state, submit, bind };
}

// initial is what the picker starts on: the script chosen now and its interval.
function initial(op: IntegrationOverview): { picked: string; every: string } {
  return { picked: op.binding?.script_id ?? "", every: String(Math.round((op.binding?.interval_seconds ?? 900) / 60)) };
}

// pickerState is what the picker shows from what is picked: the choice,
// whether its version needs approving, whether nothing can be offered, and
// whether the request can be sent.
function pickerState(p: { list: IntegrationCandidate[]; picked: string; search: string; loaded: boolean; approve: boolean; pending: boolean }) {
  const choice = p.list.find((c) => c.id === p.picked);
  const needsApproval = !!choice && !approvedServes(choice);
  const empty = p.loaded && p.list.length === 0 && !p.search;
  const blocked = !p.picked || p.pending || (needsApproval && !p.approve);
  return { choice, needsApproval, empty, blocked };
}

// approvedServes reports whether a script's latest version is the one approved
// for handlers and defines the function: nothing to approve.
function approvedServes(c: IntegrationCandidate): boolean {
  return c.running_version !== 0 && c.running_serves && c.running_version === c.version;
}

function PickerDialog({ op, onClose }: { op: IntegrationOverview; onClose: () => void }) {
  const p = usePicker(op, onClose);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Choose the {op.label.toLowerCase()} script</DialogTitle>
          <DialogDescription>
            Only scripts whose source defines <code className="font-mono">{op.function}(req)</code> are listed.
          </DialogDescription>
        </DialogHeader>
        {p.empty ? (
          <NoScripts op={op} />
        ) : (
          <PickerBody
            op={op}
            list={p.list}
            search={p.search}
            onSearch={p.setSearch}
            picked={p.picked}
            onPick={p.setPicked}
            choice={p.choice}
            needsApproval={p.needsApproval}
            approve={p.approve}
            onApprove={p.setApprove}
            every={p.every}
            onEvery={p.setEvery}
          />
        )}
        {p.bind.error && <p className="text-sm text-destructive">{errorText(p.bind.error)}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {p.empty ? "Close" : "Cancel"}
          </Button>
          {!p.empty && (
            <Button onClick={p.submit} disabled={p.blocked}>
              Use this script
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function errorText(e: unknown): string {
  return e instanceof Error && e.message ? e.message : "The script was not chosen.";
}

function NoScripts({ op }: { op: IntegrationOverview }) {
  return (
    <div className="space-y-2 rounded-md border border-dashed p-5" data-testid="integration-no-scripts">
      <p className="font-medium">
        No script declares INTEGRATION = &quot;{op.operation}&quot; and defines {op.function}(req)
      </p>
      <p className="text-sm text-muted-foreground">
        A {op.label.toLowerCase()} script declares <code className="font-mono">INTEGRATION = &quot;{op.operation}&quot;</code> and defines a top-level <code className="font-mono">{op.function}(req)</code>. It is handed every item the platform has and the ones added and removed since it last ran, and returns ready or failed with a message for each.
      </p>
      <a className="text-sm text-primary underline-offset-4 hover:underline" href="/portal/automations">
        New automation
      </a>
    </div>
  );
}

function PickerBody(p: {
  op: IntegrationOverview;
  list: IntegrationCandidate[];
  search: string;
  onSearch: (v: string) => void;
  picked: string;
  onPick: (id: string) => void;
  choice?: IntegrationCandidate;
  needsApproval: boolean;
  approve: boolean;
  onApprove: (v: boolean) => void;
  every: string;
  onEvery: (v: string) => void;
}) {
  return (
    <div className="space-y-4">
      <Label htmlFor="integration-search" className="sr-only">
        Search scripts
      </Label>
      <Input id="integration-search" type="search" placeholder="Search scripts" value={p.search} onChange={(e) => p.onSearch(e.target.value)} />
      <fieldset className="max-h-64 overflow-y-auto rounded-md border">
        <legend className="sr-only">Script</legend>
        {p.list.map((c) => (
          <label key={c.id} className={`flex cursor-pointer gap-3 border-b px-3 py-2 last:border-b-0 ${p.picked === c.id ? "bg-accent" : ""}`}>
            <input type="radio" name="integration-script" checked={p.picked === c.id} onChange={() => p.onPick(c.id)} className="mt-1" />
            <span className="flex flex-col">
              <span className="font-medium">{c.name}</span>
              <span className="text-xs text-muted-foreground">{candidateMeta(c)}</span>
            </span>
          </label>
        ))}
      </fieldset>
      {p.choice && p.needsApproval && (
        <div className="space-y-2 rounded-md border border-emerald-200 bg-emerald-50 p-3 text-sm dark:border-emerald-500/30 dark:bg-emerald-500/10">
          <p>
            Version {p.choice.version} defines {p.op.function}(req). It has not been approved for handlers.
          </p>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={p.approve} onChange={(e) => p.onApprove(e.target.checked)} />
            Approve version {p.choice.version} for handlers as you. It will run with your roles.
          </label>
        </div>
      )}
      <div className="space-y-1.5">
        <Label htmlFor="integration-every">Call it with every item every</Label>
        <div className="flex items-center gap-2 text-sm">
          <Input id="integration-every" className="w-20" inputMode="numeric" value={p.every} onChange={(e) => p.onEvery(e.target.value)} />
          <span>minutes, and whenever an item is added or removed</span>
        </div>
      </div>
      <p className="rounded-md border bg-muted/40 px-3 py-2 text-sm">
        It will be handed the {p.op.total} {p.op.key_field === "hostname" ? "hostnames" : "items"} the platform has now, then each one added or removed.
      </p>
    </div>
  );
}

function candidateMeta(c: IntegrationCandidate): string {
  const approval =
    c.running_version === 0
      ? "not approved for handlers"
      : `version ${c.running_version} approved for handlers${c.approved_by ? ` by ${c.approved_by}` : ""}`;
  return `${c.owner_email} · version ${c.version} · ${approval}`;
}

function minutes(seconds: number): string {
  const m = Math.round(seconds / 60);
  return m === 1 ? "minute" : `${m} minutes`;
}

const dateTime = new Intl.DateTimeFormat("en-US", { month: "2-digit", day: "2-digit", hour: "numeric", minute: "2-digit" });

function when(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : dateTime.format(d).replace(",", "");
}
