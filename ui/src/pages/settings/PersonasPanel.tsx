import { useState, useEffect, useCallback } from "react";
import {
  usePersonas,
  usePersonaDetail,
  useDeletePersona,
  useSystemInfo,
} from "@/api/admin/hooks";
import type { PersonaDetail } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import { Plus, Users } from "lucide-react";
import { PersonaEditor, type PersonaDraft } from "./PersonaEditor";

function emptyDraft(): PersonaDraft {
  return {
    name: "",
    displayName: "",
    description: "",
    roles: [],
    allowTools: [],
    denyTools: [],
    allowConnections: [],
    denyConnections: [],
    priority: 0,
    descriptionPrefix: "",
    descriptionOverride: "",
    agentInstructionsSuffix: "",
    agentInstructionsOverride: "",
  };
}

function detailToDraft(d: PersonaDetail): PersonaDraft {
  return {
    name: d.name,
    displayName: d.display_name,
    description: d.description ?? "",
    roles: [...d.roles],
    allowTools: [...d.allow_tools],
    denyTools: [...d.deny_tools],
    allowConnections: [...(d.allow_connections ?? [])],
    denyConnections: [...(d.deny_connections ?? [])],
    priority: d.priority,
    descriptionPrefix: d.context?.description_prefix ?? "",
    descriptionOverride: d.context?.description_override ?? "",
    agentInstructionsSuffix: d.context?.agent_instructions_suffix ?? "",
    agentInstructionsOverride: d.context?.agent_instructions_override ?? "",
  };
}

function sourceNoteFor(detail: PersonaDetail, isReadOnly: boolean): string | null {
  if (isReadOnly) {
    return "Personas are loaded from the config file in this deployment. Changes made here will not persist; manage personas by updating the config file.";
  }
  if (detail.source === "both") {
    return "This persona is managed in the database. A fallback version also exists in the config file and can be removed once database management is confirmed.";
  }
  if (detail.source === "file") {
    return "This persona is defined in the config file. Editing will create a database override.";
  }
  return null;
}

// ---------------------------------------------------------------------------
// PersonasPanel: list (left) + always-on editor (right)
// ---------------------------------------------------------------------------

export function PersonasPanel() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
  const { data: personaList, isLoading } = usePersonas();
  const personas = personaList?.personas ?? [];
  const deleteMutation = useDeletePersona();

  const [selected, setSelected] = useState<string | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [draft, setDraft] = useState<PersonaDraft>(emptyDraft());
  const [dirty, setDirty] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [pendingNav, setPendingNav] = useState<(() => void) | null>(null);

  const { data: detail } = usePersonaDetail(isCreating ? null : selected);

  // Auto-select the first persona on first load.
  useEffect(() => {
    if (!selected && !isCreating && personas.length > 0 && personas[0]) {
      setSelected(personas[0].name);
    }
  }, [personas, selected, isCreating]);

  // Sync draft from detail whenever a new persona is loaded and the user hasn't
  // started editing.
  useEffect(() => {
    if (detail && !dirty && !isCreating) {
      setDraft(detailToDraft(detail));
    }
  }, [detail, dirty, isCreating]);

  const handleSelect = useCallback(
    (name: string) => {
      const apply = () => {
        setSelected(name);
        setIsCreating(false);
        setDirty(false);
      };
      if (dirty) {
        setPendingNav(() => apply);
        return;
      }
      apply();
    },
    [dirty],
  );

  const handleCreate = useCallback(() => {
    const apply = () => {
      setSelected(null);
      setDraft(emptyDraft());
      setIsCreating(true);
      setDirty(false);
    };
    if (dirty) {
      setPendingNav(() => apply);
      return;
    }
    apply();
  }, [dirty]);

  const handleCancel = useCallback(() => {
    if (isCreating) {
      setIsCreating(false);
      if (personas.length > 0 && personas[0]) {
        setSelected(personas[0].name);
      }
      setDirty(false);
      return;
    }
    if (detail) {
      setDraft(detailToDraft(detail));
    }
    setDirty(false);
  }, [isCreating, detail, personas]);

  const handleSaved = useCallback(() => {
    if (isCreating) {
      const savedName = draft.name;
      setIsCreating(false);
      setSelected(savedName);
    }
    setDirty(false);
  }, [isCreating, draft.name]);

  const handleDelete = useCallback(() => {
    if (!detail) return;
    deleteMutation.mutate(detail.name, {
      onSuccess: () => {
        setConfirmDelete(false);
        setSelected(null);
        setDirty(false);
      },
    });
  }, [detail, deleteMutation]);

  const updateDraft = useCallback(
    (partial: Partial<PersonaDraft>) => {
      if (isReadOnly) return;
      setDraft((prev) => ({ ...prev, ...partial }));
      setDirty(true);
    },
    [isReadOnly],
  );

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Loading personas...
      </div>
    );
  }

  const canDelete = Boolean(
    detail &&
      !isReadOnly &&
      !isCreating &&
      detail.name !== "admin" &&
      detail.source !== "file",
  );

  return (
    <div className="flex h-full overflow-hidden">
      {/* Left: Persona list */}
      <div className="w-56 shrink-0 border-r bg-muted/10 flex flex-col overflow-hidden">
        <div className="flex-1 overflow-auto">
          {personas.map((p) => (
            <button
              key={p.name}
              type="button"
              onClick={() => handleSelect(p.name)}
              className={cn(
                "flex w-full flex-col px-4 py-3 text-left border-b transition-colors",
                selected === p.name && !isCreating
                  ? "bg-primary/5 border-l-2 border-l-primary"
                  : "border-l-2 border-l-transparent hover:bg-muted/50",
              )}
            >
              <div className="flex items-center gap-1.5">
                <span className="text-sm font-medium truncate">{p.display_name}</span>
                {p.source && (
                  <span className={cn(
                    "shrink-0 rounded px-1 py-0 text-xs font-medium",
                    p.source === "file" ? "bg-muted text-muted-foreground" :
                    "bg-primary/10 text-primary",
                  )}>
                    {p.source === "file" ? "file" : "database"}
                  </span>
                )}
              </div>
              <span className="text-xs font-mono text-muted-foreground truncate">{p.name}</span>
              <div className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
                <span>{p.roles.length} roles</span>
                <span>{p.tool_count} tools</span>
              </div>
            </button>
          ))}
          {personas.length === 0 && (
            <div className="px-4 py-8 text-center text-xs text-muted-foreground">
              No personas configured
            </div>
          )}
        </div>
        {!isReadOnly && (
          <div className="border-t p-2">
            <button
              type="button"
              onClick={handleCreate}
              className={cn(
                "flex w-full items-center justify-center gap-1.5 rounded-md px-3 py-2 text-xs font-medium transition-colors",
                isCreating
                  ? "bg-primary/10 text-primary"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground",
              )}
            >
              <Plus className="h-3.5 w-3.5" />
              New Persona
            </button>
          </div>
        )}
      </div>

      {/* Right: Editor */}
      <div className="flex-1 overflow-auto">
        {isCreating ? (
          <PersonaEditor
            key="__create__"
            draft={draft}
            onUpdate={updateDraft}
            onSave={handleSaved}
            onCancel={handleCancel}
            isCreate
            dirty={dirty}
            selectedName={null}
          />
        ) : selected && detail ? (
          <PersonaEditor
            key={selected}
            draft={draft}
            onUpdate={updateDraft}
            onSave={handleSaved}
            onCancel={handleCancel}
            isCreate={false}
            dirty={dirty}
            selectedName={selected}
            canDelete={canDelete}
            onDelete={() => setConfirmDelete(true)}
            sourceNote={sourceNoteFor(detail, isReadOnly)}
            isReadOnly={isReadOnly}
          />
        ) : !selected ? (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            <div className="text-center">
              <Users className="mx-auto mb-2 h-8 w-8 opacity-30" />
              <p>Select a persona or create a new one</p>
            </div>
          </div>
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            Loading...
          </div>
        )}
      </div>

      {pendingNav && (
        <ConfirmModal
          title="Discard unsaved changes?"
          message="You have unsaved changes to this persona. If you continue, your edits will be lost."
          confirmLabel="Discard"
          onConfirm={() => {
            const apply = pendingNav;
            setPendingNav(null);
            apply();
          }}
          onCancel={() => setPendingNav(null)}
          destructive
        />
      )}

      {confirmDelete && detail && (
        <ConfirmModal
          title="Delete Persona"
          message={detail.source === "both"
            ? `Are you sure you want to remove the database override for "${detail.display_name}"? It will revert to the version defined in the config file.`
            : `Are you sure you want to delete "${detail.display_name}"? This cannot be undone.`}
          confirmLabel="Delete"
          onConfirm={handleDelete}
          onCancel={() => setConfirmDelete(false)}
          destructive
        />
      )}
    </div>
  );
}

function ConfirmModal({
  title,
  message,
  confirmLabel,
  onConfirm,
  onCancel,
  destructive,
}: {
  title: string;
  message: string;
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
  destructive?: boolean;
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={onCancel}
    >
      <div
        className="rounded-lg border bg-card p-6 shadow-lg max-w-sm mx-4"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 className="text-sm font-semibold mb-2">{title}</h3>
        <p className="text-sm text-muted-foreground mb-4">{message}</p>
        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            className={cn(
              "rounded-md px-3 py-1.5 text-xs font-medium",
              destructive
                ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
                : "bg-primary text-primary-foreground hover:bg-primary/90",
            )}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
