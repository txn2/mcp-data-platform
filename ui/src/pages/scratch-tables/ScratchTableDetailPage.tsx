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
import { columnTypesText, formatLabel, type ScratchTable } from "@/api/tables/types";
import { PageHeader } from "@/components/patterns/PageHeader";
import { SectionCard } from "@/components/patterns/SectionCard";
import { CopyButton } from "@/components/provenance/parts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { useAuthStore } from "@/stores/auth";
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
        <p className="text-xs text-muted-foreground">{queryNote(row)}</p>
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

      <ColumnsSection row={row} />

      {row.source.kind === "webhook" ? (
        <WebhookSourceSection row={row} onNavigate={onNavigate} />
      ) : (
      <SectionCard title="The file behind this table">
        {/* The description first, because it is the only thing here that says
            what the data IS. The file's name and the directory it is stored
            in are how it is addressed, not what it holds. */}
        {row.source.description ? (
          <p className="mb-3 text-sm">{row.source.description}</p>
        ) : null}
        <dl className="grid gap-3 text-xs sm:grid-cols-2">
          <div>
            <dt className="text-muted-foreground">File</dt>
            <dd className="mt-0.5">
              <SourceValue row={row} onNavigate={onNavigate} />
            </dd>
          </div>
          <div>
            {/* What the file is read as decides what comes back: a JSON-lines
                table returns every value exactly, a CSV table cannot carry a
                line break inside a value (#1820). */}
            <dt className="text-muted-foreground">Read as</dt>
            <dd className="mt-0.5">{formatLabel(row.format)}</dd>
          </div>
          <div>
            {/* An operator's fact, and labelled as one: a reader needs it only
                when they are going to look in the bucket themselves. */}
            <dt className="text-muted-foreground">Stored at</dt>
            <dd className="mt-0.5 font-mono break-all">{row.location}</dd>
          </div>
        </dl>
      </SectionCard>
      )}
    </div>
  );
}

// queryNote says what comes back from the table, which is the format's rule:
// a CSV table's columns are all text, a JSON-lines or Parquet table's carry
// their types, and a webhook source's table is read window by window (#1870).
function queryNote(row: ScratchTable): string {
  if (row.source.kind === "webhook") {
    return "Each compaction window is read from the events as they arrived until the window is compacted, then from its Parquet file with duplicates removed. Read a field of an event with json_extract_scalar(payload, '$.field').";
  }
  if (row.format === "jsonl" || row.format === "parquet") {
    return `${columnTypesText(row.format)} A join to a warehouse column of the same type needs no cast.`;
  }
  return "Every column comes back as text, which is the storage format\u2019s rule rather than a choice, so a join to a typed warehouse column needs a cast.";
}

// WebhookSourceSection names the webhook source a table was created for. The
// table is created and removed with its source, so there is no file to point
// at: what a reader needs is which source, and where it keeps its events.
function WebhookSourceSection({
  row,
  onNavigate,
}: {
  row: ScratchTable;
  onNavigate: (path: string) => void;
}) {
  return (
    <SectionCard title="The webhook source behind this table">
      {row.source.description ? <p className="mb-3 text-sm">{row.source.description}</p> : null}
      <dl className="grid gap-3 text-xs sm:grid-cols-2">
        <div>
          <dt className="text-muted-foreground">Source</dt>
          <dd className="mt-0.5">
            <SourceValue row={row} onNavigate={onNavigate} />
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Stored at</dt>
          <dd className="mt-0.5 font-mono break-all">{row.location}</dd>
        </div>
      </dl>
    </SectionCard>
  );
}

// ColumnsSection lists the column names, and says the type once when every
// column shares it.
//
// Every column of a CSV table is VARCHAR -- the CSV connector's rule -- so a
// badge per column repeating it printed the same word thirty-five times and
// buried the names the section exists to help a reader find (#1796). A table
// whose columns differ, which a JSON-lines or Parquet table's usually do
// (#1833), gets the type on each one, because then it discriminates.
function ColumnsSection({ row }: { row: ScratchTable }) {
  const types = new Set(row.columns.map((c) => c.type));
  const uniform = types.size === 1 ? [...types][0] : null;
  return (
    <SectionCard title={`Columns (${row.columns.length})`}>
      {row.columns.length === 0 ? (
        <p className="text-xs text-muted-foreground">No columns were recorded.</p>
      ) : (
        <>
          {uniform ? (
            <p className="mb-2 text-xs text-muted-foreground">
              Every column is <span className="font-mono">{uniform}</span>.
            </p>
          ) : null}
          {row.all_varchar ? (
            <p className="mb-2 text-xs text-muted-foreground" data-testid="all-varchar-note">
              This JSON-lines table was registered before a JSON-lines table&apos;s columns were typed, and keeps
              every column VARCHAR as its file changes so the queries written against it keep working. Register the
              file again under the same name to declare each column&apos;s type.
            </p>
          ) : null}
          <div className="flex flex-wrap gap-1.5">
            {row.columns.map((c) => (
              <Badge key={c.name} variant="muted" className="rounded px-1.5 font-mono">
                {c.name}
                {uniform ? null : <span className="ml-1 opacity-70">{c.type}</span>}
              </Badge>
            ))}
          </div>
        </>
      )}
    </SectionCard>
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
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const path = sourcePath(row.source.kind, row.source.id, row.source.missing, isAdmin);
  if (row.source.missing) {
    return (
      <span className="text-muted-foreground">
        {kind} {row.source.id} &mdash; no longer on the platform
      </span>
    );
  }
  if (!path) {
    // A source whose page is for administrators is named, not linked.
    return (
      <span>
        {row.source.name || row.source.id}{" "}
        <span className="text-muted-foreground">({kind})</span>
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
  if (row.stale && row.follow && row.follow_error) {
    return (
      <Alert variant="warning" className="py-2">
        <AlertTriangle />
        <AlertDescription>
          This table follows its file but could not be moved onto the current version:{" "}
          {row.follow_error} Queries return the version it last read. Open the file and register
          the table again to move it.
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
  if (row.source.kind === "webhook") {
    // Not over one file, so there is no version to follow or be pinned to.
    return null;
  }
  if (row.follow) {
    return (
      <p className="text-xs text-muted-foreground">
        Follows the file: each new version written over the file moves this table onto it.
        {/*
          A table registered with the correction writes versions of somebody's
          file that nobody typed (#1577), so the page that lists what is
          registered on a shared schema says which tables do that.
        */}
        {row.repair &&
          " It also corrects the file: a new version a query engine cannot read is saved corrected," +
            " as the file's next version, and this table moves onto the corrected version."}
      </p>
    );
  }
  return (
    <p className="text-xs text-muted-foreground">
      Pinned to the version of the file it was registered over: a newer version leaves this table
      where it is until somebody registers it again.
    </p>
  );
}

// UnregisterAction drops the table. It goes through the source's own route,
// which is where the rule for who may drop a registration lives, and is absent
// for a reader that rule would refuse.
function UnregisterAction({ row, onDone }: { row: ScratchTable; onDone: () => void }) {
  // A webhook source's table is removed with the source, and the listing
  // never offers it; the file kinds are the only ones with this route.
  const unregister = useUnregisterTable(row.source.kind === "asset" ? "asset" : "resource", row.source.id);
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
