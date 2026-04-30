import { useState, useCallback } from "react";
import {
  usePersonas,
  usePersonaDetail,
  useCreatePersona,
  useUpdatePersona,
  useDeletePersona,
  useSystemInfo,
} from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { PersonaDetail, PersonaSummary } from "@/api/admin/types";
import { Plus } from "lucide-react";

type Tab = "overview" | "help";

const TAB_ITEMS: { key: Tab; label: string }[] = [
  { key: "overview", label: "Overview" },
  { key: "help", label: "Help" },
];

export function PersonasPage({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState<Tab>(
    initialTab === "help" ? "help" : "overview",
  );
  const [selectedPersona, setSelectedPersona] = useState<string | null>(null);
  const [formMode, setFormMode] = useState<"create" | "edit" | null>(null);
  const [editingPersona, setEditingPersona] = useState<string | null>(null);

  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";

  const openDetail = useCallback((name: string) => {
    setSelectedPersona(name);
    setFormMode(null);
    setEditingPersona(null);
  }, []);

  const openCreate = useCallback(() => {
    setFormMode("create");
    setEditingPersona(null);
    setSelectedPersona(null);
  }, []);

  const openEdit = useCallback((name: string) => {
    setFormMode("edit");
    setEditingPersona(name);
    setSelectedPersona(null);
  }, []);

  const closeDrawer = useCallback(() => {
    setSelectedPersona(null);
    setFormMode(null);
    setEditingPersona(null);
  }, []);

  return (
    <div className="space-y-4">
      {/* Tab bar */}
      <div className="flex gap-1 border-b">
        {TAB_ITEMS.map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`px-4 py-2 text-sm font-medium transition-colors ${
              tab === t.key
                ? "border-b-2 border-primary text-primary"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === "overview" && (
        <OverviewTab
          isReadOnly={isReadOnly}
          onSelect={openDetail}
          onCreate={openCreate}
        />
      )}
      {tab === "help" && <HelpTab />}

      {/* Detail drawer */}
      {selectedPersona && (
        <DetailDrawer
          name={selectedPersona}
          isReadOnly={isReadOnly}
          onClose={closeDrawer}
          onEdit={openEdit}
        />
      )}

      {/* Form drawer */}
      {formMode && (
        <FormDrawer
          mode={formMode}
          editName={editingPersona}
          onClose={closeDrawer}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview Tab — Rich persona cards + "add" card
// ---------------------------------------------------------------------------

function OverviewTab({
  isReadOnly,
  onSelect,
  onCreate,
}: {
  isReadOnly: boolean;
  onSelect: (name: string) => void;
  onCreate: () => void;
}) {
  const { data } = usePersonas();
  const personas = data?.personas ?? [];

  return (
    <div className="space-y-4">
      {/* Read-only banner */}
      {isReadOnly && (
        <div className="rounded-md border border-yellow-200 bg-yellow-50 px-4 py-3 text-sm text-yellow-800">
          Configuration is file-based. Personas are read-only.
        </div>
      )}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {personas.map((p) => (
          <PersonaCard key={p.name} persona={p} onSelect={onSelect} />
        ))}

        {/* Add new persona card */}
        {!isReadOnly && (
          <button
            onClick={onCreate}
            className="flex min-h-[200px] flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed border-muted-foreground/25 bg-card text-muted-foreground transition-colors hover:border-primary/50 hover:text-primary"
          >
            <Plus className="h-8 w-8" />
            <span className="text-sm font-medium">Add Persona</span>
          </button>
        )}

        {personas.length === 0 && isReadOnly && (
          <p className="col-span-full py-8 text-center text-sm text-muted-foreground">
            No personas configured
          </p>
        )}
      </div>
    </div>
  );
}

function PersonaCard({
  persona,
  onSelect,
}: {
  persona: PersonaSummary;
  onSelect: (name: string) => void;
}) {
  const { data: detail } = usePersonaDetail(persona.name);

  return (
    <button
      onClick={() => onSelect(persona.name)}
      className="flex flex-col rounded-lg border bg-card p-4 text-left transition-colors hover:border-primary/50"
    >
      {/* Header */}
      <div className="mb-2 flex items-center gap-2">
        <h3 className="text-sm font-semibold">{persona.display_name}</h3>
        <span className="rounded bg-muted px-1.5 py-0.5 text-xs font-mono text-muted-foreground">
          {persona.name}
        </span>
      </div>

      {/* Description */}
      {persona.description && (
        <p className="mb-3 text-xs text-muted-foreground">
          {persona.description}
        </p>
      )}

      {/* Roles */}
      <div className="mb-3 flex flex-wrap gap-1">
        {persona.roles.map((r) => (
          <StatusBadge key={r} variant="neutral">
            {r}
          </StatusBadge>
        ))}
      </div>

      {/* Tool rules */}
      {detail && (
        <div className="mb-3 space-y-1">
          {detail.allow_tools.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {detail.allow_tools.map((t) => (
                <span
                  key={t}
                  className="rounded bg-green-100 px-1.5 py-0.5 text-xs font-mono text-green-800"
                >
                  {t}
                </span>
              ))}
            </div>
          )}
          {detail.deny_tools.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {detail.deny_tools.map((t) => (
                <span
                  key={t}
                  className="rounded bg-red-100 px-1.5 py-0.5 text-xs font-mono text-red-800"
                >
                  {t}
                </span>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Context snippet */}
      {detail?.context?.description_prefix && (
        <p className="mb-3 line-clamp-2 text-xs text-muted-foreground italic">
          {detail.context.description_prefix.trim()}
        </p>
      )}

      {/* Footer stats */}
      <div className="mt-auto flex items-center gap-3 text-xs text-muted-foreground">
        <span>{persona.tool_count} tools</span>
      </div>
    </button>
  );
}

// ---------------------------------------------------------------------------
// Detail Drawer
// ---------------------------------------------------------------------------

function DetailDrawer({
  name,
  isReadOnly,
  onClose,
  onEdit,
}: {
  name: string;
  isReadOnly: boolean;
  onClose: () => void;
  onEdit: (name: string) => void;
}) {
  const { data: detail } = usePersonaDetail(name);
  const deleteMutation = useDeletePersona();
  const [toolsExpanded, setToolsExpanded] = useState(false);

  const handleDelete = useCallback(() => {
    if (!window.confirm(`Delete persona "${name}"?`)) return;
    deleteMutation.mutate(name, { onSuccess: () => onClose() });
  }, [name, deleteMutation, onClose]);

  if (!detail) {
    return (
      <DrawerShell onClose={onClose}>
        <p className="text-sm text-muted-foreground">Loading...</p>
      </DrawerShell>
    );
  }

  return (
    <DrawerShell onClose={onClose}>
      <div className="space-y-5">
        {/* Header */}
        <div>
          <div className="flex items-center gap-2">
            <h2 className="text-lg font-semibold">{detail.display_name}</h2>
            <span className="rounded bg-muted px-2 py-0.5 text-xs font-mono text-muted-foreground">
              {detail.name}
            </span>
          </div>
          {detail.description && (
            <p className="mt-1 text-sm text-muted-foreground">
              {detail.description}
            </p>
          )}
        </div>

        {/* Metadata grid */}
        <div className="grid grid-cols-2 gap-3 text-sm">
          <div>
            <p className="text-xs text-muted-foreground">Priority</p>
            <p className="font-medium">{detail.priority}</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Tool Count</p>
            <p className="font-medium">{detail.tools.length}</p>
          </div>
          <div className="col-span-2">
            <p className="text-xs text-muted-foreground">Roles</p>
            <div className="mt-1 flex flex-wrap gap-1">
              {detail.roles.map((r) => (
                <StatusBadge key={r} variant="neutral">
                  {r}
                </StatusBadge>
              ))}
            </div>
          </div>
        </div>

        {/* Tool Access Rules */}
        <div>
          <p className="mb-2 text-xs font-medium text-muted-foreground">
            Tool Access Rules
          </p>
          {detail.allow_tools.length > 0 && (
            <div className="mb-2">
              <p className="mb-1 text-[11px] text-muted-foreground">Allow</p>
              <div className="flex flex-wrap gap-1">
                {detail.allow_tools.map((t) => (
                  <span
                    key={t}
                    className="rounded bg-green-100 px-2 py-0.5 text-xs font-mono text-green-800"
                  >
                    {t}
                  </span>
                ))}
              </div>
            </div>
          )}
          {detail.deny_tools.length > 0 && (
            <div>
              <p className="mb-1 text-[11px] text-muted-foreground">Deny</p>
              <div className="flex flex-wrap gap-1">
                {detail.deny_tools.map((t) => (
                  <span
                    key={t}
                    className="rounded bg-red-100 px-2 py-0.5 text-xs font-mono text-red-800"
                  >
                    {t}
                  </span>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* Resolved Tools (collapsible) */}
        {detail.tools.length > 0 && (
          <div>
            <button
              onClick={() => setToolsExpanded((v) => !v)}
              className="mb-2 flex w-full items-center justify-between text-xs font-medium text-muted-foreground"
            >
              <span>Resolved Tools ({detail.tools.length})</span>
              <span>{toolsExpanded ? "Collapse" : "Expand"}</span>
            </button>
            {toolsExpanded && (
              <div className="flex flex-wrap gap-1">
                {detail.tools.map((t) => (
                  <span
                    key={t}
                    className="rounded bg-muted px-1.5 py-0.5 text-xs font-mono text-muted-foreground"
                  >
                    {t}
                  </span>
                ))}
              </div>
            )}
          </div>
        )}

        {/* Context Overrides */}
        {detail.context && hasContextContent(detail.context) && (
          <div>
            <p className="mb-2 text-xs font-medium text-muted-foreground">
              Context Overrides
            </p>
            {detail.context.description_prefix && (
              <div className="mb-2">
                <p className="mb-1 text-[11px] text-muted-foreground">
                  Description Prefix
                </p>
                <pre className="overflow-auto rounded bg-muted p-3 text-xs whitespace-pre-wrap">
                  {detail.context.description_prefix}
                </pre>
              </div>
            )}
            {detail.context.description_override && (
              <div className="mb-2">
                <p className="mb-1 text-[11px] text-muted-foreground">
                  Description Override
                </p>
                <pre className="overflow-auto rounded bg-muted p-3 text-xs whitespace-pre-wrap">
                  {detail.context.description_override}
                </pre>
              </div>
            )}
            {detail.context.agent_instructions_suffix && (
              <div className="mb-2">
                <p className="mb-1 text-[11px] text-muted-foreground">
                  Agent Instructions Suffix
                </p>
                <pre className="overflow-auto rounded bg-muted p-3 text-xs whitespace-pre-wrap">
                  {detail.context.agent_instructions_suffix}
                </pre>
              </div>
            )}
            {detail.context.agent_instructions_override && (
              <div>
                <p className="mb-1 text-[11px] text-muted-foreground">
                  Agent Instructions Override
                </p>
                <pre className="overflow-auto rounded bg-muted p-3 text-xs whitespace-pre-wrap">
                  {detail.context.agent_instructions_override}
                </pre>
              </div>
            )}
          </div>
        )}

        {/* Footer actions */}
        <div className="flex gap-2 border-t pt-3">
          {!isReadOnly && (
            <>
              <button
                onClick={() => {
                  onClose();
                  onEdit(name);
                }}
                className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
              >
                Edit
              </button>
              {name !== "admin" && (
                <button
                  onClick={handleDelete}
                  disabled={deleteMutation.isPending}
                  className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-50"
                >
                  Delete
                </button>
              )}
            </>
          )}
          <button
            onClick={onClose}
            className="rounded-md border px-4 py-2 text-sm font-medium hover:bg-muted"
          >
            Close
          </button>
        </div>
      </div>
    </DrawerShell>
  );
}

function hasContextContent(
  context: PersonaDetail["context"],
): context is NonNullable<PersonaDetail["context"]> {
  if (!context) return false;
  return !!(
    context.description_prefix ||
    context.description_override ||
    context.agent_instructions_suffix ||
    context.agent_instructions_override
  );
}

// ---------------------------------------------------------------------------
// Form Drawer — Create / Edit
// ---------------------------------------------------------------------------

function FormDrawer({
  mode,
  editName,
  onClose,
}: {
  mode: "create" | "edit";
  editName: string | null;
  onClose: () => void;
}) {
  const { data: detail } = usePersonaDetail(mode === "edit" ? editName : null);
  const createMutation = useCreatePersona();
  const updateMutation = useUpdatePersona();

  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [description, setDescription] = useState("");
  const [roles, setRoles] = useState("");
  const [allowTools, setAllowTools] = useState("");
  const [denyTools, setDenyTools] = useState("");
  const [priority, setPriority] = useState(0);
  const [loaded, setLoaded] = useState(false);

  // Pre-fill form when editing
  if (mode === "edit" && detail && !loaded) {
    setName(detail.name);
    setDisplayName(detail.display_name);
    setDescription(detail.description ?? "");
    setRoles(detail.roles.join(", "));
    setAllowTools(detail.allow_tools.join("\n"));
    setDenyTools(detail.deny_tools.join("\n"));
    setPriority(detail.priority);
    setLoaded(true);
  }

  const handleSubmit = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault();

      const rolesArr = roles
        .split(",")
        .map((r) => r.trim())
        .filter(Boolean);
      const allowArr = allowTools
        .split("\n")
        .map((l) => l.trim())
        .filter(Boolean);
      const denyArr = denyTools
        .split("\n")
        .map((l) => l.trim())
        .filter(Boolean);

      if (mode === "create") {
        createMutation.mutate(
          {
            name,
            display_name: displayName,
            description: description || undefined,
            roles: rolesArr,
            allow_tools: allowArr,
            deny_tools: denyArr,
            priority,
          },
          { onSuccess: () => onClose() },
        );
      } else if (editName) {
        updateMutation.mutate(
          {
            name: editName,
            display_name: displayName,
            description: description || undefined,
            roles: rolesArr,
            allow_tools: allowArr,
            deny_tools: denyArr,
            priority,
          },
          { onSuccess: () => onClose() },
        );
      }
    },
    [
      mode,
      name,
      editName,
      displayName,
      description,
      roles,
      allowTools,
      denyTools,
      priority,
      createMutation,
      updateMutation,
      onClose,
    ],
  );

  const isPending = createMutation.isPending || updateMutation.isPending;

  return (
    <DrawerShell onClose={onClose}>
      <h2 className="mb-4 text-lg font-semibold">
        {mode === "create" ? "Create Persona" : "Edit Persona"}
      </h2>
      <form onSubmit={handleSubmit} className="space-y-4">
        {/* Name */}
        <div>
          <label className="mb-1 block text-xs font-medium">
            Name <span className="text-red-500">*</span>
          </label>
          <input
            type="text"
            value={mode === "edit" ? editName ?? "" : name}
            onChange={(e) => setName(e.target.value)}
            disabled={mode === "edit"}
            required
            placeholder="my-persona"
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2 disabled:opacity-50"
          />
        </div>

        {/* Display Name */}
        <div>
          <label className="mb-1 block text-xs font-medium">
            Display Name <span className="text-red-500">*</span>
          </label>
          <input
            type="text"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
            placeholder="My Persona"
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>

        {/* Description */}
        <div>
          <label className="mb-1 block text-xs font-medium">Description</label>
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={2}
            placeholder="Optional description..."
            className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>

        {/* Roles */}
        <div>
          <label className="mb-1 block text-xs font-medium">Roles</label>
          <input
            type="text"
            value={roles}
            onChange={(e) => setRoles(e.target.value)}
            placeholder="role1, role2"
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
          />
          <p className="mt-0.5 text-[11px] text-muted-foreground">
            Comma-separated list of roles
          </p>
        </div>

        {/* Allow Tools */}
        <div>
          <label className="mb-1 block text-xs font-medium">Allow Tools</label>
          <textarea
            value={allowTools}
            onChange={(e) => setAllowTools(e.target.value)}
            rows={3}
            placeholder={"trino_*\ndatahub_search"}
            className="w-full rounded-md border bg-background px-3 py-2 font-mono text-sm outline-none ring-ring focus:ring-2"
          />
          <p className="mt-0.5 text-[11px] text-muted-foreground">
            One pattern per line. Wildcards supported, e.g. trino_*
          </p>
        </div>

        {/* Deny Tools */}
        <div>
          <label className="mb-1 block text-xs font-medium">Deny Tools</label>
          <textarea
            value={denyTools}
            onChange={(e) => setDenyTools(e.target.value)}
            rows={2}
            placeholder="s3_delete_*"
            className="w-full rounded-md border bg-background px-3 py-2 font-mono text-sm outline-none ring-ring focus:ring-2"
          />
          <p className="mt-0.5 text-[11px] text-muted-foreground">
            One pattern per line
          </p>
        </div>

        {/* Priority */}
        <div>
          <label className="mb-1 block text-xs font-medium">Priority</label>
          <input
            type="number"
            value={priority}
            onChange={(e) => setPriority(parseInt(e.target.value, 10) || 0)}
            className="w-32 rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>

        {/* Buttons */}
        <div className="flex gap-2 border-t pt-3">
          <button
            type="submit"
            disabled={isPending}
            className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            {isPending ? "Saving..." : "Save"}
          </button>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border px-4 py-2 text-sm font-medium hover:bg-muted"
          >
            Cancel
          </button>
        </div>
      </form>
    </DrawerShell>
  );
}

// ---------------------------------------------------------------------------
// Help Tab — Persona system documentation
// ---------------------------------------------------------------------------

function HelpTab() {
  return (
    <div className="max-w-3xl space-y-8">
      <section>
        <h2 className="mb-2 text-lg font-semibold">What are Personas?</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Personas control what each user can do on the platform. Every user
          is assigned a persona based on their role, and that persona
          determines which tools they can access and how the AI assistant
          behaves for them.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">What You&apos;ll Find Here</h2>
        <ul className="list-inside list-disc space-y-1 text-sm text-muted-foreground">
          <li>
            <strong>View all personas</strong> and how many tools each one
            grants access to
          </li>
          <li>
            <strong>See the resolved tool list</strong> for each persona
            &mdash; exactly which tools a user with that persona can use
          </li>
          <li>
            <strong>Create, edit, or remove personas</strong> (when the
            platform is in database mode)
          </li>
        </ul>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">How Tool Access Works</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Each persona has <strong>allow</strong> and <strong>deny</strong>{" "}
          rules that use wildcard patterns:
        </p>
        <ol className="mb-3 list-inside list-decimal space-y-1 text-sm text-muted-foreground">
          <li>
            <strong>Deny rules are checked first</strong> &mdash; if a tool
            matches any deny pattern, access is blocked
          </li>
          <li>
            <strong>Then allow rules</strong> &mdash; the tool must match an
            allow pattern to be accessible
          </li>
          <li>
            <strong>If nothing matches, access is denied</strong> &mdash;
            users only get what they&apos;re explicitly granted
          </li>
        </ol>
        <p className="text-sm leading-relaxed text-muted-foreground">
          For example, an analyst persona might allow all query and catalog
          tools but deny any delete or write operations.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">How Users Get Assigned</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Users are matched to personas through their roles. When someone
          logs in, the platform looks at their roles and assigns the
          matching persona. If a user&apos;s roles match multiple personas,
          the one with the highest priority wins. If no match is found,
          they get the default persona.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Customizing the Assistant</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Each persona can include custom instructions that change how the AI
          assistant behaves. You can set a system prompt (to establish the
          assistant&apos;s focus area) and per-tool hints (to guide the
          assistant toward best practices for that persona&apos;s workflow).
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">The Admin Persona</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          The admin persona has access to all tools and cannot be deleted.
          This ensures there is always at least one persona with full
          platform access.
        </p>
      </section>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Drawer Shell — shared slide-over layout
// ---------------------------------------------------------------------------

function DrawerShell({
  children,
  onClose,
}: {
  children: React.ReactNode;
  onClose: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="relative w-full max-w-lg overflow-auto bg-card p-6 shadow-xl">
        {children}
      </div>
    </div>
  );
}
