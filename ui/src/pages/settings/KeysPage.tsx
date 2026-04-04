import { useState, useCallback } from "react";
import {
  useAPIKeys,
  useCreateAPIKey,
  useDeleteAPIKey,
  useSystemInfo,
  usePersonas,
} from "@/api/admin/hooks";
import type { APIKeyCreateResponse } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import {
  Plus,
  Trash2,
  X,
  KeyRound,
  AlertTriangle,
  Copy,
  Check,
  ChevronUp,
} from "lucide-react";

// ---------------------------------------------------------------------------
// Expiration presets
// ---------------------------------------------------------------------------

const EXPIRATION_OPTIONS = [
  { label: "Never", value: "" },
  { label: "24 hours", value: "24h" },
  { label: "7 days", value: "168h" },
  { label: "30 days", value: "720h" },
  { label: "90 days", value: "2160h" },
  { label: "1 year", value: "8760h" },
] as const;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatExpiration(expiresAt?: string): string {
  if (!expiresAt) return "Never";
  return new Date(expiresAt).toLocaleDateString();
}

// ---------------------------------------------------------------------------
// Draft state for key creation
// ---------------------------------------------------------------------------

interface KeyDraft {
  name: string;
  email: string;
  description: string;
  roles: string[];
  expirationPreset: string;
}

function emptyDraft(): KeyDraft {
  return {
    name: "",
    email: "",
    description: "",
    roles: [],
    expirationPreset: "",
  };
}

// ---------------------------------------------------------------------------
// KeysPage — table layout
// ---------------------------------------------------------------------------

export function KeysPage() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
  const { data: keyList, isLoading } = useAPIKeys();
  const keys = keyList?.keys ?? [];

  const [showForm, setShowForm] = useState(false);
  const [createdKey, setCreatedKey] = useState<APIKeyCreateResponse | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);
  const deleteMutation = useDeleteAPIKey();

  const handleCreated = useCallback((resp: APIKeyCreateResponse) => {
    setCreatedKey(resp);
    setShowForm(false);
  }, []);

  const handleDelete = useCallback(
    (name: string) => {
      deleteMutation.mutate(name, {
        onSuccess: () => setDeleteConfirm(null),
      });
    },
    [deleteMutation],
  );

  return (
    <div className="flex h-[calc(100vh-8rem)] flex-col overflow-hidden rounded-lg border bg-card">
      {/* Header */}
      <div className="flex items-center justify-between border-b px-5 py-3">
        <div>
          <h3 className="text-sm font-semibold leading-none">API Keys</h3>
          <p className="mt-1 text-xs text-muted-foreground">
            Manage API keys for programmatic access
          </p>
        </div>
        {!isReadOnly && (
          <button
            type="button"
            onClick={() => {
              setShowForm((prev) => !prev);
              setCreatedKey(null);
            }}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
              showForm
                ? "border text-muted-foreground hover:bg-muted"
                : "bg-primary text-primary-foreground hover:bg-primary/90",
            )}
          >
            {showForm ? (
              <>
                <ChevronUp className="h-3.5 w-3.5" />
                Cancel
              </>
            ) : (
              <>
                <Plus className="h-3.5 w-3.5" />
                Add Key
              </>
            )}
          </button>
        )}
      </div>

      {/* Created key banner */}
      {createdKey && (
        <CreatedKeyBanner
          response={createdKey}
          onDismiss={() => setCreatedKey(null)}
        />
      )}

      {/* Add key form (slide-down) */}
      {showForm && (
        <AddKeyForm
          onCreated={handleCreated}
          onCancel={() => setShowForm(false)}
        />
      )}

      {/* Table */}
      <div className="flex-1 overflow-auto">
        {isLoading ? (
          <div className="flex items-center justify-center py-16 text-sm text-muted-foreground">
            Loading...
          </div>
        ) : keys.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
            <KeyRound className="mb-3 h-8 w-8 opacity-30" />
            <p className="text-sm">No API keys configured</p>
            {!isReadOnly && (
              <p className="mt-1 text-xs opacity-60">
                Click &ldquo;Add Key&rdquo; to create one
              </p>
            )}
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/30 text-left text-xs font-medium text-muted-foreground">
                <th className="px-5 py-2">Name</th>
                <th className="px-5 py-2">Email</th>
                <th className="px-5 py-2">Description</th>
                <th className="px-5 py-2">Roles</th>
                <th className="px-5 py-2">Expiration</th>
                {!isReadOnly && <th className="px-5 py-2 w-20">Actions</th>}
              </tr>
            </thead>
            <tbody className="divide-y">
              {keys.map((k) => (
                <tr
                  key={k.name}
                  className={cn(
                    "transition-colors hover:bg-muted/20",
                    k.expired && "opacity-50",
                  )}
                >
                  <td className="px-5 py-3 font-medium">
                    <div className="flex items-center gap-2">
                      <span className={cn(k.expired && "line-through")}>{k.name}</span>
                      {k.expired && (
                        <span className="shrink-0 rounded-full bg-red-100 px-1.5 py-0.5 text-[9px] font-semibold text-red-700 dark:bg-red-900/30 dark:text-red-400">
                          Expired
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-5 py-3 text-muted-foreground">
                    {k.email || <span className="italic opacity-50">--</span>}
                  </td>
                  <td className="px-5 py-3 text-muted-foreground max-w-xs truncate">
                    {k.description || <span className="italic opacity-50">--</span>}
                  </td>
                  <td className="px-5 py-3">
                    <div className="flex flex-wrap gap-1">
                      {k.roles.map((r) => (
                        <span
                          key={r}
                          className="rounded-full border bg-muted/50 px-2 py-0.5 text-[10px] font-medium"
                        >
                          {r}
                        </span>
                      ))}
                      {k.roles.length === 0 && (
                        <span className="text-xs italic text-muted-foreground opacity-50">None</span>
                      )}
                    </div>
                  </td>
                  <td className="px-5 py-3 text-muted-foreground">
                    {formatExpiration(k.expires_at)}
                  </td>
                  {!isReadOnly && (
                    <td className="px-5 py-3">
                      {deleteConfirm === k.name ? (
                        <div className="flex items-center gap-1.5">
                          <button
                            type="button"
                            onClick={() => handleDelete(k.name)}
                            disabled={deleteMutation.isPending}
                            className="inline-flex items-center gap-1 rounded bg-red-600 px-2 py-1 text-[10px] font-medium text-white hover:bg-red-700 disabled:opacity-50"
                          >
                            {deleteMutation.isPending ? "..." : "Confirm"}
                          </button>
                          <button
                            type="button"
                            onClick={() => setDeleteConfirm(null)}
                            className="inline-flex items-center rounded border px-1.5 py-1 text-[10px] text-muted-foreground hover:bg-muted"
                          >
                            <X className="h-3 w-3" />
                          </button>
                        </div>
                      ) : (
                        <button
                          type="button"
                          onClick={() => setDeleteConfirm(k.name)}
                          className="inline-flex items-center gap-1 rounded border border-transparent px-2 py-1 text-[10px] text-muted-foreground hover:border-red-200 hover:text-red-600 dark:hover:border-red-800 dark:hover:text-red-400"
                        >
                          <Trash2 className="h-3 w-3" />
                          Delete
                        </button>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Created key amber banner
// ---------------------------------------------------------------------------

function CreatedKeyBanner({
  response,
  onDismiss,
}: {
  response: APIKeyCreateResponse;
  onDismiss: () => void;
}) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    void navigator.clipboard.writeText(response.key).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }, [response.key]);

  return (
    <div className="border-b border-amber-300 bg-amber-50 px-5 py-4 dark:border-amber-700 dark:bg-amber-950/30">
      <div className="flex items-start gap-3">
        <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-amber-600 dark:text-amber-400" />
        <div className="flex-1">
          <p className="text-sm font-semibold text-amber-800 dark:text-amber-300">
            Key created: {response.name} — copy it now, it will not be shown again
          </p>
          <div className="mt-2 flex items-center gap-2">
            <code className="flex-1 rounded-md border border-amber-300 bg-white px-3 py-2 font-mono text-sm break-all dark:border-amber-700 dark:bg-amber-950/50 dark:text-amber-200">
              {response.key}
            </code>
            <button
              type="button"
              onClick={handleCopy}
              className={cn(
                "inline-flex shrink-0 items-center gap-1.5 rounded-md px-3 py-2 text-xs font-medium transition-colors",
                copied
                  ? "bg-green-600 text-white"
                  : "bg-amber-600 text-white hover:bg-amber-700 dark:bg-amber-700 dark:hover:bg-amber-600",
              )}
            >
              {copied ? (
                <>
                  <Check className="h-3.5 w-3.5" />
                  Copied
                </>
              ) : (
                <>
                  <Copy className="h-3.5 w-3.5" />
                  Copy
                </>
              )}
            </button>
            <button
              type="button"
              onClick={onDismiss}
              className="inline-flex items-center rounded-md border border-amber-300 px-2 py-2 text-xs text-amber-700 hover:bg-amber-100 dark:border-amber-700 dark:text-amber-400 dark:hover:bg-amber-900/30"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Add key form (inline above the table)
// ---------------------------------------------------------------------------

function AddKeyForm({
  onCreated,
}: {
  onCreated: (resp: APIKeyCreateResponse) => void;
  onCancel: () => void;
}) {
  const createMutation = useCreateAPIKey();
  const [draft, setDraft] = useState<KeyDraft>(emptyDraft());
  const [roleInput, setRoleInput] = useState("");
  const [error, setError] = useState<string | null>(null);

  const updateDraft = useCallback((partial: Partial<KeyDraft>) => {
    setDraft((prev) => ({ ...prev, ...partial }));
    setError(null);
  }, []);

  const addRole = useCallback(() => {
    const trimmed = roleInput.trim();
    if (trimmed && !draft.roles.includes(trimmed)) {
      updateDraft({ roles: [...draft.roles, trimmed] });
    }
    setRoleInput("");
  }, [roleInput, draft.roles, updateDraft]);

  const removeRole = useCallback(
    (role: string) => {
      updateDraft({ roles: draft.roles.filter((r) => r !== role) });
    },
    [draft.roles, updateDraft],
  );

  const handleSubmit = useCallback(() => {
    if (!draft.name.trim()) {
      setError("Name is required");
      return;
    }

    const expiresIn = draft.expirationPreset || undefined;

    createMutation.mutate(
      {
        name: draft.name.trim(),
        email: draft.email.trim() || undefined,
        description: draft.description.trim() || undefined,
        roles: draft.roles,
        expires_in: expiresIn,
      },
      {
        onSuccess: (resp) => onCreated(resp),
        onError: (err) => {
          setError(err instanceof Error ? err.message : "Failed to create key");
        },
      },
    );
  }, [draft, createMutation, onCreated]);

  return (
    <div className="border-b bg-muted/10 px-5 py-4 space-y-4">
      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-4 py-2.5 text-xs text-red-700 dark:border-red-800 dark:bg-red-950/30 dark:text-red-400">
          {error}
        </div>
      )}

      {/* Row 1: Name, Email, Description */}
      <div className="grid grid-cols-3 gap-3">
        <div>
          <label className="mb-1 block text-xs font-medium">
            Name <span className="text-red-500">*</span>
          </label>
          <input
            type="text"
            value={draft.name}
            onChange={(e) => updateDraft({ name: e.target.value })}
            placeholder="e.g. ci-pipeline"
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none focus:ring-2 focus:ring-primary/30"
          />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium">Email</label>
          <input
            type="email"
            value={draft.email}
            onChange={(e) => updateDraft({ email: e.target.value })}
            placeholder="e.g. team@example.com"
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none focus:ring-2 focus:ring-primary/30"
          />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium">Description</label>
          <input
            type="text"
            value={draft.description}
            onChange={(e) => updateDraft({ description: e.target.value })}
            placeholder="What is this key used for?"
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none focus:ring-2 focus:ring-primary/30"
          />
        </div>
      </div>

      {/* Row 2: Roles + Expiration + Create button */}
      <div className="space-y-2">
        <div className="flex items-end gap-3">
          <div className="flex-1">
            <label className="mb-1 block text-xs font-medium">Roles</label>
            <div className="flex items-center gap-2">
              {draft.roles.length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {draft.roles.map((r) => (
                    <span
                      key={r}
                      className="inline-flex items-center gap-1 rounded-full border bg-muted/50 px-2 py-0.5 text-[10px] font-medium"
                    >
                      {r}
                      <button
                        type="button"
                        onClick={() => removeRole(r)}
                        className="text-muted-foreground hover:text-foreground"
                      >
                        <X className="h-2.5 w-2.5" />
                      </button>
                    </span>
                  ))}
                </div>
              )}
              <input
                type="text"
                value={roleInput}
                onChange={(e) => setRoleInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    addRole();
                  }
                }}
                placeholder="Type role + Enter"
                className="flex-1 min-w-[140px] rounded-md border bg-background px-3 py-1.5 text-sm outline-none focus:ring-2 focus:ring-primary/30"
              />
            </div>
          </div>

        <div className="w-36">
          <label className="mb-1 block text-xs font-medium">Expiration</label>
          <select
            value={draft.expirationPreset}
            onChange={(e) => updateDraft({ expirationPreset: e.target.value })}
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none focus:ring-2 focus:ring-primary/30"
          >
            {EXPIRATION_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        </div>

        <button
          type="button"
          onClick={handleSubmit}
          disabled={createMutation.isPending || !draft.name.trim()}
          className="inline-flex items-center gap-1.5 rounded-md bg-primary px-4 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50 transition-colors"
        >
          <KeyRound className="h-3.5 w-3.5" />
          {createMutation.isPending ? "Creating..." : "Create"}
        </button>
      </div>
      <RoleBrowser
        onSelect={(role) => {
          if (!draft.roles.includes(role)) {
            updateDraft({ roles: [...draft.roles, role] });
          }
        }}
      />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Role Browser — shows available roles from personas, click to add
// ---------------------------------------------------------------------------

function RoleBrowser({ onSelect }: { onSelect: (role: string) => void }) {
  const [open, setOpen] = useState(false);
  const { data: personaData } = usePersonas();
  const personas = personaData?.personas ?? [];

  // Build role → persona mapping.
  const roleMap: { role: string; persona: string; displayName: string }[] = [];
  for (const p of personas) {
    for (const r of p.roles) {
      roleMap.push({ role: r, persona: p.name, displayName: p.display_name });
    }
  }
  roleMap.sort((a, b) => a.role.localeCompare(b.role));

  if (roleMap.length === 0) return null;

  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="text-[10px] text-primary hover:underline"
      >
        {open ? "Hide available roles" : "Browse available roles"}
      </button>
      {open && (
        <div className="mt-2 rounded-md border max-h-40 overflow-auto">
          <table className="w-full text-xs">
            <thead className="sticky top-0 bg-card">
              <tr className="border-b">
                <th className="px-3 py-1.5 text-left font-medium text-muted-foreground">Role</th>
                <th className="px-3 py-1.5 text-left font-medium text-muted-foreground">Persona</th>
              </tr>
            </thead>
            <tbody>
              {roleMap.map((r) => (
                <tr
                  key={`${r.role}-${r.persona}`}
                  className="border-b last:border-0 cursor-pointer hover:bg-muted/50"
                  onClick={() => onSelect(r.role)}
                >
                  <td className="px-3 py-1.5 font-mono">{r.role}</td>
                  <td className="px-3 py-1.5 text-muted-foreground">{r.displayName}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
