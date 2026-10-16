import { useState } from "react";
import { useReconciliation, useRepairTables } from "@/api/admin/hooks";
import type { ReconcileOrphan, Reconciliation, RepairItem, RepairOutcome } from "@/api/admin/types";
import { useTableConnections } from "@/api/tables/hooks";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { SectionCard } from "@/components/patterns/SectionCard";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

// ReconcilePage is a connection's scratch schema against its registrations
// (#2136), for administrators: tables no registration holds, registrations
// whose table is gone, and tables reading somewhere their registration does
// not say. Only what the platform can prove is its own can be selected and
// repaired; everything else is shown and left alone.

const LEFT_ALONE = "Not the platform's; left alone";

export function ReconcilePage() {
  const connections = useTableConnections();
  const [picked, setPicked] = useState("");
  const names = (connections.data?.connections ?? []).map((c) => c.name);
  // The first connection until another is chosen: most deployments have one.
  const connection = picked || names[0] || "";
  if (connections.isSuccess && names.length === 0) {
    return <p className="text-sm text-muted-foreground">No connection here holds a scratch schema.</p>;
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <FilterSelect
          label="Connection"
          value={connection}
          onChange={setPicked}
          options={names.map((n) => ({ value: n, label: n }))}
        />
      </div>
      {connection ? <Report key={connection} connection={connection} /> : null}
    </div>
  );
}

/** Report reads one connection's reconciliation and repairs what is chosen. */
function Report({ connection }: { connection: string }) {
  const { data, isLoading, isError, error } = useReconciliation(connection);
  const repair = useRepairTables();
  const [chosen, setChosen] = useState<Record<string, RepairItem>>({});
  const [outcomes, setOutcomes] = useState<RepairOutcome[]>([]);

  if (isLoading) {
    return (
      <div className="flex justify-center py-10">
        <LoadingIndicator />
      </div>
    );
  }
  if (isError || !data) {
    return (
      <Alert variant="destructive" className="py-2">
        <AlertDescription>{(error as Error | null)?.message ?? "The scratch schema could not be read."}</AlertDescription>
      </Alert>
    );
  }

  const toggle = (key: string, item: RepairItem) =>
    setChosen((c) => {
      const next = { ...c };
      if (next[key]) delete next[key];
      else next[key] = item;
      return next;
    });
  const items = Object.values(chosen);
  const submit = () =>
    repair.mutate(
      { connection, repair: items },
      {
        onSuccess: (res) => {
          setOutcomes(res.outcomes);
          setChosen({});
        },
      },
    );

  return (
    <div className="space-y-4">
      {data.truncated && (
        <Alert className="py-2">
          <AlertDescription>
            The schema holds more tables than one listing returns, so the list below is partial.
          </AlertDescription>
        </Alert>
      )}
      <Orphans data={data} chosen={chosen} onToggle={toggle} />
      <Missing data={data} chosen={chosen} onToggle={toggle} />
      <Drifted data={data} />
      <Failed outcomes={outcomes} />
      {repair.error && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>{repair.error.message}</AlertDescription>
        </Alert>
      )}
      <div className="flex justify-end">
        <Button type="button" onClick={submit} disabled={items.length === 0 || repair.isPending}>
          {repair.isPending ? "Repairing..." : "Repair selected"}
        </Button>
      </div>
    </div>
  );
}

type Choose = { chosen: Record<string, RepairItem>; onToggle: (key: string, item: RepairItem) => void };

function Orphans({ data, chosen, onToggle }: { data: Reconciliation } & Choose) {
  return (
    <SectionCard title="Tables with no registration">
      {data.orphans.length === 0 ? (
        <None />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Table</TableHead>
              <TableHead>Reads</TableHead>
              <TableHead>File</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.orphans.map((o) => {
              const key = `drop:${o.table}`;
              return (
                <TableRow key={key}>
                  <TableCell>
                    {o.provable && (
                      <input
                        type="checkbox"
                        aria-label={`Drop ${o.table}`}
                        checked={!!chosen[key]}
                        onChange={() => onToggle(key, { action: "drop", table: o.table })}
                      />
                    )}
                  </TableCell>
                  <TableCell className="font-mono text-xs">{o.table}</TableCell>
                  <TableCell className="font-mono text-xs break-all">{o.location || "-"}</TableCell>
                  <TableCell className="text-xs">{fileOf(o)}</TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
    </SectionCard>
  );
}

/** fileOf says what an orphan's location belongs to. */
function fileOf(o: ReconcileOrphan): string {
  if (!o.provable || !o.source) return LEFT_ALONE;
  const kind = o.source.kind === "resource" ? "Resource" : "Asset";
  return `${kind} ${o.source.id}${o.source.exists ? "" : " (deleted)"}`;
}

function Missing({ data, chosen, onToggle }: { data: Reconciliation } & Choose) {
  return (
    <SectionCard title="Registrations whose table is gone">
      {data.missing.length === 0 ? (
        <None />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Table</TableHead>
              <TableHead>Source</TableHead>
              <TableHead>Registered by</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.missing.map((m) => {
              const r = m.registration;
              const key = `forget:${r.id}`;
              return (
                <TableRow key={key}>
                  <TableCell>
                    {m.provable && (
                      <input
                        type="checkbox"
                        aria-label={`Forget ${r.table}`}
                        checked={!!chosen[key]}
                        onChange={() => onToggle(key, { action: "forget", registration_id: r.id })}
                      />
                    )}
                  </TableCell>
                  <TableCell className="font-mono text-xs">{r.table}</TableCell>
                  <TableCell className="text-xs">
                    {SOURCE_LABELS[r.source_kind]} {r.source_id}
                  </TableCell>
                  <TableCell className="text-xs">{r.registered_by}</TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
    </SectionCard>
  );
}

const SOURCE_LABELS: Record<string, string> = { resource: "Resource", asset: "Asset", webhook: "Webhook source" };

function Drifted({ data }: { data: Reconciliation }) {
  return (
    <SectionCard title="Tables reading somewhere else">
      {data.drifted.length === 0 ? (
        <None />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Table</TableHead>
              <TableHead>Registered at</TableHead>
              <TableHead>Reads</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.drifted.map((d) => (
              <TableRow key={d.registration.id}>
                <TableCell className="font-mono text-xs">{d.registration.table}</TableCell>
                <TableCell className="font-mono text-xs break-all">{d.registration.location}</TableCell>
                <TableCell className="font-mono text-xs break-all">{d.location}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </SectionCard>
  );
}

/** Failed lists the repairs the last request asked for that were not made,
 * with the reason each gave. */
function Failed({ outcomes }: { outcomes: RepairOutcome[] }) {
  const failed = outcomes.filter((o) => !o.done);
  if (failed.length === 0) return null;
  return (
    <Alert variant="destructive" className="py-2">
      <AlertDescription>
        <ul className="list-disc space-y-1 pl-4">
          {failed.map((o) => (
            <li key={`${o.action}:${o.table ?? o.registration_id}`}>
              <span className="font-mono">{o.table ?? o.registration_id}</span>: {o.reason}
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  );
}

function None() {
  return <p className="text-xs text-muted-foreground">None.</p>;
}
