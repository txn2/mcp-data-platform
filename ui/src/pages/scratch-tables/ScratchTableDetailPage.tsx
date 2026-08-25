import { useState } from "react";
import {
  AlertTriangle,
  ExternalLink,
  FileX2,
  Loader2,
  Table2,
  Trash2,
} from "lucide-react";
import { useScratchTable, useUnregisterTable, TableApiError } from "@/api/tables/hooks";
import type { ScratchTable } from "@/api/tables/types";
import { PageHeader } from "@/components/patterns/PageHeader";
import { SectionCard } from "@/components/patterns/SectionCard";
import { CopyButton } from "@/components/provenance/parts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { sourceKindLabel, sourcePath } from "./source";

// ScratchTableDetailPage is one registration at an address of its own
// (#1472): what to query, what comes back, which file it reads, and what to do
// when the table has fallen behind that file.
//
// Unregistering goes through the source's own route, which is where the rule
// for who may drop a table is written. The listing reports whether this reader
// is one of them, so the control is absent rather than present and refusing.

export function ScratchTableDetailPage({
  registrationId,
  onBack,
  onNavigate,
}: {
  registrationId: string;
  onBack: () => void;
  onNavigate: (path: string) => void;
}) {
  const { data, isLoading, error } = useScratchTable(registrationId);

  if (isLoading) {
    return (
      <div className="flex justify-center py-16">
        <LoadingIndicator />
      </div>
    );
  }
  if (error || !data) {
    return (
      <div className="space-y-4">
        <PageHeader backLabel="Scratch Tables" onBack={onBack} title="Registered table" />
        <Alert variant="destructive" className="py-2">
          <AlertDescription>{notFoundText(error)}</AlertDescription>
        </Alert>
      </div>
    );
  }

  return <Registration row={data} onBack={onBack} onNavigate={onNavigate} />;
}

function Registration({
  row,
  onBack,
  onNavigate,
}: {
  row: ScratchTable;
  onBack: () => void;
  onNavigate: (path: string) => void;
}) {
  return (
    <div className="space-y-4">
      <PageHeader
        backLabel="Scratch Tables"
        onBack={onBack}
        icon={Table2}
        title={row.table}
        urn={row.query_table}
        subtitle={
          <>
            on <span className="font-medium text-foreground">{row.connection}</span> &middot;
            registered by {row.registered_by} on{" "}
            {new Date(row.registered_at).toLocaleDateString()}
          </>
        }
        actions={
          <>
            <CopyButton text={row.query_table} label="Copy the table name" />
            <UnregisterAction row={row} onDone={onBack} />
          </>
        }
      />

      <StateNotice row={row} />

      <SectionCard title="Query it">
        <p className="text-xs text-muted-foreground">
          Every column comes back as text, which is the CSV connector&rsquo;s rule rather than a
          choice, so a join to a typed warehouse column needs a cast.
        </p>
        {/* A registration that recorded columns always carries a sample, since
            the platform derives one from them. The fallback is the plain
            select, which is true of any registration and says nothing the
            columns below it contradict. */}
        {row.sample_sql ? (
          <div className="mt-2 flex items-start gap-2">
            <pre className="min-w-0 flex-1 overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs">
              {row.sample_sql}
            </pre>
            <CopyButton text={row.sample_sql} label="Copy the sample query" />
          </div>
        ) : (
          <p className="mt-2 font-mono text-xs">SELECT * FROM {row.query_table}</p>
        )}
      </SectionCard>

      <SectionCard title={`Columns (${row.columns.length})`}>
        {row.columns.length === 0 ? (
          <p className="text-xs text-muted-foreground">No columns were recorded.</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {row.columns.map((c) => (
              <Badge key={c.name} variant="muted" className="rounded px-1.5 font-mono">
                {c.name}
                <span className="ml-1 opacity-70">{c.type}</span>
              </Badge>
            ))}
          </div>
        )}
      </SectionCard>

      <SectionCard title="What it reads">
        <dl className="grid gap-3 text-xs sm:grid-cols-2">
          <div>
            <dt className="text-muted-foreground">Source</dt>
            <dd className="mt-0.5">
              <SourceValue row={row} onNavigate={onNavigate} />
            </dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Directory</dt>
            <dd className="mt-0.5 font-mono break-all">{row.location}</dd>
          </div>
        </dl>
      </SectionCard>
    </div>
  );
}

// SourceValue links to the file the table reads, or says it is gone. A record
// that no longer exists gets no link: sending a reader to a page that answers
// "no such file" is worse than telling them here.
function SourceValue({
  row,
  onNavigate,
}: {
  row: ScratchTable;
  onNavigate: (path: string) => void;
}) {
  const kind = sourceKindLabel(row.source.kind);
  const path = sourcePath(row.source.kind, row.source.id, row.source.missing);
  if (!path) {
    return (
      <span className="text-muted-foreground">
        {kind} {row.source.id} &mdash; no longer on the platform
      </span>
    );
  }
  return (
    <button
      type="button"
      onClick={() => onNavigate(path)}
      className="inline-flex items-center gap-1 font-medium text-primary hover:underline"
    >
      {row.source.name || row.source.id}
      <ExternalLink aria-hidden className="size-3" />
      <span className="font-normal text-muted-foreground">({kind})</span>
    </button>
  );
}

// StateNotice is the currency verdict and what to do about it. Both cases are
// things only a cross-source read can tell a reader, and neither is worth
// showing as a bare flag: a table that returns rows nobody expects is a
// question about what to do next.
function StateNotice({ row }: { row: ScratchTable }) {
  if (row.source.missing) {
    return (
      <Alert variant="destructive" className="py-2">
        <FileX2 />
        <AlertDescription>
          The file this table was built over is no longer on the platform, so the table reads a
          directory whose contents are gone. Drop it, or register the table again over the file
          that replaced it.
        </AlertDescription>
      </Alert>
    );
  }
  if (row.stale) {
    return (
      <Alert variant="warning" className="py-2">
        <AlertTriangle />
        <AlertDescription>
          The file has a newer version than the table points at, so queries return the version that
          was current when it was registered. Open the file and register it again to move the table
          onto the current version.
        </AlertDescription>
      </Alert>
    );
  }
  return null;
}

// UnregisterAction drops the table. It goes through the source's own route,
// which is where the rule for who may drop a registration lives, and is absent
// for a reader that rule would refuse.
function UnregisterAction({ row, onDone }: { row: ScratchTable; onDone: () => void }) {
  const unregister = useUnregisterTable(row.source.kind, row.source.id);
  const [confirming, setConfirming] = useState(false);

  if (!row.can_unregister) {
    return null;
  }
  if (!confirming) {
    return (
      <Button type="button" variant="outline" size="xs" onClick={() => setConfirming(true)}>
        <Trash2 />
        Unregister
      </Button>
    );
  }
  return (
    <span className="flex items-center gap-2">
      <span className="text-xs text-muted-foreground">Drop this table?</span>
      <Button type="button" variant="ghost" size="xs" onClick={() => setConfirming(false)}>
        Cancel
      </Button>
      <Button
        type="button"
        variant="destructive"
        size="xs"
        disabled={unregister.isPending}
        onClick={() => unregister.mutate(row.id, { onSuccess: onDone })}
      >
        {unregister.isPending ? <Loader2 className="animate-spin" /> : <Trash2 />}
        Unregister
      </Button>
    </span>
  );
}

// notFoundText renders what went wrong.
//
// A registration on a connection the reader is not granted answers as a 404,
// the same as one that never existed, so the two are told apart nowhere --
// including here. A read that FAILED is not either of those and must not be
// reported as an absence: the table may well be there and queryable.
function notFoundText(err: unknown): string {
  if (err instanceof TableApiError) {
    return err.status === 404
      ? "No registered table with this id, or none you can reach."
      : err.detail;
  }
  if (err) {
    return "This registered table could not be read. That is a failure to reach it rather than an absence.";
  }
  return "No registered table with this id, or none you can reach.";
}
