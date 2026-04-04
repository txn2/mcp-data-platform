import { useState, useCallback } from "react";
import {
  useAPIKeys,
  useCreateAPIKey,
  useDeleteAPIKey,
  useSystemInfo,
} from "@/api/admin/hooks";
import type { APIKeySummary, APIKeyCreateResponse } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import {
  Plus,
  Trash2,
  X,
  KeyRound,
  AlertTriangle,
  Copy,
  Check,
  Shield,
  Clock,
  Mail,
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
  { label: "Custom", value: "custom" },
] as const;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatExpiration(expiresAt?: string, expired?: boolean): string {
  if (!expiresAt) return "Never";
  const d = new Date(expiresAt);
  if (expired) return `Expired ${d.toLocaleDateString()}`;
  const now = new Date();
  const diffMs = d.getTime() - now.getTime();
  const diffDays = Math.ceil(diffMs / (1000 * 60 * 60 * 24));
  if (diffDays <= 0) return "Expiring today";
  if (diffDays === 1) return "Expires tomorrow";
  if (diffDays < 30) return `Expires in ${diffDays} days`;
  return `Expires ${d.toLocaleDateString()}`;
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
  customDuration: string;
}

function emptyDraft(): KeyDraft {
  return {
    name: "",
    email: "",
    description: "",
    roles: [],
    expirationPreset: "",
    customDuration: "",
  };
}

// ---------------------------------------------------------------------------
// KeysPanel — master-detail layout
// ---------------------------------------------------------------------------

export function KeysPanel() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
  const { data: keyList, isLoading } = useAPIKeys();
  const keys = keyList?.keys ?? [];

  const [selected, setSelected] = useState<string | null>(null);
  const [mode, setMode] = useState<"view" | "create">("view");
  const [createdKey, setCreatedKey] = useState<APIKeyCreateResponse | null>(null);

  const selectedKey = keys.find((k) => k.name === selected) ?? null;

  const handleSelect = useCallback((name: string) => {
    setSelected(name);
    setMode("view");
    setCreatedKey(null);
  }, []);

  const handleCreate = useCallback(() => {
    setSelected(null);
    setMode("create");
    setCreatedKey(null);
  }, []);

  const handleCreated = useCallback((resp: APIKeyCreateResponse) => {
    setCreatedKey(resp);
    setSelected(resp.name);
    setMode("view");
  }, []);

  const handleCancelCreate = useCallback(() => {
    setMode("view");
    if (keys.length > 0 && keys[0]) {
      setSelected(keys[0].name);
    }
  }, [keys]);

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Loading keys...
      </div>
    );
  }

  return (
    <div className="flex h-full overflow-hidden">
      {/* Left: Key list */}
      <div className="w-56 shrink-0 border-r bg-muted/10 flex flex-col overflow-hidden">
        <div className="flex-1 overflow-auto">
          {keys.map((k) => (
            <button
              key={k.name}
              type="button"
              onClick={() => handleSelect(k.name)}
              className={cn(
                "flex w-full flex-col px-4 py-3 text-left border-b transition-colors",
                selected === k.name && mode !== "create"
                  ? "bg-primary/5 border-l-2 border-l-primary"
                  : "border-l-2 border-l-transparent hover:bg-muted/50",
              )}
            >
              <div className="flex items-center gap-2">
                <span className={cn(
                  "text-sm font-medium truncate",
                  k.expired && "line-through text-muted-foreground",
                )}>
                  {k.name}
                </span>
                {k.expired && (
                  <span className="shrink-0 rounded-full bg-red-100 px-1.5 py-0.5 text-[9px] font-semibold text-red-700 dark:bg-red-900/30 dark:text-red-400">
                    expired
                  </span>
                )}
              </div>
              {k.email && (
                <span className="text-[10px] text-muted-foreground truncate">{k.email}</span>
              )}
              <div className="mt-1 flex items-center gap-3 text-[10px] text-muted-foreground">
                <span>{k.roles.length} role{k.roles.length !== 1 ? "s" : ""}</span>
                <span>{formatExpiration(k.expires_at, k.expired)}</span>
              </div>
            </button>
          ))}
          {keys.length === 0 && (
            <div className="px-4 py-8 text-center text-xs text-muted-foreground">
              No API keys configured
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
              New Key
            </button>
          </div>
        )}
      </div>

      {/* Right: Detail / Create panel */}
      <div className="flex-1 overflow-auto">
        {createdKey ? (
          <KeyCreatedView
            response={createdKey}
            onDismiss={() => setCreatedKey(null)}
          />
        ) : mode === "create" ? (
          <KeyCreateForm
            onCreated={handleCreated}
            onCancel={handleCancelCreate}
          />
        ) : selectedKey ? (
          <KeyViewer
            keyInfo={selectedKey}
            isReadOnly={isReadOnly}
            onDeleted={() => {
              setSelected(null);
              setMode("view");
            }}
          />
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            <div className="text-center">
              <KeyRound className="mx-auto mb-2 h-8 w-8 opacity-30" />
              <p>Select a key or create a new one</p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Key Viewer (read-only detail)
// ---------------------------------------------------------------------------

function KeyViewer({
  keyInfo,
  isReadOnly,
  onDeleted,
}: {
  keyInfo: APIKeySummary;
  isReadOnly: boolean;
  onDeleted: () => void;
}) {
  const deleteMutation = useDeleteAPIKey();
  const [confirmDelete, setConfirmDelete] = useState(false);

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h2 className="text-lg font-semibold">{keyInfo.name}</h2>
            {keyInfo.expired && (
              <span className="rounded-full bg-red-100 px-2 py-0.5 text-[10px] font-semibold text-red-700 dark:bg-red-900/30 dark:text-red-400">
                expired
              </span>
            )}
          </div>
          {keyInfo.email && (
            <div className="mt-1 flex items-center gap-1.5 text-sm text-muted-foreground">
              <Mail className="h-3.5 w-3.5" />
              {keyInfo.email}
            </div>
          )}
          {keyInfo.description && (
            <p className="mt-2 text-sm text-muted-foreground">{keyInfo.description}</p>
          )}
        </div>
        {!isReadOnly && (
          <div className="flex gap-2">
            {confirmDelete ? (
              <div className="flex items-center gap-2">
                <span className="text-xs text-red-600 dark:text-red-400">Delete this key?</span>
                <button
                  type="button"
                  onClick={() => {
                    deleteMutation.mutate(keyInfo.name, {
                      onSuccess: () => onDeleted(),
                    });
                  }}
                  disabled={deleteMutation.isPending}
                  className="inline-flex items-center gap-1 rounded-md bg-red-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-red-700 disabled:opacity-50"
                >
                  <Trash2 className="h-3 w-3" />
                  {deleteMutation.isPending ? "Deleting..." : "Confirm"}
                </button>
                <button
                  type="button"
                  onClick={() => setConfirmDelete(false)}
                  className="inline-flex items-center gap-1 rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted"
                >
                  <X className="h-3 w-3" />
                  Cancel
                </button>
              </div>
            ) : (
              <button
                type="button"
                onClick={() => setConfirmDelete(true)}
                className="inline-flex items-center gap-1 rounded-md border border-red-200 px-3 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950/30"
              >
                <Trash2 className="h-3 w-3" />
                Delete
              </button>
            )}
          </div>
        )}
      </div>

      {/* Roles */}
      <div>
        <h3 className="mb-2 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          <Shield className="h-3.5 w-3.5" />
          Roles
        </h3>
        <div className="flex flex-wrap gap-1.5">
          {keyInfo.roles.map((r) => (
            <span
              key={r}
              className="rounded-full border bg-muted/50 px-2.5 py-0.5 text-xs font-medium"
            >
              {r}
            </span>
          ))}
          {keyInfo.roles.length === 0 && (
            <span className="text-xs text-muted-foreground italic">No roles assigned</span>
          )}
        </div>
      </div>

      {/* Expiration */}
      <div>
        <h3 className="mb-2 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          <Clock className="h-3.5 w-3.5" />
          Expiration
        </h3>
        <p className={cn(
          "text-sm",
          keyInfo.expired ? "text-red-600 dark:text-red-400 font-medium" : "text-foreground",
        )}>
          {keyInfo.expires_at
            ? `${new Date(keyInfo.expires_at).toLocaleString()} (${formatExpiration(keyInfo.expires_at, keyInfo.expired).toLowerCase()})`
            : "Never expires"}
        </p>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Key Created View — shows the secret once
// ---------------------------------------------------------------------------

function KeyCreatedView({
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
    <div className="p-6 space-y-6">
      <div>
        <h2 className="text-lg font-semibold">Key Created: {response.name}</h2>
        {response.email && (
          <p className="mt-1 text-sm text-muted-foreground">{response.email}</p>
        )}
      </div>

      {/* Warning banner */}
      <div className="rounded-lg border border-amber-300 bg-amber-50 p-4 dark:border-amber-700 dark:bg-amber-950/30">
        <div className="flex items-start gap-3">
          <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-amber-600 dark:text-amber-400" />
          <div className="flex-1">
            <p className="text-sm font-semibold text-amber-800 dark:text-amber-300">
              Copy this key now — it will not be shown again
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
              {response.warning || "Store this API key in a secure location. You will not be able to retrieve it after leaving this page."}
            </p>
          </div>
        </div>

        <div className="mt-4 flex items-center gap-2">
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
        </div>
      </div>

      {/* Key details */}
      <div className="space-y-3 text-sm">
        <div>
          <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Roles</span>
          <div className="mt-1 flex flex-wrap gap-1.5">
            {response.roles.map((r) => (
              <span key={r} className="rounded-full border bg-muted/50 px-2.5 py-0.5 text-xs font-medium">
                {r}
              </span>
            ))}
          </div>
        </div>
        {response.expires_at && (
          <div>
            <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Expires</span>
            <p className="mt-1 text-sm">{new Date(response.expires_at).toLocaleString()}</p>
          </div>
        )}
      </div>

      <button
        type="button"
        onClick={onDismiss}
        className="inline-flex items-center gap-1.5 rounded-md border px-4 py-2 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground transition-colors"
      >
        Done
      </button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Key Create Form
// ---------------------------------------------------------------------------

function KeyCreateForm({
  onCreated,
  onCancel,
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

  const removeRole = useCallback((role: string) => {
    updateDraft({ roles: draft.roles.filter((r) => r !== role) });
  }, [draft.roles, updateDraft]);

  const handleSubmit = useCallback(() => {
    if (!draft.name.trim()) {
      setError("Name is required");
      return;
    }

    let expiresIn: string | undefined;
    if (draft.expirationPreset === "custom") {
      if (!draft.customDuration.trim()) {
        setError("Custom duration is required (e.g. 720h, 30d)");
        return;
      }
      expiresIn = draft.customDuration.trim();
    } else if (draft.expirationPreset) {
      expiresIn = draft.expirationPreset;
    }

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
    <div className="p-6 space-y-5">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Create API Key</h2>
        <button
          type="button"
          onClick={onCancel}
          className="inline-flex items-center gap-1 rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <X className="h-3 w-3" />
          Cancel
        </button>
      </div>

      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-4 py-2.5 text-xs text-red-700 dark:border-red-800 dark:bg-red-950/30 dark:text-red-400">
          {error}
        </div>
      )}

      {/* Name */}
      <div>
        <label className="mb-1.5 block text-xs font-medium">
          Name <span className="text-red-500">*</span>
        </label>
        <input
          type="text"
          value={draft.name}
          onChange={(e) => updateDraft({ name: e.target.value })}
          placeholder="e.g. ci-pipeline"
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/30"
        />
      </div>

      {/* Email */}
      <div>
        <label className="mb-1.5 block text-xs font-medium">Email</label>
        <input
          type="email"
          value={draft.email}
          onChange={(e) => updateDraft({ email: e.target.value })}
          placeholder="e.g. team@example.com"
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/30"
        />
      </div>

      {/* Description */}
      <div>
        <label className="mb-1.5 block text-xs font-medium">Description</label>
        <textarea
          value={draft.description}
          onChange={(e) => updateDraft({ description: e.target.value })}
          placeholder="What is this key used for?"
          rows={2}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/30 resize-none"
        />
      </div>

      {/* Roles */}
      <div>
        <label className="mb-1.5 block text-xs font-medium">Roles</label>
        <div className="flex flex-wrap gap-1.5 mb-2">
          {draft.roles.map((r) => (
            <span
              key={r}
              className="inline-flex items-center gap-1 rounded-full border bg-muted/50 px-2.5 py-0.5 text-xs font-medium"
            >
              {r}
              <button
                type="button"
                onClick={() => removeRole(r)}
                className="text-muted-foreground hover:text-foreground"
              >
                <X className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
        <div className="flex gap-2">
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
            placeholder="Type a role and press Enter"
            className="flex-1 rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/30"
          />
          <button
            type="button"
            onClick={addRole}
            disabled={!roleInput.trim()}
            className="rounded-md border px-3 py-2 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-40"
          >
            Add
          </button>
        </div>
      </div>

      {/* Expiration */}
      <div>
        <label className="mb-1.5 block text-xs font-medium">Expiration</label>
        <select
          value={draft.expirationPreset}
          onChange={(e) => updateDraft({ expirationPreset: e.target.value, customDuration: "" })}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/30"
        >
          {EXPIRATION_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
        {draft.expirationPreset === "custom" && (
          <input
            type="text"
            value={draft.customDuration}
            onChange={(e) => updateDraft({ customDuration: e.target.value })}
            placeholder="e.g. 720h, 2160h"
            className="mt-2 w-full rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/30"
          />
        )}
      </div>

      {/* Submit */}
      <button
        type="button"
        onClick={handleSubmit}
        disabled={createMutation.isPending || !draft.name.trim()}
        className="inline-flex items-center gap-1.5 rounded-md bg-primary px-4 py-2 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50 transition-colors"
      >
        <KeyRound className="h-3.5 w-3.5" />
        {createMutation.isPending ? "Creating..." : "Create Key"}
      </button>
    </div>
  );
}
