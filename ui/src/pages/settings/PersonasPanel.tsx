import { useState, useEffect, useCallback, useMemo } from "react";
import {
  usePersonas,
  usePersonaDetail,
  useDeletePersona,
  useSystemInfo,
} from "@/api/admin/hooks";
import type { PersonaDetail } from "@/api/admin/types";
import { Users } from "lucide-react";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Badge } from "@/components/ui/badge";
import { PersonaEditor, type PersonaDraft } from "./PersonaEditor";
import {
  DetailList,
  DetailListAddButton,
  DetailListEmpty,
  DetailListItem,
  MasterDetail,
} from "./MasterDetail";

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

function sourceNoteFor(
  detail: PersonaDetail,
  isReadOnly: boolean,
): string | null {
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
  const personas = useMemo(() => personaList?.personas ?? [], [personaList]);
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
    <MasterDetail
      list={
        <DetailList
          footer={
            !isReadOnly && (
              <DetailListAddButton
                active={isCreating}
                label="New Persona"
                onClick={handleCreate}
              />
            )
          }
        >
          {personas.map((p) => (
            <DetailListItem
              key={p.name}
              selected={selected === p.name && !isCreating}
              onClick={() => handleSelect(p.name)}
            >
              <span className="flex items-center gap-1.5">
                <span className="truncate text-sm font-medium">
                  {p.display_name}
                </span>
                {/* Where the persona is defined decides whether an edit
                    persists, so the source rides the same info/muted pair the
                    rest of the admin surfaces use for "database" vs "file". */}
                {p.source && (
                  <Badge
                    variant={p.source === "file" ? "muted" : "info"}
                    className="rounded px-1"
                  >
                    {p.source === "file" ? "file" : "database"}
                  </Badge>
                )}
              </span>
              <span className="truncate font-mono text-xs text-muted-foreground">
                {p.name}
              </span>
              <span className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
                <span>{p.roles.length} roles</span>
                <span>{p.tool_count} tools</span>
              </span>
            </DetailListItem>
          ))}
          {personas.length === 0 && (
            <DetailListEmpty>No personas configured</DetailListEmpty>
          )}
        </DetailList>
      }
    >
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
        <div className="flex h-full items-center justify-center p-6">
          <EmptyState icon={Users} className="w-full max-w-sm">
            Select a persona or create a new one
          </EmptyState>
        </div>
      ) : (
        <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
          Loading...
        </div>
      )}

      <ConfirmDialog
        open={pendingNav !== null}
        onOpenChange={(open) => {
          if (!open) setPendingNav(null);
        }}
        destructive
        title="Discard unsaved changes?"
        description="You have unsaved changes to this persona. If you continue, your edits will be lost."
        confirmLabel="Discard"
        onConfirm={() => {
          const apply = pendingNav;
          setPendingNav(null);
          apply?.();
        }}
      />

      {detail && (
        <ConfirmDialog
          open={confirmDelete}
          onOpenChange={setConfirmDelete}
          destructive
          title="Delete Persona"
          description={
            detail.source === "both"
              ? `Are you sure you want to remove the database override for "${detail.display_name}"? It will revert to the version defined in the config file.`
              : `Are you sure you want to delete "${detail.display_name}"? This cannot be undone.`
          }
          confirmLabel="Delete"
          loading={deleteMutation.isPending}
          onConfirm={handleDelete}
        />
      )}
    </MasterDetail>
  );
}
