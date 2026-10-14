import { History, Undo2 } from "lucide-react";
import { useCatalogHistory, useRollback, type Change } from "@/api/portal/catalog";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { MutationError } from "./primitives";

// ASPECT_LABELS names each aspect the way the page's sections do.
const ASPECT_LABELS: Record<string, string> = {
  description: "Description",
  tags: "Tags",
  glossary_terms: "Glossary terms",
  owners: "Owners",
  domain: "Domain",
  custom_properties: "Properties",
  structured_properties: "Structured properties",
  documentation_links: "Links",
  curated_queries: "Queries",
  incidents: "Incidents",
  context_documents: "Context documents",
};

/** summarize renders an aspect value as one short line. */
export function summarize(value: unknown): string {
  if (value === undefined || value === null) return "cleared";
  if (typeof value === "string") return value.length > 120 ? `${value.slice(0, 117)}…` : value;
  if (Array.isArray(value)) {
    const parts = value.map((v) =>
      typeof v === "string" ? v : typeof v === "object" && v !== null ? itemName(v) : String(v),
    );
    return parts.length === 0 ? "none" : parts.join(", ");
  }
  if (typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>);
    return entries.length === 0 ? "none" : entries.map(([k, v]) => `${k}=${String(v)}`).join(", ");
  }
  return String(value);
}

// itemName picks the field a list item of an aspect is known by.
function itemName(v: object): string {
  const o = v as Record<string, unknown>;
  for (const k of ["title", "name", "owner", "url", "id"]) {
    if (typeof o[k] === "string") return o[k] as string;
  }
  return JSON.stringify(o);
}

/**
 * HistoryPanel lists every change written to one catalog entity's curation,
 * newest first: who wrote which aspect when, what it now holds and what it
 * replaced. An editor can roll a change back, which restores the value it
 * replaced and is recorded as a change of its own.
 */
export function HistoryPanel({ reference, canEdit }: { reference: string; canEdit: boolean }) {
  const history = useCatalogHistory(reference);
  const rollback = useRollback();
  const changes = history.data ?? [];

  return (
    <SectionCard
      title={
        <span className="flex items-center gap-1.5">
          <History className="size-4" /> History
        </span>
      }
    >
      {history.isError ? (
        <p className="text-sm text-destructive">Failed to load the history.</p>
      ) : history.isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : changes.length === 0 ? (
        <p className="text-sm text-muted-foreground">No changes have been written.</p>
      ) : (
        <ol className="space-y-2">
          {changes.map((c) => (
            <ChangeRow
              key={c.id}
              change={c}
              canEdit={canEdit}
              pending={rollback.isPending}
              onRollback={() =>
                rollback.mutate({ reference: c.reference, aspect: c.aspect, change_id: c.id })
              }
            />
          ))}
        </ol>
      )}
      <MutationError mut={rollback} />
    </SectionCard>
  );
}

function ChangeRow({
  change,
  canEdit,
  pending,
  onRollback,
}: {
  change: Change;
  canEdit: boolean;
  pending: boolean;
  onRollback: () => void;
}) {
  return (
    <li className="flex items-start gap-3 rounded-lg border p-2.5 text-sm">
      <div className="min-w-0 flex-1 space-y-0.5">
        <div className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">
            {ASPECT_LABELS[change.aspect] ?? change.aspect}
          </span>
          <span>{change.written_by || "unknown"}</span>
          <time dateTime={change.written_at}>{new Date(change.written_at).toLocaleString()}</time>
          {change.reverts && <span>rollback</span>}
        </div>
        <p className="break-words">{summarize(change.value)}</p>
        {change.previous !== undefined && change.previous !== null && (
          <p className="break-words text-xs text-muted-foreground">
            was: {summarize(change.previous)}
          </p>
        )}
      </div>
      {canEdit && (
        <Button
          variant="ghost"
          size="xs"
          className="text-muted-foreground"
          disabled={pending}
          onClick={onRollback}
          aria-label={`Roll back the ${ASPECT_LABELS[change.aspect] ?? change.aspect} change of ${change.written_at}`}
        >
          <Undo2 /> Roll back
        </Button>
      )}
    </li>
  );
}
