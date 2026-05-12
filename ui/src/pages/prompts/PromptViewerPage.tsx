import { useState, useEffect, useMemo, useCallback } from "react";
import {
  ArrowLeft,
  Globe,
  Users,
  User,
  MessageSquare,
  Pencil,
  Save,
  X,
  Trash2,
  Eye,
  Code,
  Copy,
  Check,
  AlertTriangle,
  FileBox,
  Share2,
  Braces,
} from "lucide-react";
import { useMyPrompts, useUpdateMyPrompt, useDeleteMyPrompt, useCreateAsset } from "@/api/portal/hooks";
import { ShareDialog } from "@/components/ShareDialog";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import type { Prompt } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import { extractPromptArguments } from "./promptArguments";

interface Props {
  promptId: string;
  onNavigate: (path: string) => void;
  onBack: () => void;
}

type ScopeStyle = { label: string; icon: typeof Globe; color: string };

const scopeStyles: Record<string, ScopeStyle> = {
  global: { label: "Global", icon: Globe, color: "bg-blue-500/10 text-blue-400 border-blue-500/20" },
  persona: { label: "Persona", icon: Users, color: "bg-purple-500/10 text-purple-400 border-purple-500/20" },
  personal: { label: "Personal", icon: User, color: "bg-zinc-500/10 text-zinc-400 border-zinc-500/20" },
  system: { label: "System", icon: MessageSquare, color: "bg-amber-500/10 text-amber-400 border-amber-500/20" },
};

const defaultScopeStyle: ScopeStyle = scopeStyles["personal"]!;

function getScopeStyle(scope: string): ScopeStyle {
  const match = scopeStyles[scope];
  return match !== undefined ? match : defaultScopeStyle;
}

function ScopeBadge({ scope }: { scope: string }) {
  const cfg = getScopeStyle(scope);
  const Icon = cfg.icon;
  return (
    <span className={cn("inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap", cfg.color)}>
      <Icon className="h-3 w-3" />
      {cfg.label}
    </span>
  );
}

function ArgumentsPanel({ args }: { args: Prompt["arguments"] }) {
  if (!args || args.length === 0) return null;
  const required = args.filter((a) => a.required).length;
  const optional = args.length - required;
  return (
    <div className="rounded-lg border bg-card overflow-hidden">
      <div className="flex items-center justify-between border-b bg-muted/40 px-3 py-2">
        <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <Braces className="h-3.5 w-3.5" />
          <span>Arguments ({args.length})</span>
        </div>
        <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
          <span>{required} required</span>
          <span className="opacity-50">·</span>
          <span>{optional} optional</span>
        </div>
      </div>
      <ul className="divide-y">
        {args.map((a) => (
          <li key={a.name} className="px-3 py-2 grid grid-cols-1 md:grid-cols-[minmax(0,1fr)_2fr] gap-x-4 gap-y-1 items-baseline">
            <div className="flex items-center gap-2 flex-wrap">
              <code className="text-xs font-mono text-foreground bg-muted/60 rounded px-1.5 py-0.5 break-all">
                {`{{${a.name}}}`}
              </code>
              <span
                className={cn(
                  "inline-flex items-center rounded-full border px-1.5 py-0 text-[10px] font-medium uppercase tracking-wide",
                  a.required
                    ? "bg-rose-500/10 text-rose-400 border-rose-500/20"
                    : "bg-zinc-500/10 text-zinc-400 border-zinc-500/20",
                )}
              >
                {a.required ? "required" : "optional"}
              </span>
            </div>
            <div className="text-xs text-muted-foreground break-words">
              {a.description || <span className="italic opacity-60">No description</span>}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

type ViewMode = "preview" | "source";

interface EditForm {
  name: string;
  display_name: string;
  description: string;
  content: string;
  category: string;
  arguments: Prompt["arguments"];
}

export function PromptViewerPage({ promptId, onNavigate, onBack }: Props) {
  const { data, isLoading } = useMyPrompts();
  const updateMutation = useUpdateMyPrompt();
  const deleteMutation = useDeleteMyPrompt();
  const createAssetMutation = useCreateAsset();

  const prompt = useMemo<Prompt | undefined>(() => {
    if (!data) return undefined;
    return [...data.personal, ...data.available].find((p) => p.id === promptId);
  }, [data, promptId]);

  const isOwner = prompt?.scope === "personal";

  const [viewMode, setViewMode] = useState<ViewMode>("preview");
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState<EditForm>({ name: "", display_name: "", description: "", content: "", category: "", arguments: [] });
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [shareAssetId, setShareAssetId] = useState<string | null>(null);
  const [shareOpen, setShareOpen] = useState(false);
  const [saveAsAssetNotice, setSaveAsAssetNotice] = useState<{ assetId: string; name: string } | null>(null);

  // Reset edit form when prompt loads/changes.
  useEffect(() => {
    if (prompt && !editing) {
      setForm({
        name: prompt.name,
        display_name: prompt.display_name,
        description: prompt.description,
        content: prompt.content,
        category: prompt.category,
        arguments: prompt.arguments ?? [],
      });
    }
  }, [prompt, editing]);

  // Live-sync the arguments table with the content textarea: new {{name}}
  // placeholders typed in content appear as new rows; placeholders removed
  // from content drop out; descriptions and required flags the user has
  // edited in place are preserved across content edits.
  const handleContentChange = useCallback((next: string) => {
    setForm((prev) => ({
      ...prev,
      content: next,
      arguments: extractPromptArguments(next, prev.arguments),
    }));
  }, []);

  const updateArgField = useCallback(
    (name: string, patch: Partial<Prompt["arguments"][number]>) => {
      setForm((prev) => ({
        ...prev,
        arguments: prev.arguments.map((a) => (a.name === name ? { ...a, ...patch } : a)),
      }));
    },
    [],
  );

  const handleSave = useCallback(() => {
    if (!prompt) return;
    setError(null);
    updateMutation.mutate(
      { id: prompt.id, ...form },
      {
        onSuccess: () => {
          setEditing(false);
        },
        onError: (err) => {
          setError(err instanceof Error ? err.message : "Save failed");
        },
      },
    );
  }, [prompt, form, updateMutation]);

  const handleDelete = useCallback(() => {
    if (!prompt) return;
    setError(null);
    deleteMutation.mutate(prompt.id, {
      onSuccess: () => {
        setDeleteOpen(false);
        onBack();
      },
      onError: (err) => {
        setError(err instanceof Error ? err.message : "Delete failed");
      },
    });
  }, [prompt, deleteMutation, onBack]);

  const handleCopyContent = useCallback(async () => {
    if (!prompt) return;
    try {
      await navigator.clipboard.writeText(prompt.content);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // best-effort
    }
  }, [prompt]);

  const handleSaveAsAsset = useCallback(() => {
    if (!prompt) return;
    setError(null);
    const name = (prompt.display_name || prompt.name).trim() || "Prompt";
    const description = prompt.description || `Snapshot of prompt "${prompt.name}"`;
    createAssetMutation.mutate(
      {
        name,
        description,
        content_type: "text/markdown",
        content: prompt.content,
        tags: prompt.category ? ["prompt", prompt.category] : ["prompt"],
      },
      {
        onSuccess: (asset) => {
          setSaveAsAssetNotice({ assetId: asset.id, name: asset.name });
        },
        onError: (err) => {
          setError(err instanceof Error ? err.message : "Save as asset failed");
        },
      },
    );
  }, [prompt, createAssetMutation]);

  const handleShare = useCallback(() => {
    if (!prompt) return;
    setError(null);
    // Sharing a prompt means snapshotting it as a markdown asset, then sharing
    // that asset. This reuses the asset share infrastructure (user shares,
    // public links, expiration, revocation) without duplicating it for prompts.
    const name = (prompt.display_name || prompt.name).trim() || "Prompt";
    createAssetMutation.mutate(
      {
        name,
        description: prompt.description || `Snapshot of prompt "${prompt.name}"`,
        content_type: "text/markdown",
        content: prompt.content,
        tags: prompt.category ? ["prompt", prompt.category] : ["prompt"],
      },
      {
        onSuccess: (asset) => {
          setShareAssetId(asset.id);
          setShareOpen(true);
        },
        onError: (err) => {
          setError(err instanceof Error ? err.message : "Failed to prepare share");
        },
      },
    );
  }, [prompt, createAssetMutation]);

  if (isLoading) {
    return <LoadingIndicator />;
  }

  if (!prompt) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
        <MessageSquare className="h-12 w-12 mb-2 opacity-30" />
        <p className="text-sm">Prompt not found</p>
        <button onClick={onBack} className="mt-2 text-sm text-primary hover:underline">Back</button>
      </div>
    );
  }

  const dirty = editing && (
    form.name !== prompt.name ||
    form.display_name !== prompt.display_name ||
    form.description !== prompt.description ||
    form.content !== prompt.content ||
    form.category !== prompt.category
  );

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex flex-wrap items-center gap-2">
        <button
          onClick={onBack}
          className="rounded-md p-1.5 hover:bg-accent"
          aria-label="Back"
        >
          <ArrowLeft className="h-4 w-4" />
        </button>
        <h2 className="text-lg font-semibold truncate flex-1 min-w-0">{prompt.display_name || prompt.name}</h2>
        <ScopeBadge scope={prompt.scope} />

        {!editing && (
          <>
            <button
              onClick={handleCopyContent}
              className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent"
              title="Copy prompt content"
            >
              {copied ? <Check className="h-3.5 w-3.5 text-green-500" /> : <Copy className="h-3.5 w-3.5" />}
              {copied ? "Copied" : "Copy"}
            </button>
            <button
              onClick={handleSaveAsAsset}
              disabled={createAssetMutation.isPending}
              className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent disabled:opacity-50"
              title="Snapshot this prompt as a markdown asset in My Assets"
            >
              <FileBox className="h-3.5 w-3.5" />
              {createAssetMutation.isPending ? "Saving..." : "Save as Asset"}
            </button>
            <button
              onClick={handleShare}
              disabled={createAssetMutation.isPending}
              className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
              title="Save as asset and share with another user"
            >
              <Share2 className="h-3.5 w-3.5" />
              Share
            </button>
            {isOwner && (
              <>
                <button
                  onClick={() => setEditing(true)}
                  className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent"
                >
                  <Pencil className="h-3.5 w-3.5" /> Edit
                </button>
                <button
                  onClick={() => setDeleteOpen(true)}
                  className="inline-flex items-center gap-1.5 rounded-md border border-destructive/30 px-3 py-1.5 text-sm font-medium text-destructive hover:bg-destructive/10"
                >
                  <Trash2 className="h-3.5 w-3.5" /> Delete
                </button>
              </>
            )}
          </>
        )}

        {editing && (
          <>
            <button
              onClick={handleSave}
              disabled={!dirty || updateMutation.isPending}
              className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              <Save className="h-3.5 w-3.5" /> {updateMutation.isPending ? "Saving..." : "Save"}
            </button>
            <button
              onClick={() => { setEditing(false); setError(null); }}
              className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent"
            >
              <X className="h-3.5 w-3.5" /> Cancel
            </button>
          </>
        )}
      </div>

      {/* Notices */}
      {error && (
        <div className="rounded-md bg-red-500/10 border border-red-500/20 px-3 py-2 text-xs text-red-400">{error}</div>
      )}
      {saveAsAssetNotice && (
        <div className="flex items-center justify-between rounded-md bg-emerald-500/10 border border-emerald-500/20 px-3 py-2 text-xs text-emerald-400">
          <span>Saved as asset “{saveAsAssetNotice.name}”.</span>
          <div className="flex items-center gap-3">
            <button
              onClick={() => onNavigate(`/assets/${saveAsAssetNotice.assetId}`)}
              className="underline hover:text-emerald-300"
            >
              Open asset
            </button>
            <button
              onClick={() => setSaveAsAssetNotice(null)}
              className="text-emerald-400/70 hover:text-emerald-300"
              aria-label="Dismiss"
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        </div>
      )}

      {/* Metadata strip */}
      {!editing && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3 rounded-lg border bg-card p-3 text-xs">
          <div><span className="text-muted-foreground">Name:</span> <span className="font-mono select-all">{prompt.name}</span></div>
          <div><span className="text-muted-foreground">Category:</span> <span>{prompt.category || "—"}</span></div>
          <div className="md:col-span-2"><span className="text-muted-foreground">Description:</span> <span className="break-words">{prompt.description || "—"}</span></div>
          <div><span className="text-muted-foreground">Owner:</span> <span>{prompt.owner_email || "—"}</span></div>
          <div><span className="text-muted-foreground">Updated:</span> <span>{prompt.updated_at ? new Date(prompt.updated_at).toLocaleString() : "—"}</span></div>
        </div>
      )}

      {/* Arguments */}
      {!editing && <ArgumentsPanel args={prompt.arguments} />}

      {/* Edit form */}
      {editing && (
        <div className="rounded-lg border bg-card p-4 space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-xs text-muted-foreground">Name</label>
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Display Name</label>
              <input value={form.display_name} onChange={(e) => setForm({ ...form, display_name: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" />
            </div>
            <div className="col-span-2">
              <label className="text-xs text-muted-foreground">Description</label>
              <textarea
                value={form.description}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                rows={3}
                className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none resize-y"
              />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Category</label>
              <input value={form.category} onChange={(e) => setForm({ ...form, category: e.target.value })} className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none" />
            </div>
          </div>
          <div>
            <label className="text-xs text-muted-foreground">Content (Markdown)</label>
            <textarea
              value={form.content}
              onChange={(e) => handleContentChange(e.target.value)}
              rows={16}
              className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none font-mono"
            />
            <p className="text-[11px] text-muted-foreground mt-1">
              Use <code className="font-mono">{"{{name}}"}</code> (preferred) or <code className="font-mono">{"{name}"}</code> to declare an argument. Rows auto-appear below as you type.
            </p>
          </div>

          {/* Arguments editor — auto-synced from content above */}
          <div>
            <label className="text-xs text-muted-foreground">Arguments</label>
            {form.arguments.length === 0 ? (
              <div className="rounded-md border bg-muted/20 px-3 py-3 text-xs text-muted-foreground">
                No arguments yet. Add a <code className="font-mono">{"{{placeholder}}"}</code> in the content above.
              </div>
            ) : (
              <div className="rounded-md border bg-card overflow-hidden">
                <div className="grid grid-cols-[minmax(0,160px)_minmax(0,1fr)_110px] gap-3 px-3 py-2 border-b bg-muted/40 text-[11px] font-medium text-muted-foreground uppercase tracking-wide">
                  <div>Name</div>
                  <div>Description</div>
                  <div className="text-right">Required</div>
                </div>
                <ul className="divide-y">
                  {form.arguments.map((a) => (
                    <li key={a.name} className="grid grid-cols-[minmax(0,160px)_minmax(0,1fr)_110px] gap-3 px-3 py-2 items-start">
                      <code className="text-xs font-mono text-foreground bg-muted/60 rounded px-1.5 py-0.5 break-all mt-1">
                        {`{{${a.name}}}`}
                      </code>
                      <textarea
                        value={a.description}
                        onChange={(e) => updateArgField(a.name, { description: e.target.value })}
                        placeholder="What this argument is for"
                        rows={2}
                        className="w-full rounded-md border bg-background px-2 py-1 text-xs outline-none ring-ring focus:ring-2 resize-y"
                      />
                      <label className="inline-flex items-center justify-end gap-2 text-xs cursor-pointer select-none mt-1.5">
                        <input
                          type="checkbox"
                          checked={a.required}
                          onChange={(e) => updateArgField(a.name, { required: e.target.checked })}
                          className="h-3.5 w-3.5"
                        />
                        <span className={cn("font-medium", a.required ? "text-rose-400" : "text-muted-foreground")}>
                          {a.required ? "Required" : "Optional"}
                        </span>
                      </label>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        </div>
      )}

      {/* View mode toggle */}
      {!editing && (
        <div className="flex items-center gap-2">
          <div className="inline-flex rounded-md border text-sm">
            <button
              type="button"
              onClick={() => setViewMode("preview")}
              className={cn("flex items-center gap-1.5 px-3 py-1.5 rounded-l-md", viewMode === "preview" ? "bg-accent font-medium" : "hover:bg-accent/50")}
            >
              <Eye className="h-3.5 w-3.5" /> Preview
            </button>
            <button
              type="button"
              onClick={() => setViewMode("source")}
              className={cn("flex items-center gap-1.5 px-3 py-1.5 rounded-r-md border-l", viewMode === "source" ? "bg-accent font-medium" : "hover:bg-accent/50")}
            >
              <Code className="h-3.5 w-3.5" /> Source
            </button>
          </div>
        </div>
      )}

      {/* Content body */}
      {!editing && (
        viewMode === "preview" ? (
          <div className="rounded-lg border bg-card p-6">
            <MarkdownRenderer content={prompt.content} />
          </div>
        ) : (
          <pre className="rounded-lg border bg-card p-6 text-sm overflow-auto whitespace-pre-wrap font-mono">{prompt.content}</pre>
        )
      )}

      {/* Delete confirmation modal */}
      {deleteOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={() => setDeleteOpen(false)}
            onKeyDown={(e) => { if (e.key === "Escape") setDeleteOpen(false); }}
            role="button"
            tabIndex={-1}
            aria-label="Close"
          />
          <div className="relative rounded-lg border bg-card p-6 shadow-lg max-w-sm w-full mx-4 space-y-4">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-destructive/10">
                <AlertTriangle className="h-5 w-5 text-destructive" />
              </div>
              <div>
                <h3 className="text-sm font-semibold">Delete prompt</h3>
                <p className="text-sm text-muted-foreground">This action cannot be undone.</p>
              </div>
            </div>
            <p className="text-sm">
              Delete <span className="font-medium">{prompt.display_name || prompt.name}</span>?
            </p>
            <div className="flex justify-end gap-2">
              <button onClick={() => setDeleteOpen(false)} className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80">Cancel</button>
              <button
                onClick={handleDelete}
                disabled={deleteMutation.isPending}
                className="rounded-md bg-destructive px-4 py-2 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
              >
                {deleteMutation.isPending ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Share dialog (over a freshly created asset snapshot) */}
      {shareAssetId && (
        <ShareDialog
          target={{ type: "asset", id: shareAssetId }}
          open={shareOpen}
          onOpenChange={(o) => {
            setShareOpen(o);
            if (!o) {
              // Surface the asset snapshot so the user can find it later.
              setSaveAsAssetNotice({ assetId: shareAssetId, name: prompt.display_name || prompt.name });
              setShareAssetId(null);
            }
          }}
        />
      )}
    </div>
  );
}
