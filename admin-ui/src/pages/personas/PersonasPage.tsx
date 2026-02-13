import { useState, useCallback } from "react";
import {
  usePersonas,
  usePersonaDetail,
  useCreatePersona,
  useUpdatePersona,
  useDeletePersona,
  useSystemInfo,
} from "@/api/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { PersonaDetail, PersonaSummary } from "@/api/types";
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
        <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-mono text-muted-foreground">
          {persona.name}
        </span>
      </div>

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
                  className="rounded bg-green-100 px-1.5 py-0.5 text-[10px] font-mono text-green-800"
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
                  className="rounded bg-red-100 px-1.5 py-0.5 text-[10px] font-mono text-red-800"
                >
                  {t}
                </span>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Prompt snippet */}
      {detail?.prompts?.system_prefix && (
        <p className="mb-3 line-clamp-2 text-xs text-muted-foreground italic">
          {detail.prompts.system_prefix.trim()}
        </p>
      )}

      {/* Footer stats */}
      <div className="mt-auto flex items-center gap-3 text-xs text-muted-foreground">
        <span>{persona.tool_count} tools</span>
        {detail?.hints && Object.keys(detail.hints).length > 0 && (
          <span>{Object.keys(detail.hints).length} hints</span>
        )}
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
                    className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-mono text-muted-foreground"
                  >
                    {t}
                  </span>
                ))}
              </div>
            )}
          </div>
        )}

        {/* Prompts */}
        {detail.prompts && hasPromptContent(detail.prompts) && (
          <div>
            <p className="mb-2 text-xs font-medium text-muted-foreground">
              Prompts
            </p>
            {detail.prompts.system_prefix && (
              <div className="mb-2">
                <p className="mb-1 text-[11px] text-muted-foreground">
                  System Prefix
                </p>
                <pre className="overflow-auto rounded bg-muted p-3 text-xs whitespace-pre-wrap">
                  {detail.prompts.system_prefix}
                </pre>
              </div>
            )}
            {detail.prompts.system_suffix && (
              <div className="mb-2">
                <p className="mb-1 text-[11px] text-muted-foreground">
                  System Suffix
                </p>
                <pre className="overflow-auto rounded bg-muted p-3 text-xs whitespace-pre-wrap">
                  {detail.prompts.system_suffix}
                </pre>
              </div>
            )}
            {detail.prompts.instructions && (
              <div>
                <p className="mb-1 text-[11px] text-muted-foreground">
                  Instructions
                </p>
                <pre className="overflow-auto rounded bg-muted p-3 text-xs whitespace-pre-wrap">
                  {detail.prompts.instructions}
                </pre>
              </div>
            )}
          </div>
        )}

        {/* Hints */}
        {detail.hints && Object.keys(detail.hints).length > 0 && (
          <div>
            <p className="mb-2 text-xs font-medium text-muted-foreground">
              Hints
            </p>
            <div className="overflow-auto rounded border">
              <table className="w-full text-xs">
                <thead>
                  <tr className="border-b bg-muted/50">
                    <th className="px-2 py-1 text-left font-medium">Tool</th>
                    <th className="px-2 py-1 text-left font-medium">Hint</th>
                  </tr>
                </thead>
                <tbody>
                  {Object.entries(detail.hints).map(([tool, hint]) => (
                    <tr key={tool} className="border-b">
                      <td className="px-2 py-1 font-mono">{tool}</td>
                      <td className="px-2 py-1">{hint}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
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

function hasPromptContent(
  prompts: PersonaDetail["prompts"],
): prompts is NonNullable<PersonaDetail["prompts"]> {
  if (!prompts) return false;
  return !!(
    prompts.system_prefix ||
    prompts.system_suffix ||
    prompts.instructions
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
      {/* Intro */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">What are Personas?</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Personas are the authorization and customization layer of the MCP Data
          Platform. Each persona defines <strong>who</strong> can use{" "}
          <strong>which tools</strong>, and <strong>how</strong> the AI assistant
          behaves when interacting with that user. When a user connects, their
          identity (via OIDC roles or API key) is mapped to a persona, which
          controls tool access, system prompts, and tool-specific hints.
        </p>
      </section>

      {/* Tool Filtering */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Tool Filtering</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Each persona has <strong>allow</strong> and <strong>deny</strong> tool
          patterns. The platform evaluates them for every tool call:
        </p>
        <ol className="mb-3 list-inside list-decimal space-y-1 text-sm text-muted-foreground">
          <li>
            Check <strong>deny</strong> rules first &mdash; if any pattern
            matches, access is <strong>denied</strong> (deny always wins).
          </li>
          <li>
            Check <strong>allow</strong> rules &mdash; if any pattern matches,
            access is <strong>granted</strong>.
          </li>
          <li>
            If no allow rule matches, access is <strong>denied</strong>{" "}
            (fail-closed).
          </li>
        </ol>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Patterns support glob-style wildcards. For example:
        </p>
        <div className="overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Pattern</th>
                <th className="px-3 py-2 text-left font-medium">Matches</th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">*</td>
                <td className="px-3 py-2 text-xs">All tools</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">trino_*</td>
                <td className="px-3 py-2 text-xs">
                  trino_query, trino_describe_table, trino_list_catalogs, etc.
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">s3_delete_*</td>
                <td className="px-3 py-2 text-xs">
                  s3_delete_object (useful in deny rules)
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">datahub_search</td>
                <td className="px-3 py-2 text-xs">
                  Exact match only &mdash; datahub_search
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      {/* Default Persona & Fail-Closed */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">
          Default Persona & Fail-Closed Behavior
        </h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          If no persona matches the user&apos;s roles, the platform assigns a{" "}
          <strong>default persona</strong>. You can configure which persona is
          the default via the{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            default_persona
          </code>{" "}
          setting. If no default is configured, a built-in &quot;no access&quot;
          persona is used that <strong>denies all tools</strong>. This ensures
          fail-closed behavior &mdash; users must be explicitly granted access.
        </p>
      </section>

      {/* Role Mapping */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Role Mapping</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          The platform supports multiple strategies for mapping users to
          personas:
        </p>
        <div className="space-y-3">
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">OIDC Role Mapping</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Roles are extracted from OIDC token claims at a configurable path
              (e.g.{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                realm_access.roles
              </code>
              ). Roles with the configured prefix (e.g.{" "}
              <code className="rounded bg-muted px-1 py-0.5">dp_</code>) are
              matched against the{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                oidc_to_persona
              </code>{" "}
              mapping table. For example,{" "}
              <code className="rounded bg-muted px-1 py-0.5">dp_admin</code>{" "}
              maps to the{" "}
              <code className="rounded bg-muted px-1 py-0.5">admin</code>{" "}
              persona.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">
              Static User/Group Mapping
            </h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Map specific users (by email or ID) directly to personas via the{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                user_personas
              </code>{" "}
              config. Useful for assigning personas to individual users
              regardless of their OIDC roles.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">API Key Roles</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              API keys include a roles list. These roles are matched against
              persona definitions the same way OIDC roles are. A key with{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                roles: [&quot;admin&quot;]
              </code>{" "}
              will map to the admin persona.
            </p>
          </div>
        </div>
      </section>

      {/* Priority */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Priority</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          When a user&apos;s roles match multiple personas, the persona with the{" "}
          <strong>highest priority</strong> value wins. For example, if a user
          has both{" "}
          <code className="rounded bg-muted px-1 py-0.5">data_engineer</code>{" "}
          and <code className="rounded bg-muted px-1 py-0.5">admin</code>{" "}
          roles, and the admin persona has priority 100 while data-engineer has
          priority 0, the user gets the admin persona. The default priority is 0.
        </p>
      </section>

      {/* Prompts */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Prompt Customization</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Each persona can customize the AI assistant&apos;s system prompt with
          three fields, combined in order:
        </p>
        <div className="overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Field</th>
                <th className="px-3 py-2 text-left font-medium">Purpose</th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">system_prefix</td>
                <td className="px-3 py-2 text-xs">
                  Prepended to the system prompt. Sets the assistant&apos;s
                  identity and focus area.
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">instructions</td>
                <td className="px-3 py-2 text-xs">
                  Additional instructions inserted between prefix and suffix.
                  Specific guidance for this persona&apos;s tasks.
                </td>
              </tr>
              <tr>
                <td className="px-3 py-2 font-mono text-xs">system_suffix</td>
                <td className="px-3 py-2 text-xs">
                  Appended to the system prompt. Often used for constraints or
                  reminders.
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      {/* Hints */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Tool Hints</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Hints are tool-specific guidance injected into tool call responses.
          They help the AI assistant make better decisions for this persona. For
          example, a data-engineer persona might have a hint on{" "}
          <code className="rounded bg-muted px-1 py-0.5">trino_query</code>{" "}
          saying &quot;Use iceberg catalog for production tables&quot;. Hints are
          key-value pairs where the key is the tool name and the value is the
          guidance text.
        </p>
      </section>

      {/* Config Modes */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Configuration Modes</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          The platform supports two configuration modes:
        </p>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">
              File Mode{" "}
              <code className="rounded bg-muted px-1 py-0.5 text-xs">
                config_mode: file
              </code>
            </h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Personas are defined in{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                platform.yaml
              </code>{" "}
              and loaded at startup. Changes require restarting the server. The
              admin API is <strong>read-only</strong> &mdash; create, update, and
              delete operations return 405 Method Not Allowed.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">
              Database Mode{" "}
              <code className="rounded bg-muted px-1 py-0.5 text-xs">
                config_mode: database
              </code>
            </h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Personas are stored in the database and managed via the admin API.
              Full CRUD operations are available. Changes take effect
              immediately. The initial configuration is seeded from{" "}
              <code className="rounded bg-muted px-1 py-0.5">
                platform.yaml
              </code>{" "}
              on first run.
            </p>
          </div>
        </div>
      </section>

      {/* YAML Example */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">YAML Configuration Example</h2>
        <pre className="overflow-auto rounded-lg border bg-muted p-4 text-xs leading-relaxed">
{`personas:
  data-engineer:
    display_name: "Data Engineer"
    roles:
      - data_engineer
    tools:
      allow:
        - "trino_*"
        - "datahub_*"
        - "s3_*"
      deny:
        - "s3_delete_*"
    prompts:
      system_prefix: |
        You are helping a data engineer build
        and maintain data pipelines.
    hints:
      trino_query: "Use iceberg catalog for production tables"
      datahub_search: "Search the catalog before writing new queries"

  store-manager:
    display_name: "Store Manager"
    roles:
      - store_manager
    tools:
      allow:
        - "trino_query"
        - "trino_describe_table"
        - "datahub_search"
        - "s3_get_object"
      deny: []
    prompts:
      system_prefix: |
        You are helping a store manager access
        store-level data.

  default_persona: store-manager

  role_mapping:
    oidc_to_persona:
      "dp_data_engineer": "data-engineer"
      "dp_store_manager": "store-manager"
    user_personas:
      "marcus@acme.com": "data-engineer"
      "kevin@acme.com": "store-manager"`}
        </pre>
      </section>

      {/* Admin Persona */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">The Admin Persona</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          The admin persona has special protections. It is designated in the
          platform config via{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            admin.persona
          </code>{" "}
          and <strong>cannot be deleted</strong> through the API (returns 409
          Conflict). This ensures there is always at least one persona with full
          access. The admin persona typically uses{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            allow: [&quot;*&quot;]
          </code>{" "}
          to grant access to all tools.
        </p>
      </section>

      {/* API Reference */}
      <section>
        <h2 className="mb-2 text-lg font-semibold">Admin API Endpoints</h2>
        <div className="overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Method</th>
                <th className="px-3 py-2 text-left font-medium">Endpoint</th>
                <th className="px-3 py-2 text-left font-medium">
                  Description
                </th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b">
                <td className="px-3 py-2">
                  <StatusBadge variant="success">GET</StatusBadge>
                </td>
                <td className="px-3 py-2 font-mono text-xs">/personas</td>
                <td className="px-3 py-2 text-xs">
                  List all personas with tool counts
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2">
                  <StatusBadge variant="success">GET</StatusBadge>
                </td>
                <td className="px-3 py-2 font-mono text-xs">
                  /personas/:name
                </td>
                <td className="px-3 py-2 text-xs">
                  Get persona detail with resolved tool list
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2">
                  <StatusBadge variant="warning">POST</StatusBadge>
                </td>
                <td className="px-3 py-2 font-mono text-xs">/personas</td>
                <td className="px-3 py-2 text-xs">
                  Create persona (database mode only)
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2">
                  <StatusBadge variant="warning">PUT</StatusBadge>
                </td>
                <td className="px-3 py-2 font-mono text-xs">
                  /personas/:name
                </td>
                <td className="px-3 py-2 text-xs">
                  Update persona (database mode only)
                </td>
              </tr>
              <tr>
                <td className="px-3 py-2">
                  <StatusBadge variant="error">DELETE</StatusBadge>
                </td>
                <td className="px-3 py-2 font-mono text-xs">
                  /personas/:name
                </td>
                <td className="px-3 py-2 text-xs">
                  Delete persona (database mode only, admin protected)
                </td>
              </tr>
            </tbody>
          </table>
        </div>
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
