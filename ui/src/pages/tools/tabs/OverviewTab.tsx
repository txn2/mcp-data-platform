import { useEffect, useState } from "react";
import { Check, Pencil, RotateCcw, Save, X } from "lucide-react";
import {
  useResetToolDescription,
  useUpdateToolDescription,
} from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { ToolDetail } from "@/api/admin/types";
import { errorMessage } from "@/lib/utils";
import { PersonaDecisionTable } from "../parts/PersonaDecisionTable";

export function OverviewTab({ detail }: { detail: ToolDetail }) {
  return (
    <div className="space-y-4">
      <DescriptionSection detail={detail} />
      <RoutingSection detail={detail} />
      <PersonaMatrixSection detail={detail} />
      <SchemaSection detail={detail} />
    </div>
  );
}

// DescriptionActions carries the edit affordance and, once editing, the save
// and cancel pair — plus the reset that removes an override entirely.
function DescriptionActions({
  detail,
  editing,
  submitting,
  saving,
  onEdit,
  onSave,
  onCancel,
  onReset,
}: {
  detail: ToolDetail;
  editing: boolean;
  submitting: boolean;
  saving: boolean;
  onEdit: () => void;
  onSave: () => void;
  onCancel: () => void;
  onReset: () => void;
}) {
  return (
    <div className="flex items-center gap-2">
      {detail.description_overridden && (
        <StatusBadge variant="warning">
          overridden
          {detail.override_author ? ` · ${detail.override_author}` : ""}
        </StatusBadge>
      )}
      {!editing ? (
        <Button variant="outline" size="xs" onClick={onEdit}>
          <Pencil /> Edit
        </Button>
      ) : (
        <>
          <Button size="xs" onClick={onSave} disabled={submitting}>
            <Save />
            {saving ? "Saving…" : "Save"}
          </Button>
          <Button variant="outline" size="xs" onClick={onCancel} disabled={submitting}>
            <X /> Cancel
          </Button>
        </>
      )}
      {detail.description_overridden && !editing && (
        <Button variant="outline" size="xs" onClick={onReset} disabled={submitting}>
          <RotateCcw /> Reset
        </Button>
      )}
    </div>
  );
}

function DescriptionSection({ detail }: { detail: ToolDetail }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(detail.description ?? "");
  const [confirmReset, setConfirmReset] = useState(false);
  const update = useUpdateToolDescription(detail.name);
  const reset = useResetToolDescription(detail.name);

  // Re-sync the draft whenever the underlying tool changes (selection switch).
  useEffect(() => {
    setDraft(detail.description ?? "");
    setEditing(false);
  }, [detail.name, detail.description]);

  const submitting = update.isPending || reset.isPending;

  return (
    <SectionCard
      title="Description"
      action={
        <DescriptionActions
          detail={detail}
          editing={editing}
          submitting={submitting}
          saving={update.isPending}
          onEdit={() => setEditing(true)}
          onSave={() => update.mutate(draft, { onSuccess: () => setEditing(false) })}
          onCancel={() => {
            setDraft(detail.description ?? "");
            setEditing(false);
          }}
          onReset={() => setConfirmReset(true)}
        />
      }
    >
      {editing ? (
        <Textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          rows={6}
          aria-label="Tool description"
          className="field-sizing-fixed"
        />
      ) : (
        <p className="text-sm leading-relaxed whitespace-pre-line text-muted-foreground">
          {detail.description || <span className="italic">No description.</span>}
        </p>
      )}

      {update.isError && (
        <p className="mt-1 text-xs text-destructive">
          Failed to save: {errorMessage(update.error)}
        </p>
      )}
      {update.isSuccess && !editing && (
        <p className="mt-1 inline-flex items-center gap-1 text-xs text-emerald-700 dark:text-emerald-400">
          <Check className="size-3" /> Description saved.
        </p>
      )}

      <ConfirmDialog
        open={confirmReset}
        onOpenChange={setConfirmReset}
        title="Revert description to the platform default?"
        description="The override will be removed and the toolkit's own description served again."
        confirmLabel="Reset"
        loading={reset.isPending}
        onConfirm={() => {
          reset.mutate();
          setConfirmReset(false);
        }}
      />
    </SectionCard>
  );
}

function RoutingSection({ detail }: { detail: ToolDetail }) {
  return (
    <SectionCard title="Routing">
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
    </SectionCard>
  );
}

function PersonaMatrixSection({ detail }: { detail: ToolDetail }) {
  const personas = detail.personas ?? [];
  if (personas.length === 0) {
    return (
      <SectionCard title="Personas">
        <p className="text-sm text-muted-foreground">
          No database-managed personas configured.
        </p>
      </SectionCard>
    );
  }
  return (
    <SectionCard
      title={`Personas (${personas.filter((p) => p.allowed).length}/${personas.length} allow)`}
    >
      <PersonaDecisionTable personas={personas} />
    </SectionCard>
  );
}

function SchemaSection({ detail }: { detail: ToolDetail }) {
  if (!detail.input_schema) return null;
  return (
    <SectionCard title="Input schema">
      <pre className="max-h-[400px] overflow-auto rounded border bg-muted/40 p-3 font-mono text-xs">
        {JSON.stringify(detail.input_schema, null, 2)}
      </pre>
    </SectionCard>
  );
}
