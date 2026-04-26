import { useEffect, useState } from "react";
import { Check, Pencil, RotateCcw, Save, X } from "lucide-react";
import {
  useResetToolDescription,
  useUpdateToolDescription,
} from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { ToolDetail } from "@/api/admin/types";

export function OverviewTab({ detail }: { detail: ToolDetail }) {
  return (
    <div className="space-y-6">
      <DescriptionSection detail={detail} />
      <RoutingSection detail={detail} />
      <PersonaMatrixSection detail={detail} />
      <SchemaSection detail={detail} />
    </div>
  );
}

function DescriptionSection({ detail }: { detail: ToolDetail }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(detail.description ?? "");
  const update = useUpdateToolDescription(detail.name);
  const reset = useResetToolDescription(detail.name);

  // Re-sync the draft whenever the underlying tool changes (selection switch).
  useEffect(() => {
    setDraft(detail.description ?? "");
    setEditing(false);
  }, [detail.name, detail.description]);

  const submitting = update.isPending || reset.isPending;

  return (
    <section>
      <div className="mb-2 flex items-center justify-between gap-2">
        <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Description
        </h3>
        <div className="flex items-center gap-2">
          {detail.description_overridden && (
            <StatusBadge variant="warning">
              overridden
              {detail.override_author ? ` · ${detail.override_author}` : ""}
            </StatusBadge>
          )}
          {!editing ? (
            <button
              onClick={() => setEditing(true)}
              className="inline-flex items-center gap-1 rounded border px-2 py-1 text-xs hover:bg-muted"
            >
              <Pencil className="h-3 w-3" /> Edit
            </button>
          ) : (
            <>
              <button
                onClick={() => {
                  update.mutate(draft, {
                    onSuccess: () => setEditing(false),
                  });
                }}
                disabled={submitting}
                className="inline-flex items-center gap-1 rounded bg-primary px-2 py-1 text-xs font-medium text-primary-foreground disabled:opacity-50"
              >
                <Save className="h-3 w-3" />
                {update.isPending ? "Saving…" : "Save"}
              </button>
              <button
                onClick={() => {
                  setDraft(detail.description ?? "");
                  setEditing(false);
                }}
                disabled={submitting}
                className="inline-flex items-center gap-1 rounded border px-2 py-1 text-xs disabled:opacity-50"
              >
                <X className="h-3 w-3" /> Cancel
              </button>
            </>
          )}
          {detail.description_overridden && !editing && (
            <button
              onClick={() => {
                if (
                  window.confirm(
                    "Revert description to the platform default? The override will be removed.",
                  )
                ) {
                  reset.mutate();
                }
              }}
              disabled={submitting}
              className="inline-flex items-center gap-1 rounded border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50"
            >
              <RotateCcw className="h-3 w-3" /> Reset
            </button>
          )}
        </div>
      </div>

      {editing ? (
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          rows={6}
          className="w-full rounded border bg-background p-2 text-sm outline-none ring-ring focus:ring-2"
        />
      ) : (
        <p className="whitespace-pre-line text-sm leading-relaxed text-muted-foreground">
          {detail.description || (
            <span className="italic">No description.</span>
          )}
        </p>
      )}

      {update.isError && (
        <p className="mt-1 text-xs text-destructive">
          Failed to save: {(update.error as Error).message}
        </p>
      )}
      {update.isSuccess && !editing && (
        <p className="mt-1 inline-flex items-center gap-1 text-xs text-green-700">
          <Check className="h-3 w-3" /> Description saved.
        </p>
      )}
    </section>
  );
}

function RoutingSection({ detail }: { detail: ToolDetail }) {
  return (
    <section>
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        Routing
      </h3>
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
        <dt className="text-muted-foreground">Toolkit</dt>
        <dd>{detail.toolkit_name || "—"}</dd>
        <dt className="text-muted-foreground">Kind</dt>
        <dd>{detail.toolkit_kind}</dd>
        <dt className="text-muted-foreground">Connection</dt>
        <dd>{detail.connection || <span className="text-muted-foreground">platform</span>}</dd>
        {detail.title && (
          <>
            <dt className="text-muted-foreground">Title</dt>
            <dd>{detail.title}</dd>
          </>
        )}
      </dl>
    </section>
  );
}

function PersonaMatrixSection({ detail }: { detail: ToolDetail }) {
  const personas = detail.personas ?? [];
  if (personas.length === 0) {
    return (
      <section>
        <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Personas
        </h3>
        <p className="text-sm text-muted-foreground">
          No database-managed personas configured.
        </p>
      </section>
    );
  }
  return (
    <section>
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        Personas ({personas.filter((p) => p.allowed).length}/{personas.length} allow)
      </h3>
      <div className="overflow-hidden rounded border">
        <table className="w-full text-sm">
          <thead className="bg-muted/40 text-xs">
            <tr>
              <th className="px-3 py-1.5 text-left font-medium">Persona</th>
              <th className="px-3 py-1.5 text-left font-medium">Decision</th>
              <th className="px-3 py-1.5 text-left font-medium">Pattern</th>
              <th className="px-3 py-1.5 text-left font-medium">Source</th>
            </tr>
          </thead>
          <tbody>
            {personas.map((p) => (
              <tr key={p.persona} className="border-t">
                <td className="px-3 py-1.5 font-medium">{p.persona}</td>
                <td className="px-3 py-1.5">
                  <StatusBadge variant={p.allowed ? "success" : "neutral"}>
                    {p.allowed ? "allow" : "deny"}
                  </StatusBadge>
                </td>
                <td className="px-3 py-1.5 font-mono text-xs">
                  {p.matched_pattern || <span className="text-muted-foreground">—</span>}
                </td>
                <td className="px-3 py-1.5 text-xs text-muted-foreground">
                  {p.source}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function SchemaSection({ detail }: { detail: ToolDetail }) {
  if (!detail.input_schema) return null;
  return (
    <section>
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        Input schema
      </h3>
      <pre className="max-h-[400px] overflow-auto rounded border bg-muted/40 p-3 font-mono text-xs">
        {JSON.stringify(detail.input_schema, null, 2)}
      </pre>
    </section>
  );
}
