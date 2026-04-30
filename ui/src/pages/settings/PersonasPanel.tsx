import { useState, useEffect, useCallback, useMemo } from "react";
import {
  usePersonas,
  usePersonaDetail,
  useCreatePersona,
  useUpdatePersona,
  useDeletePersona,
  useSystemInfo,
  useTools,
  useConnections,
} from "@/api/admin/hooks";
import type { PersonaDetail, ToolInfo, ConnectionInfo } from "@/api/admin/types";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { cn } from "@/lib/utils";
import {
  Plus,
  Trash2,
  Save,
  X,
  ChevronDown,
  Users,
  Shield,
  Wrench,
  FileText,
  AlertCircle,
  Check,
  HelpCircle,
  Search,
  Cable,
} from "lucide-react";

// ---------------------------------------------------------------------------
// Help text content
// ---------------------------------------------------------------------------

const HELP = {
  roles: `Roles determine which users get this persona. When a user authenticates via OIDC, their token claims are mapped to roles (via auth.oidc.role_claim_path and role_prefix in config). When using API keys, roles are assigned directly in the key definition. A user is assigned a persona when any of their roles match one of the persona's roles.`,
  priority: `When a user's roles match multiple personas, the one with the highest priority wins. Default is 0. The built-in "admin" persona uses priority 1000.`,
  allowTools: `Glob patterns for tools this persona can access. Use * to match any sequence of characters. Examples: * (all tools), trino_* (all Trino tools), datahub_search (specific tool). If empty, no tools are allowed (deny by default).`,
  denyTools: `Glob patterns for tools explicitly denied to this persona. Deny rules override allow rules. Example: *_delete_* denies all delete operations even if allow includes *.`,
  allowConnections: `Connection identifiers this persona can access (format: kind/name). If empty, all connections are permitted. Works alongside tool access — a tool call must pass both checks.`,
  denyConnections: `Connection identifiers explicitly denied to this persona. Deny rules override allow rules.`,
  descriptionPrefix: `Prepended to the platform description when this persona calls platform_info. Use to add persona-specific context before the base description. Ignored if Description Override is set.`,
  descriptionOverride: `Replaces the platform description entirely for this persona. Use when this persona needs a completely different description.`,
  agentInstructionsSuffix: `Appended to the platform agent instructions when this persona calls platform_info. Use to add persona-specific guidance after the base instructions. Ignored if Agent Instructions Override is set.`,
  agentInstructionsOverride: `Replaces the platform agent instructions entirely for this persona. Use when this persona needs completely different instructions.`,
};

// ---------------------------------------------------------------------------
// Draft state
// ---------------------------------------------------------------------------

interface PersonaDraft {
  name: string;
  displayName: string;
  description: string;
  roles: string[];
  allowTools: string[];
  denyTools: string[];
  allowConnections: string[];
  denyConnections: string[];
  priority: number;
  descriptionPrefix: string;
  descriptionOverride: string;
  agentInstructionsSuffix: string;
  agentInstructionsOverride: string;
}

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

// ---------------------------------------------------------------------------
// PersonasPanel — master-detail layout
// ---------------------------------------------------------------------------

export function PersonasPanel() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
  const { data: personaList, isLoading } = usePersonas();
  const personas = personaList?.personas ?? [];

  const [selected, setSelected] = useState<string | null>(null);
  const [mode, setMode] = useState<"view" | "edit" | "create">("view");
  const [draft, setDraft] = useState<PersonaDraft>(emptyDraft());
  const [dirty, setDirty] = useState(false);

  const { data: detail } = usePersonaDetail(selected);

  useEffect(() => {
    if (detail && mode === "view") {
      setDraft(detailToDraft(detail));
      setDirty(false);
    }
  }, [detail, mode]);

  useEffect(() => {
    if (!selected && personas.length > 0 && personas[0]) {
      setSelected(personas[0].name);
    }
  }, [personas, selected]);

  const handleSelect = useCallback(
    (name: string) => {
      if (dirty && !window.confirm("Discard unsaved changes?")) return;
      setSelected(name);
      setMode("view");
      setDirty(false);
    },
    [dirty],
  );

  const handleCreate = useCallback(() => {
    if (dirty && !window.confirm("Discard unsaved changes?")) return;
    setSelected(null);
    setDraft(emptyDraft());
    setMode("create");
    setDirty(false);
  }, [dirty]);

  const handleEdit = useCallback(() => {
    if (detail) {
      setDraft(detailToDraft(detail));
      setMode("edit");
      setDirty(false);
    }
  }, [detail]);

  const handleCancel = useCallback(() => {
    if (mode === "create") {
      if (personas.length > 0 && personas[0]) {
        setSelected(personas[0].name);
      }
      setMode("view");
    } else {
      setMode("view");
      if (detail) setDraft(detailToDraft(detail));
    }
    setDirty(false);
  }, [mode, detail, personas]);

  const updateDraft = useCallback((partial: Partial<PersonaDraft>) => {
    setDraft((prev) => ({ ...prev, ...partial }));
    setDirty(true);
  }, []);

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Loading personas...
      </div>
    );
  }

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
                selected === p.name && mode !== "create"
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
                mode === "create"
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

      {/* Right: Detail / Edit panel */}
      <div className="flex-1 overflow-auto">
        {mode === "create" ? (
          <PersonaEditor
            draft={draft}
            onUpdate={updateDraft}
            onSave={handleCancel}
            onCancel={handleCancel}
            isCreate
            dirty={dirty}
            selectedName={null}
          />
        ) : selected && detail ? (
          mode === "edit" ? (
            <PersonaEditor
              draft={draft}
              onUpdate={updateDraft}
              onSave={() => {
                setMode("view");
                setDirty(false);
              }}
              onCancel={handleCancel}
              isCreate={false}
              dirty={dirty}
              selectedName={selected}
            />
          ) : (
            <PersonaViewer
              detail={detail}
              isReadOnly={isReadOnly}
              onEdit={handleEdit}
              onDeleted={() => {
                setSelected(null);
                setMode("view");
              }}
            />
          )
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
    </div>
  );
}

// ---------------------------------------------------------------------------
// Help tooltip
// ---------------------------------------------------------------------------

function HelpText({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  return (
    <span className="relative inline-block">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        className="text-muted-foreground/50 hover:text-muted-foreground"
      >
        <HelpCircle className="h-3.5 w-3.5" />
      </button>
      {open && (
        <div className="absolute left-0 top-6 z-30 w-80 rounded-md border bg-popover p-3 text-xs text-popover-foreground shadow-lg leading-relaxed">
          {text}
        </div>
      )}
    </span>
  );
}

// ---------------------------------------------------------------------------
// Persona Viewer (read-only)
// ---------------------------------------------------------------------------

function PersonaViewer({
  detail,
  isReadOnly,
  onEdit,
  onDeleted,
}: {
  detail: PersonaDetail;
  isReadOnly: boolean;
  onEdit: () => void;
  onDeleted: () => void;
}) {
  const deleteMutation = useDeletePersona();
  const [confirmDelete, setConfirmDelete] = useState(false);

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h2 className="text-lg font-semibold">{detail.display_name}</h2>
            <span className="rounded bg-muted px-2 py-0.5 text-xs font-mono text-muted-foreground">
              {detail.name}
            </span>
          </div>
          {detail.description && (
            <p className="mt-1 text-sm text-muted-foreground">{detail.description}</p>
          )}
          {detail.source === "both" && (
            <p className="mt-1 text-xs text-muted-foreground">
              This persona is managed in the database. A fallback version also exists in the config file and can be removed once database management is confirmed.
            </p>
          )}
          {detail.source === "file" && !isReadOnly && (
            <p className="mt-1 text-xs text-muted-foreground">
              This persona is defined in the config file. Editing will create a database override.
            </p>
          )}
        </div>
        {!isReadOnly && (
          <div className="flex gap-2">
            <button type="button" onClick={onEdit} className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90">
              Edit
            </button>
            {detail.name !== "admin" && detail.source !== "file" && (
              <button type="button" onClick={() => setConfirmDelete(true)} className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-destructive/10 hover:text-destructive hover:border-destructive/30">
                <Trash2 className="h-3 w-3" />
              </button>
            )}
          </div>
        )}
      </div>

      <div className="grid grid-cols-3 gap-4">
        <InfoCard label="Priority" value={String(detail.priority)} />
        <InfoCard label="Resolved Tools" value={String(detail.tools.length)} />
        <InfoCard label="Roles" value={detail.roles.join(", ") || "None"} />
      </div>

      <ViewSection icon={Wrench} title="Tool Access Rules">
        <div className="grid grid-cols-2 gap-4">
          <div>
            <p className="mb-2 text-[11px] font-medium text-muted-foreground">Allow Patterns</p>
            {detail.allow_tools.length > 0 ? (
              <div className="flex flex-wrap gap-1">
                {detail.allow_tools.map((t) => (
                  <span key={t} className="rounded bg-green-100 px-2 py-0.5 text-xs font-mono text-green-800 dark:bg-green-900/30 dark:text-green-400">{t}</span>
                ))}
              </div>
            ) : (
              <p className="text-xs text-muted-foreground italic">None (all tools denied)</p>
            )}
          </div>
          <div>
            <p className="mb-2 text-[11px] font-medium text-muted-foreground">Deny Patterns</p>
            {detail.deny_tools.length > 0 ? (
              <div className="flex flex-wrap gap-1">
                {detail.deny_tools.map((t) => (
                  <span key={t} className="rounded bg-red-100 px-2 py-0.5 text-xs font-mono text-red-800 dark:bg-red-900/30 dark:text-red-400">{t}</span>
                ))}
              </div>
            ) : (
              <p className="text-xs text-muted-foreground italic">None</p>
            )}
          </div>
        </div>
      </ViewSection>

      {((detail.allow_connections && detail.allow_connections.length > 0) || (detail.deny_connections && detail.deny_connections.length > 0)) && (
        <ViewSection icon={Cable} title="Connection Access">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <p className="mb-2 text-[11px] font-medium text-muted-foreground">Allow Connections</p>
              {detail.allow_connections && detail.allow_connections.length > 0 ? (
                <div className="flex flex-wrap gap-1">
                  {detail.allow_connections.map((c) => (
                    <span key={c} className="rounded bg-green-100 px-2 py-0.5 text-xs font-mono text-green-800 dark:bg-green-900/30 dark:text-green-400">{c}</span>
                  ))}
                </div>
              ) : (
                <p className="text-xs text-muted-foreground italic">None (all connections permitted)</p>
              )}
            </div>
            <div>
              <p className="mb-2 text-[11px] font-medium text-muted-foreground">Deny Connections</p>
              {detail.deny_connections && detail.deny_connections.length > 0 ? (
                <div className="flex flex-wrap gap-1">
                  {detail.deny_connections.map((c) => (
                    <span key={c} className="rounded bg-red-100 px-2 py-0.5 text-xs font-mono text-red-800 dark:bg-red-900/30 dark:text-red-400">{c}</span>
                  ))}
                </div>
              ) : (
                <p className="text-xs text-muted-foreground italic">None</p>
              )}
            </div>
          </div>
        </ViewSection>
      )}

      {detail.tools.length > 0 && (
        <Collapsible title={`Resolved Tools (${detail.tools.length})`} defaultOpen={false}>
          <div className="flex flex-wrap gap-1">
            {detail.tools.map((t) => (
              <span key={t} className="rounded bg-muted px-1.5 py-0.5 text-xs font-mono text-muted-foreground">{t}</span>
            ))}
          </div>
        </Collapsible>
      )}

      {(detail.context?.description_prefix || detail.context?.description_override || detail.context?.agent_instructions_suffix || detail.context?.agent_instructions_override) && (
        <ViewSection icon={FileText} title="Context Overrides">
          {detail.context?.description_prefix && <PromptBlock label="Description Prefix" value={detail.context.description_prefix} />}
          {detail.context?.description_override && <PromptBlock label="Description Override" value={detail.context.description_override} />}
          {detail.context?.agent_instructions_suffix && <PromptBlock label="Agent Instructions Suffix" value={detail.context.agent_instructions_suffix} />}
          {detail.context?.agent_instructions_override && <PromptBlock label="Agent Instructions Override" value={detail.context.agent_instructions_override} />}
        </ViewSection>
      )}

      {confirmDelete && (
        <ConfirmModal
          title="Delete Persona"
          message={detail.source === "both"
            ? `Are you sure you want to remove the database override for "${detail.display_name}"? It will revert to the version defined in the config file.`
            : `Are you sure you want to delete "${detail.display_name}"? This cannot be undone.`}
          confirmLabel="Delete"
          onConfirm={() => {
            deleteMutation.mutate(detail.name, { onSuccess: onDeleted });
            setConfirmDelete(false);
          }}
          onCancel={() => setConfirmDelete(false)}
          destructive
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Persona Editor
// ---------------------------------------------------------------------------

function PersonaEditor({
  draft,
  onUpdate,
  onSave,
  onCancel,
  isCreate,
  dirty,
  selectedName,
}: {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  onSave: () => void;
  onCancel: () => void;
  isCreate: boolean;
  dirty: boolean;
  selectedName: string | null;
}) {
  const createMutation = useCreatePersona();
  const updateMutation = useUpdatePersona();
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const handleSave = useCallback(() => {
    setSaveError(null);
    const payload = {
      name: draft.name,
      display_name: draft.displayName,
      description: draft.description || undefined,
      roles: draft.roles,
      allow_tools: draft.allowTools,
      deny_tools: draft.denyTools,
      allow_connections: draft.allowConnections.length > 0 ? draft.allowConnections : undefined,
      deny_connections: draft.denyConnections.length > 0 ? draft.denyConnections : undefined,
      priority: draft.priority,
      description_prefix: draft.descriptionPrefix || undefined,
      description_override: draft.descriptionOverride || undefined,
      agent_instructions_suffix: draft.agentInstructionsSuffix || undefined,
      agent_instructions_override: draft.agentInstructionsOverride || undefined,
    };

    const mutation = isCreate ? createMutation : updateMutation;
    mutation.mutate(
      isCreate ? payload : { ...payload, name: selectedName! },
      {
        onSuccess: () => {
          setSaveSuccess(true);
          setTimeout(() => setSaveSuccess(false), 2000);
          onSave();
        },
        onError: (err) => {
          setSaveError(err instanceof Error ? err.message : "Failed to save");
        },
      },
    );
  }, [draft, isCreate, selectedName, createMutation, updateMutation, onSave]);

  const isPending = createMutation.isPending || updateMutation.isPending;

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b px-6 py-3 bg-muted/10">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold">
            {isCreate ? "New Persona" : `Edit: ${draft.displayName}`}
          </h2>
          {dirty && (
            <span className="flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400">
              <AlertCircle className="h-3 w-3" />
              Unsaved
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <button type="button" onClick={onCancel} className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted">
            Cancel
          </button>
          <button
            type="button"
            onClick={handleSave}
            disabled={isPending || (!dirty && !isCreate)}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-all disabled:opacity-50",
              saveSuccess ? "bg-green-600 text-white" : "bg-primary text-primary-foreground hover:bg-primary/90",
            )}
          >
            {saveSuccess ? (<><Check className="h-3 w-3" />Saved</>) : isPending ? "Saving..." : (<><Save className="h-3 w-3" />{isCreate ? "Create" : "Save"}</>)}
          </button>
        </div>
      </div>

      {saveError && (
        <div className="flex items-center gap-2 border-b bg-red-50 px-6 py-2 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-400">
          <AlertCircle className="h-3.5 w-3.5" />{saveError}
        </div>
      )}

      {/* Form */}
      <div className="flex-1 overflow-auto p-6 space-y-6">
        {/* Identity */}
        <EditSection icon={Shield} title="Identity">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="mb-1 block text-xs font-medium">Name (slug)</label>
              <input
                type="text"
                value={draft.name}
                onChange={(e) => onUpdate({ name: e.target.value })}
                disabled={!isCreate}
                placeholder="my-persona"
                className="w-full rounded-md border bg-background px-3 py-2 text-sm font-mono outline-none ring-ring focus:ring-2 disabled:opacity-50 disabled:cursor-not-allowed"
              />
              <p className="mt-1 text-xs text-muted-foreground">Unique identifier. Lowercase, hyphens allowed. Cannot be changed after creation.</p>
            </div>
            <div>
              <label className="mb-1 block text-xs font-medium">Display Name</label>
              <input
                type="text"
                value={draft.displayName}
                onChange={(e) => onUpdate({ displayName: e.target.value })}
                placeholder="My Persona"
                className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
              />
            </div>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium">Description</label>
            <textarea
              value={draft.description}
              onChange={(e) => onUpdate({ description: e.target.value })}
              rows={2}
              placeholder="What this persona is for..."
              className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2 resize-none"
            />
          </div>
          <div className="w-40">
            <label className="mb-1 flex items-center gap-1.5 text-xs font-medium">
              Priority <HelpText text={HELP.priority} />
            </label>
            <input
              type="number"
              value={draft.priority}
              onChange={(e) => onUpdate({ priority: parseInt(e.target.value, 10) || 0 })}
              className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
            />
          </div>
        </EditSection>

        {/* Roles */}
        <EditSection icon={Users} title="Roles" help={HELP.roles}>
          <TagInput
            items={draft.roles}
            onChange={(roles) => onUpdate({ roles })}
            placeholder="Add role (e.g. analyst, data_engineer)..."
            variant="neutral"
          />
        </EditSection>

        {/* Tool Access */}
        <EditSection icon={Wrench} title="Tool Access Rules">
          <div className="space-y-4">
            <div>
              <label className="mb-2 flex items-center gap-1.5 text-xs font-medium text-green-700 dark:text-green-400">
                Allow Patterns <HelpText text={HELP.allowTools} />
              </label>
              <ToolPatternEditor
                patterns={draft.allowTools}
                onChange={(allowTools) => onUpdate({ allowTools })}
                variant="green"
              />
            </div>
            <div>
              <label className="mb-2 flex items-center gap-1.5 text-xs font-medium text-red-700 dark:text-red-400">
                Deny Patterns <HelpText text={HELP.denyTools} />
              </label>
              <ToolPatternEditor
                patterns={draft.denyTools}
                onChange={(denyTools) => onUpdate({ denyTools })}
                variant="red"
              />
            </div>
          </div>
        </EditSection>

        {/* Connection Access */}
        <EditSection icon={Cable} title="Connection Access">
          <p className="text-xs text-muted-foreground mb-3">
            Connection-level access controls work alongside tool access. A tool call must pass both checks. If the allow list is empty, all connections are permitted. Deny rules override allow rules.
          </p>
          <div className="space-y-4">
            <div>
              <label className="mb-2 flex items-center gap-1.5 text-xs font-medium text-green-700 dark:text-green-400">
                Allow Connections <HelpText text={HELP.allowConnections} />
              </label>
              <ConnectionPatternEditor
                patterns={draft.allowConnections}
                onChange={(allowConnections) => onUpdate({ allowConnections })}
                variant="green"
              />
            </div>
            <div>
              <label className="mb-2 flex items-center gap-1.5 text-xs font-medium text-red-700 dark:text-red-400">
                Deny Connections <HelpText text={HELP.denyConnections} />
              </label>
              <ConnectionPatternEditor
                patterns={draft.denyConnections}
                onChange={(denyConnections) => onUpdate({ denyConnections })}
                variant="red"
              />
            </div>
          </div>
        </EditSection>

        {/* Context Overrides */}
        <EditSection icon={FileText} title="Context Overrides">
          <div className="space-y-4">
            <div>
              <label className="mb-1 flex items-center gap-1.5 text-xs font-medium">
                Description Prefix <HelpText text={HELP.descriptionPrefix} />
              </label>
              <MarkdownEditor value={draft.descriptionPrefix} onChange={(descriptionPrefix) => onUpdate({ descriptionPrefix })} placeholder="Prepended to the platform description..." minHeight="120px" />
            </div>
            <div>
              <label className="mb-1 flex items-center gap-1.5 text-xs font-medium">
                Description Override <HelpText text={HELP.descriptionOverride} />
              </label>
              <MarkdownEditor value={draft.descriptionOverride} onChange={(descriptionOverride) => onUpdate({ descriptionOverride })} placeholder="Replaces the platform description entirely..." minHeight="120px" />
            </div>
            <div>
              <label className="mb-1 flex items-center gap-1.5 text-xs font-medium">
                Agent Instructions Suffix <HelpText text={HELP.agentInstructionsSuffix} />
              </label>
              <MarkdownEditor value={draft.agentInstructionsSuffix} onChange={(agentInstructionsSuffix) => onUpdate({ agentInstructionsSuffix })} placeholder="Appended to the platform agent instructions..." minHeight="120px" />
            </div>
            <div>
              <label className="mb-1 flex items-center gap-1.5 text-xs font-medium">
                Agent Instructions Override <HelpText text={HELP.agentInstructionsOverride} />
              </label>
              <MarkdownEditor value={draft.agentInstructionsOverride} onChange={(agentInstructionsOverride) => onUpdate({ agentInstructionsOverride })} placeholder="Replaces the platform agent instructions entirely..." minHeight="120px" />
            </div>
          </div>
        </EditSection>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Tool Pattern Editor — autocomplete with actual tool names
// ---------------------------------------------------------------------------

function ToolPatternEditor({
  patterns,
  onChange,
  variant,
}: {
  patterns: string[];
  onChange: (patterns: string[]) => void;
  variant: "green" | "red";
}) {
  const { data: toolsData } = useTools();
  const allTools = toolsData?.tools ?? [];
  const [input, setInput] = useState("");
  const [showPicker, setShowPicker] = useState(false);
  const [search, setSearch] = useState("");

  // Group tools by toolkit kind
  const toolsByKind = useMemo(() => {
    const groups: Record<string, ToolInfo[]> = {};
    for (const t of allTools) {
      const key = t.kind || "other";
      if (!groups[key]) groups[key] = [];
      groups[key]!.push(t);
    }
    return groups;
  }, [allTools]);

  // Filter by search
  const filteredGroups = useMemo(() => {
    if (!search) return toolsByKind;
    const s = search.toLowerCase();
    const result: Record<string, ToolInfo[]> = {};
    for (const [kind, tools] of Object.entries(toolsByKind)) {
      const filtered = tools.filter(
        (t) => t.name.toLowerCase().includes(s) || (t.title ?? "").toLowerCase().includes(s) || kind.toLowerCase().includes(s),
      );
      if (filtered.length > 0) result[kind] = filtered;
    }
    return result;
  }, [toolsByKind, search]);

  const colorClasses = variant === "green"
    ? "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400"
    : "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400";

  const add = (val: string) => {
    const trimmed = val.trim();
    if (trimmed && !patterns.includes(trimmed)) {
      onChange([...patterns, trimmed]);
    }
    setInput("");
  };

  const addWildcard = (kind: string) => {
    const pattern = `${kind}_*`;
    if (!patterns.includes(pattern)) {
      onChange([...patterns, pattern]);
    }
  };

  return (
    <div>
      {/* Current patterns */}
      <div className="flex flex-wrap gap-1.5 mb-2">
        {patterns.map((p) => (
          <span key={p} className={cn("inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs font-mono", colorClasses)}>
            {p}
            <button type="button" onClick={() => onChange(patterns.filter((x) => x !== p))} className="opacity-60 hover:opacity-100">
              <X className="h-2.5 w-2.5" />
            </button>
          </span>
        ))}
        {patterns.length === 0 && (
          <span className="text-xs text-muted-foreground italic">No patterns — {variant === "green" ? "no tools allowed" : "nothing denied"}</span>
        )}
      </div>

      {/* Input row */}
      <div className="flex gap-2">
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(input); } }}
          placeholder="Type a pattern (e.g. trino_*) or use the picker..."
          className="flex-1 rounded-md border bg-background px-3 py-1.5 text-xs font-mono outline-none ring-ring focus:ring-2"
        />
        <button type="button" onClick={() => add(input)} disabled={!input.trim()} className="rounded-md border px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-30">
          <Plus className="h-3 w-3" />
        </button>
        <button
          type="button"
          onClick={() => { setShowPicker((v) => !v); setSearch(""); }}
          className={cn("rounded-md border px-2.5 py-1.5 text-xs transition-colors", showPicker ? "bg-primary/10 text-primary border-primary/30" : "text-muted-foreground hover:bg-muted hover:text-foreground")}
          title="Browse available tools"
        >
          <Search className="h-3 w-3" />
        </button>
      </div>

      {/* Tool picker dropdown */}
      {showPicker && (
        <div className="mt-2 rounded-md border bg-card shadow-sm max-h-64 overflow-hidden flex flex-col">
          <div className="border-b px-3 py-2">
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search tools..."
              className="w-full bg-transparent text-xs outline-none placeholder:text-muted-foreground"
              autoFocus
            />
          </div>
          <div className="flex-1 overflow-auto">
            {Object.entries(filteredGroups).sort(([a], [b]) => a.localeCompare(b)).map(([kind, tools]) => (
              <div key={kind}>
                <div className="flex items-center justify-between bg-muted/30 px-3 py-1.5 border-b">
                  <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{kind}</span>
                  <button
                    type="button"
                    onClick={() => addWildcard(kind)}
                    disabled={patterns.includes(`${kind}_*`)}
                    className="text-xs font-mono text-primary hover:underline disabled:opacity-30 disabled:no-underline"
                  >
                    {kind}_*
                  </button>
                </div>
                {tools.map((t) => {
                  const alreadyAdded = patterns.includes(t.name);
                  return (
                    <button
                      key={t.name}
                      type="button"
                      onClick={() => { if (!alreadyAdded) add(t.name); }}
                      disabled={alreadyAdded}
                      className={cn(
                        "flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs border-b last:border-0 transition-colors",
                        alreadyAdded ? "opacity-40 cursor-not-allowed" : "hover:bg-muted/50",
                      )}
                    >
                      <span className="font-mono flex-1">{t.name}</span>
                      {t.title && <span className="text-muted-foreground truncate max-w-[200px]">{t.title}</span>}
                      {alreadyAdded && <Check className="h-3 w-3 text-green-600" />}
                    </button>
                  );
                })}
              </div>
            ))}
            {Object.keys(filteredGroups).length === 0 && (
              <div className="px-3 py-4 text-center text-xs text-muted-foreground">No tools match "{search}"</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Connection Pattern Editor — picker with available connections
// ---------------------------------------------------------------------------

function ConnectionPatternEditor({
  patterns,
  onChange,
  variant,
}: {
  patterns: string[];
  onChange: (patterns: string[]) => void;
  variant: "green" | "red";
}) {
  const { data: connectionsData } = useConnections();
  const allConnections = connectionsData?.connections ?? [];
  const [input, setInput] = useState("");
  const [showPicker, setShowPicker] = useState(false);
  const [search, setSearch] = useState("");

  // Group connections by kind
  const connectionsByKind = useMemo(() => {
    const groups: Record<string, ConnectionInfo[]> = {};
    for (const c of allConnections) {
      const key = c.kind || "other";
      if (!groups[key]) groups[key] = [];
      groups[key]!.push(c);
    }
    return groups;
  }, [allConnections]);

  // Filter by search
  const filteredGroups = useMemo(() => {
    if (!search) return connectionsByKind;
    const s = search.toLowerCase();
    const result: Record<string, ConnectionInfo[]> = {};
    for (const [kind, conns] of Object.entries(connectionsByKind)) {
      const filtered = conns.filter(
        (c) => c.name.toLowerCase().includes(s) || c.connection.toLowerCase().includes(s) || kind.toLowerCase().includes(s),
      );
      if (filtered.length > 0) result[kind] = filtered;
    }
    return result;
  }, [connectionsByKind, search]);

  const colorClasses = variant === "green"
    ? "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400"
    : "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400";

  const add = (val: string) => {
    const trimmed = val.trim();
    if (trimmed && !patterns.includes(trimmed)) {
      onChange([...patterns, trimmed]);
    }
    setInput("");
  };

  return (
    <div>
      {/* Current patterns */}
      <div className="flex flex-wrap gap-1.5 mb-2">
        {patterns.map((p) => (
          <span key={p} className={cn("inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs font-mono", colorClasses)}>
            {p}
            <button type="button" onClick={() => onChange(patterns.filter((x) => x !== p))} className="opacity-60 hover:opacity-100">
              <X className="h-2.5 w-2.5" />
            </button>
          </span>
        ))}
        {patterns.length === 0 && (
          <span className="text-xs text-muted-foreground italic">
            {variant === "green" ? "None (all connections permitted)" : "Nothing denied"}
          </span>
        )}
      </div>

      {/* Input row */}
      <div className="flex gap-2">
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(input); } }}
          placeholder="Type a connection id (e.g. trino/primary) or use the picker..."
          className="flex-1 rounded-md border bg-background px-3 py-1.5 text-xs font-mono outline-none ring-ring focus:ring-2"
        />
        <button type="button" onClick={() => add(input)} disabled={!input.trim()} className="rounded-md border px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-30">
          <Plus className="h-3 w-3" />
        </button>
        <button
          type="button"
          onClick={() => { setShowPicker((v) => !v); setSearch(""); }}
          className={cn("rounded-md border px-2.5 py-1.5 text-xs transition-colors", showPicker ? "bg-primary/10 text-primary border-primary/30" : "text-muted-foreground hover:bg-muted hover:text-foreground")}
          title="Browse available connections"
        >
          <Search className="h-3 w-3" />
        </button>
      </div>

      {/* Connection picker dropdown */}
      {showPicker && (
        <div className="mt-2 rounded-md border bg-card shadow-sm max-h-64 overflow-hidden flex flex-col">
          <div className="border-b px-3 py-2">
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search connections..."
              className="w-full bg-transparent text-xs outline-none placeholder:text-muted-foreground"
              autoFocus
            />
          </div>
          <div className="flex-1 overflow-auto">
            {Object.entries(filteredGroups).sort(([a], [b]) => a.localeCompare(b)).map(([kind, conns]) => (
              <div key={kind}>
                <div className="bg-muted/30 px-3 py-1.5 border-b">
                  <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{kind}</span>
                </div>
                {conns.map((c) => {
                  const connId = c.connection;
                  const alreadyAdded = patterns.includes(connId);
                  return (
                    <button
                      key={connId}
                      type="button"
                      onClick={() => { if (!alreadyAdded) add(connId); }}
                      disabled={alreadyAdded}
                      className={cn(
                        "flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs border-b last:border-0 transition-colors",
                        alreadyAdded ? "opacity-40 cursor-not-allowed" : "hover:bg-muted/50",
                      )}
                    >
                      <span className="font-mono flex-1">{connId}</span>
                      <span className="text-muted-foreground truncate max-w-[150px]">{c.name}</span>
                      {alreadyAdded && <Check className="h-3 w-3 text-green-600" />}
                    </button>
                  );
                })}
              </div>
            ))}
            {Object.keys(filteredGroups).length === 0 && (
              <div className="px-3 py-4 text-center text-xs text-muted-foreground">
                {search ? `No connections match "${search}"` : "No connections available"}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Shared UI primitives
// ---------------------------------------------------------------------------

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border bg-muted/20 px-3 py-2">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className="text-sm font-medium truncate">{value}</p>
    </div>
  );
}

function ViewSection({ icon: Icon, title, children }: { icon: typeof Shield; title: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-3 flex items-center gap-2">
        <Icon className="h-4 w-4 text-muted-foreground" />
        <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{title}</h3>
      </div>
      {children}
    </div>
  );
}

function EditSection({ icon: Icon, title, help, children }: { icon: typeof Shield; title: string; help?: string; children: React.ReactNode }) {
  const [open, setOpen] = useState(true);
  return (
    <div className="rounded-lg border">
      <button type="button" onClick={() => setOpen((v) => !v)} className="flex w-full items-center gap-2 px-4 py-3 text-left">
        <Icon className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium flex-1">{title}</span>
        {help && <HelpText text={help} />}
        <ChevronDown className={cn("h-3.5 w-3.5 text-muted-foreground transition-transform", !open && "-rotate-90")} />
      </button>
      {open && <div className="border-t px-4 py-4 space-y-4">{children}</div>}
    </div>
  );
}

function Collapsible({ title, defaultOpen = true, children }: { title: string; defaultOpen?: boolean; children: React.ReactNode }) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div>
      <button type="button" onClick={() => setOpen((v) => !v)} className="mb-2 flex w-full items-center justify-between text-xs font-medium text-muted-foreground">
        <span>{title}</span>
        <ChevronDown className={cn("h-3 w-3 transition-transform", !open && "-rotate-90")} />
      </button>
      {open && children}
    </div>
  );
}

function PromptBlock({ label, value }: { label: string; value: string }) {
  return (
    <div className="mb-3">
      <p className="mb-1 text-[11px] text-muted-foreground">{label}</p>
      <pre className="overflow-auto rounded-md border bg-muted/30 p-3 text-xs whitespace-pre-wrap break-words max-h-48">{value}</pre>
    </div>
  );
}

function TagInput({
  items,
  onChange,
  placeholder,
  variant,
}: {
  items: string[];
  onChange: (items: string[]) => void;
  placeholder: string;
  variant: "neutral" | "green" | "red";
}) {
  const [input, setInput] = useState("");
  const colors = { neutral: "bg-muted text-foreground", green: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400", red: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400" };
  const add = () => {
    const val = input.trim();
    if (val && !items.includes(val)) { onChange([...items, val]); setInput(""); }
  };
  return (
    <div>
      <div className="flex flex-wrap gap-1.5 mb-2">
        {items.map((item) => (
          <span key={item} className={cn("inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs", colors[variant])}>
            {item}
            <button type="button" onClick={() => onChange(items.filter((i) => i !== item))} className="opacity-60 hover:opacity-100"><X className="h-2.5 w-2.5" /></button>
          </span>
        ))}
      </div>
      <div className="flex gap-2">
        <input type="text" value={input} onChange={(e) => setInput(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }} placeholder={placeholder} className="flex-1 rounded-md border bg-background px-3 py-1.5 text-xs outline-none ring-ring focus:ring-2" />
        <button type="button" onClick={add} disabled={!input.trim()} className="rounded-md border px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-30"><Plus className="h-3 w-3" /></button>
      </div>
    </div>
  );
}

function ConfirmModal({ title, message, confirmLabel, onConfirm, onCancel, destructive }: { title: string; message: string; confirmLabel: string; onConfirm: () => void; onCancel: () => void; destructive?: boolean }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onCancel}>
      <div className="rounded-lg border bg-card p-6 shadow-lg max-w-sm mx-4" onClick={(e) => e.stopPropagation()}>
        <h3 className="text-sm font-semibold mb-2">{title}</h3>
        <p className="text-sm text-muted-foreground mb-4">{message}</p>
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onCancel} className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted">Cancel</button>
          <button type="button" onClick={onConfirm} className={cn("rounded-md px-3 py-1.5 text-xs font-medium", destructive ? "bg-destructive text-destructive-foreground hover:bg-destructive/90" : "bg-primary text-primary-foreground hover:bg-primary/90")}>{confirmLabel}</button>
        </div>
      </div>
    </div>
  );
}
