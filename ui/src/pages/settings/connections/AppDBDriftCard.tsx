import { useState } from "react";
import { AlertCircle } from "lucide-react";
import {
  useAdoptAppDBSchema,
  useAppDBDrift,
  type AppDBAdoptResult,
  type AppDBDifference,
  type AppDBDriftResult,
} from "@/api/admin/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

// AppDBDriftCard is an application database's schema against what its applied
// migrations build (#2406), checked when the card opens, and adopting a schema
// the platform keeps no migrations for: it is written as a baseline migration
// marked applied, once a throwaway database built from it differs from the
// live schema in nothing.

const CHANGE_VARIANT: Record<
  AppDBDifference["change"],
  "destructive" | "secondary" | "outline"
> = {
  added: "destructive",
  missing: "secondary",
  changed: "outline",
};

function Differences({ diffs }: { diffs: AppDBDifference[] }) {
  return (
    <ul className="space-y-1 text-xs" data-testid="appdb-drift-differences">
      {diffs.map((d) => (
        <li key={`${d.kind}-${d.object}-${d.change}`}>
          <Badge variant={CHANGE_VARIANT[d.change]}>{d.change}</Badge>{" "}
          <span className="text-muted-foreground">{d.kind}</span>{" "}
          <span className="font-mono">{d.object}</span>
          {d.change === "changed" && (
            <div className="ml-4 font-mono text-[11px] text-muted-foreground">
              <div>live: {d.live}</div>
              <div>migrations: {d.expected}</div>
            </div>
          )}
        </li>
      ))}
    </ul>
  );
}

function SchemaResult({ r }: { r: AppDBDriftResult }) {
  return (
    <div className="space-y-1">
      <div className="text-xs">
        Schema <span className="font-mono">{r.schema}</span>
        <span className="text-muted-foreground">
          {" "}
          through version {r.applied_version}
        </span>
      </div>
      {r.differences.length === 0 && (
        <p className="text-xs text-muted-foreground">
          Matches what the applied migrations build.
        </p>
      )}
      {r.differences.length > 0 && <Differences diffs={r.differences} />}
    </div>
  );
}

// Unchecked lists the schemas the check could not compare, one line per
// reason, so a database with many unmanaged schemas does not bury the ones
// that drifted.
function Unchecked({ schemas }: { schemas: AppDBDriftResult[] }) {
  const byReason = new Map<string, string[]>();
  for (const r of schemas) {
    byReason.set(r.reason ?? "", [
      ...(byReason.get(r.reason ?? "") ?? []),
      r.schema,
    ]);
  }
  return (
    <div
      className="space-y-1 text-xs text-muted-foreground"
      data-testid="appdb-drift-unchecked"
    >
      {[...byReason].map(([reason, names]) => (
        <details key={reason}>
          <summary className="cursor-pointer">
            {names.length} schema(s) not checked: {reason}
          </summary>
          <p className="ml-4 font-mono">{names.join(", ")}</p>
        </details>
      ))}
    </div>
  );
}

function AdoptOutcome({ res }: { res: AppDBAdoptResult }) {
  if (res.adopted) {
    return (
      <p className="text-xs" data-testid="appdb-adopted">
        Schema <span className="font-mono">{res.schema}</span> is managed from
        version {res.version}.
      </p>
    );
  }
  return (
    <div className="space-y-1 text-xs" data-testid="appdb-adopt-refused">
      {res.unsupported.length > 0 && (
        <p>
          Not adopted: the baseline cannot write{" "}
          {res.unsupported.map((u) => `${u.kind} ${u.name}`).join(", ")}.
        </p>
      )}
      {res.differences.length > 0 && (
        <>
          <p>Not adopted: built from zero, the baseline still differs.</p>
          <Differences diffs={res.differences} />
        </>
      )}
    </div>
  );
}

function AdoptForm({ connectionName }: { connectionName: string }) {
  const adopt = useAdoptAppDBSchema(connectionName);
  const [schema, setSchema] = useState("public");
  return (
    <>
      <div className="flex flex-wrap items-center gap-2 border-t pt-3">
        <Input
          aria-label="Schema"
          className="h-8 w-40 font-mono"
          value={schema}
          onChange={(e) => setSchema(e.target.value)}
        />
        <Button
          size="sm"
          variant="outline"
          disabled={adopt.isPending || !schema}
          onClick={() => adopt.mutate(schema)}
        >
          Adopt schema
        </Button>
      </div>
      {adopt.error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{adopt.error.message}</AlertDescription>
        </Alert>
      )}
      {adopt.data && <AdoptOutcome res={adopt.data} />}
    </>
  );
}

export function AppDBDriftCard({ connectionName }: { connectionName: string }) {
  const { data, error, isLoading } = useAppDBDrift(connectionName);
  const total =
    data?.schemas.reduce((n, r) => n + r.differences.length, 0) ?? 0;
  return (
    <SectionCard
      title="Drift"
      collapsible
      defaultOpen={false}
      summary={data ? `${total} object(s) differ` : undefined}
    >
      {isLoading && <p className="text-xs text-muted-foreground">Loading...</p>}
      {error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
      {data && (
        <div className="space-y-4">
          {data.schemas.length === 0 && (
            <p className="text-xs text-muted-foreground">
              The platform keeps no migrations for this connection.
            </p>
          )}
          {data.schemas
            .filter((r) => r.checked)
            .map((r) => (
              <SchemaResult key={r.schema} r={r} />
            ))}
          {data.schemas.some((r) => !r.checked) && (
            <Unchecked schemas={data.schemas.filter((r) => !r.checked)} />
          )}
          <AdoptForm connectionName={connectionName} />
        </div>
      )}
    </SectionCard>
  );
}
