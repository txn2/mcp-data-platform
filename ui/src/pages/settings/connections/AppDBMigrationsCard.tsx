import { useState } from "react";
import { AlertCircle, ListChecks } from "lucide-react";
import {
  useAppDBMigrations,
  usePlanAppDBMigrations,
  type AppDBMigration,
  type AppDBMigrationEvent,
  type AppDBMigrationPlan,
  type AppDBMigrationReport,
} from "@/api/admin/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

// AppDBMigrationsCard is an application database's migrations (#2404): the
// ones the platform keeps for the connection's schema, which the target's
// ledger records as applied, the plan of the pending ones, and the history.
// Migrations are added and applied with manage_schema; this card reads them.

function formatWhen(value?: string): string {
  if (!value) return "";
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? value : at.toLocaleString();
}

const STATE_VARIANT: Record<AppDBMigration["state"], "default" | "secondary" | "destructive"> = {
  applied: "default",
  pending: "secondary",
  drift: "destructive",
};

function MigrationRow({ m }: { m: AppDBMigration }) {
  return (
    <tr className="border-t align-top">
      <td className="py-1.5 pr-3 font-mono text-xs">{m.version}</td>
      <td className="py-1.5 pr-3">
        <div>{m.name}</div>
        <details className="text-xs">
          <summary className="cursor-pointer text-muted-foreground">SQL</summary>
          <pre className="mt-1 max-h-48 overflow-auto whitespace-pre-wrap rounded bg-muted p-2 font-mono text-[11px]">
            {m.up}
          </pre>
          {m.down && (
            <pre className="mt-1 max-h-48 overflow-auto whitespace-pre-wrap rounded bg-muted p-2 font-mono text-[11px]">
              {m.down}
            </pre>
          )}
        </details>
      </td>
      <td className="py-1.5 pr-3">
        <Badge variant={STATE_VARIANT[m.state]}>{m.state}</Badge>
        {!m.transactional && (
          <Badge variant="outline" className="ml-1">
            outside a transaction
          </Badge>
        )}
      </td>
      <td className="py-1.5 text-xs text-muted-foreground">
        {m.applied_at ? `${formatWhen(m.applied_at)}${m.applied_by ? ` by ${m.applied_by}` : ""}` : ""}
      </td>
    </tr>
  );
}

function PlanResult({ plan }: { plan: AppDBMigrationPlan }) {
  if (plan.migrations.length === 0 && plan.unplanned.length === 0) {
    return <p className="text-xs text-muted-foreground">Nothing is pending.</p>;
  }
  return (
    <div className="space-y-2 text-xs" data-testid="appdb-plan">
      <div>
        <span className="font-medium">Changes</span>
        <ul className="ml-4 list-disc font-mono">
          {plan.changed.map((c) => (
            <li key={c}>{c}</li>
          ))}
        </ul>
      </div>
      <div>
        <span className="font-medium">Locks</span>
        <ul className="ml-4 list-disc font-mono">
          {plan.locks.map((l) => (
            <li key={`${l.relation}-${l.mode}`}>
              {l.relation}: {l.mode}
            </li>
          ))}
        </ul>
      </div>
      {plan.unplanned.length > 0 && (
        <p className="text-muted-foreground">
          Not planned, because they run outside a transaction: {plan.unplanned.join(", ")}
        </p>
      )}
    </div>
  );
}

function History({ events }: { events: AppDBMigrationEvent[] }) {
  if (events.length === 0) return null;
  return (
    <div>
      <div className="mb-1 text-xs font-medium">History</div>
      <ul className="space-y-0.5 text-xs">
        {events.map((e, i) => (
          <li key={`${e.at}-${i}`} className="flex flex-wrap gap-2">
            <span className="text-muted-foreground">{formatWhen(e.at)}</span>
            <span className="font-mono">{e.version}</span>
            <span>{e.event}</span>
            {e.actor && <span className="text-muted-foreground">{e.actor}</span>}
            {e.event !== "added" && e.event !== "edited" && e.duration_ms > 0 && (
              <span className="text-muted-foreground">{e.duration_ms} ms</span>
            )}
            {e.detail && <span className="font-mono text-destructive">{e.detail}</span>}
          </li>
        ))}
      </ul>
    </div>
  );
}

function MigrationTable({ migrations }: { migrations: AppDBMigration[] }) {
  if (migrations.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        No migrations yet. Add them with manage_schema.
      </p>
    );
  }
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs text-muted-foreground">
        <tr>
          <th className="pb-1 pr-3 font-normal">Version</th>
          <th className="pb-1 pr-3 font-normal">Name</th>
          <th className="pb-1 pr-3 font-normal">State</th>
          <th className="pb-1 font-normal">Applied</th>
        </tr>
      </thead>
      <tbody>
        {migrations.map((m) => (
          <MigrationRow key={m.version} m={m} />
        ))}
      </tbody>
    </table>
  );
}

function ReportBody({
  data,
  plan,
  planError,
}: {
  data: AppDBMigrationReport;
  plan?: AppDBMigrationPlan;
  planError: Error | null;
}) {
  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">
        Schema <span className="font-mono">{data.schema}</span> at version{" "}
        <span className="font-medium">{data.current_version}</span>, recorded in{" "}
        <span className="font-mono">{data.ledger}</span>.
      </p>
      {data.drift.map((d) => (
        <Alert key={d.version} variant="destructive">
          <AlertCircle />
          <AlertDescription>
            Version {d.version}: {d.reason}
          </AlertDescription>
        </Alert>
      ))}
      <MigrationTable migrations={data.migrations} />
      {planError && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{planError.message}</AlertDescription>
        </Alert>
      )}
      {plan && <PlanResult plan={plan} />}
      <History events={data.events} />
    </div>
  );
}

export function AppDBMigrationsCard({ connectionName }: { connectionName: string }) {
  const [schema] = useState("public");
  const { data, error, isLoading } = useAppDBMigrations(connectionName, schema, true);
  const plan = usePlanAppDBMigrations(connectionName, schema);
  const pending = data?.migrations.filter((m) => m.state === "pending").length ?? 0;

  return (
    <SectionCard
      title="Migrations"
      collapsible
      summary={data ? `version ${data.current_version}, ${pending} pending` : undefined}
      action={
        <Button
          size="sm"
          variant="outline"
          disabled={pending === 0 || plan.isPending}
          onClick={() => plan.mutate()}
        >
          <ListChecks className="size-4" />
          Plan
        </Button>
      }
    >
      {isLoading && <p className="text-xs text-muted-foreground">Loading...</p>}
      {error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
      {data && <ReportBody data={data} plan={plan.data} planError={plan.error} />}
    </SectionCard>
  );
}
