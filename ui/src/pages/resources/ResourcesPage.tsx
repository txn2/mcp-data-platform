import { useState, useRef, useCallback } from "react";
import {
  Search,
  FileUp,
  Tag,
  Trash2,
  Pencil,
  Download,
  Globe,
  Users,
  User,
  FolderOpen,
  File,
  FileText,
  X,
  Loader2,
} from "lucide-react";
import { useResources, useUploadResource, useUpdateResource, useDeleteResource } from "@/api/resources/hooks";
import { BASE_URL } from "@/api/resources/client";
import { useAuthStore } from "@/stores/auth";
import { usePersonas } from "@/api/admin/hooks";
import { formatBytes } from "@/lib/format";
import type { Resource, ResourceUpdate } from "@/api/resources/types";

const CATEGORIES = ["samples", "playbooks", "templates", "references"] as const;

interface Props {
  admin?: boolean;
  onNavigate?: (path: string) => void;
}

function scopeIcon(scope: string) {
  if (scope === "global") return Globe;
  if (scope === "persona") return Users;
  return User;
}

function scopeLabel(scope: string, scopeId: string) {
  if (scope === "global") return "Global";
  if (scope === "persona") return scopeId;
  return "My Resources";
}

function categoryColor(cat: string) {
  switch (cat) {
    case "samples":
      return "bg-sky-100 text-sky-700 dark:bg-sky-950 dark:text-sky-300";
    case "playbooks":
      return "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300";
    case "templates":
      return "bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300";
    case "references":
      return "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
    default:
      return "bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300";
  }
}

function scopeBadgeColor(scope: string) {
  switch (scope) {
    case "global":
      return "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300";
    case "persona":
      return "bg-orange-100 text-orange-700 dark:bg-orange-950 dark:text-orange-300";
    default:
      return "bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300";
  }
}

// --- Main Page ---

export function ResourcesPage({ admin }: Props) {
  const userPersona = useAuthStore((s) => s.user?.persona);
  const { data: personaData } = usePersonas();
  const personaNames = (personaData?.personas ?? []).map((p) => p.name);

  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("");
  const [activeTab, setActiveTab] = useState<string>(admin ? "all" : "user");
  const [showUpload, setShowUpload] = useState(false);
  const [detail, setDetail] = useState<Resource | null>(null);
  const [editing, setEditing] = useState<Resource | null>(null);
  const [deleting, setDeleting] = useState<Resource | null>(null);

  // User mode: filter by tab scope. Admin mode: "all" tab fetches without scope filter.
  const queryParams: Record<string, string | undefined> = {
    category: category || undefined,
    q: search || undefined,
  };
  if (activeTab !== "all") {
    queryParams.scope = activeTab === "user" ? "user" : activeTab === "global" ? "global" : "persona";
    if (activeTab !== "user" && activeTab !== "global") {
      queryParams.scope_id = activeTab;
    }
  }

  const { data, isLoading } = useResources(queryParams);

  const resources = data?.resources ?? [];
  const total = data?.total ?? 0;

  // Build tabs based on mode.
  // User: My Resources + persona tab (if assigned) + Global
  // Admin: All + per-persona tabs + Global
  const tabs: { key: string; label: string; icon: typeof Globe }[] = [];
  if (admin) {
    tabs.push({ key: "all", label: "All Resources", icon: FolderOpen });
    tabs.push({ key: "global", label: "Global", icon: Globe });
    for (const name of personaNames) {
      tabs.push({ key: name, label: name, icon: Users });
    }
    tabs.push({ key: "user", label: "User", icon: User });
  } else {
    tabs.push({ key: "user", label: "My Resources", icon: User });
    if (userPersona) {
      tabs.push({ key: userPersona, label: userPersona, icon: Users });
    }
    tabs.push({ key: "global", label: "Global", icon: Globe });
  }

  return (
    <div className="space-y-4">
      {/* Tabs */}
      <div className="flex items-center gap-2 border-b pb-px overflow-x-auto">
        {tabs.map((tab) => {
          const Icon = tab.icon;
          return (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={`flex items-center gap-1.5 px-3 py-2 text-sm font-medium border-b-2 transition-colors whitespace-nowrap ${
                activeTab === tab.key
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground hover:border-muted-foreground/30"
              }`}
            >
              <Icon className="h-3.5 w-3.5" />
              {tab.label}
            </button>
          );
        })}
      </div>

      {/* Filters + Upload */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[200px]">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search resources..."
            className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>
        <select
          value={category}
          onChange={(e) => setCategory(e.target.value)}
          className="rounded-md border bg-background px-3 py-2 text-sm"
        >
          <option value="">All categories</option>
          {CATEGORIES.map((c) => (
            <option key={c} value={c}>{c}</option>
          ))}
        </select>
        <button
          onClick={() => setShowUpload(true)}
          className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 transition-colors"
        >
          <FileUp className="h-4 w-4" />
          Upload
        </button>
      </div>

      {/* Results */}
      {isLoading ? (
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          <Loader2 className="h-5 w-5 animate-spin mr-2" />
          Loading...
        </div>
      ) : resources.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <FolderOpen className="h-12 w-12 mb-2 opacity-30" />
          <p className="text-sm font-medium">No resources yet</p>
          <p className="text-xs mt-1 max-w-sm text-center">
            Upload reference material like samples, playbooks, templates, or references
            that will be available to both humans and AI agents.
          </p>
          <button
            onClick={() => setShowUpload(true)}
            className="mt-3 inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            <FileUp className="h-4 w-4" />
            Upload Resource
          </button>
        </div>
      ) : (
        <div className="rounded-lg border bg-card overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width: admin ? "25%" : "30%"}}>Name</th>
                {admin && <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"10%"}}>Scope</th>}
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"9%"}}>Category</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"13%"}}>Type</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"12%"}}>Tags</th>
                <th className="px-4 py-2.5 text-right font-medium text-muted-foreground" style={{width:"7%"}}>Size</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width: admin ? "13%" : "15%"}}>Uploader</th>
                <th className="px-4 py-2.5 text-left font-medium text-muted-foreground" style={{width:"8%"}}>Updated</th>
                <th className="px-4 py-2.5" style={{width:"3%"}} />
              </tr>
            </thead>
            <tbody>
              {resources.map((r) => {
                const ScopeIcon = scopeIcon(r.scope);
                return (
                  <tr
                    key={r.id}
                    onClick={() => setDetail(r)}
                    className="border-b last:border-0 cursor-pointer transition-colors hover:bg-accent/50"
                  >
                    <td className="px-4 py-2.5 max-w-0">
                      <div className="flex items-center gap-2">
                        <File className="h-4 w-4 text-muted-foreground shrink-0" />
                        <div className="min-w-0 flex-1">
                          <span className="font-medium truncate block">{r.display_name}</span>
                          <span className="text-xs text-muted-foreground truncate block">{r.description}</span>
                        </div>
                      </div>
                    </td>
                    {admin && (
                      <td className="px-4 py-2.5">
                        <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium whitespace-nowrap inline-flex items-center gap-0.5 ${scopeBadgeColor(r.scope)}`}>
                          <ScopeIcon className="h-2.5 w-2.5" />
                          {scopeLabel(r.scope, r.scope_id)}
                        </span>
                      </td>
                    )}
                    <td className="px-4 py-2.5">
                      <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium whitespace-nowrap ${categoryColor(r.category)}`}>
                        {r.category}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 text-xs text-muted-foreground truncate">{r.mime_type}</td>
                    <td className="px-4 py-2.5 max-w-0">
                      <div className="flex flex-wrap gap-1">
                        {r.tags.slice(0, 3).map((t) => (
                          <span key={t} className="text-[10px] px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground truncate max-w-[80px]">
                            {t}
                          </span>
                        ))}
                        {r.tags.length > 3 && (
                          <span className="text-[10px] text-muted-foreground">+{r.tags.length - 3}</span>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-2.5 text-right text-muted-foreground">{formatBytes(r.size_bytes)}</td>
                    <td className="px-4 py-2.5 text-xs text-muted-foreground truncate">{r.uploader_email || r.uploader_sub}</td>
                    <td className="px-4 py-2.5 text-xs text-muted-foreground">{new Date(r.updated_at).toLocaleDateString()}</td>
                    <td className="px-2 py-2.5">
                      <button
                        onClick={(e) => { e.stopPropagation(); setDetail(r); }}
                        className="rounded p-1 text-muted-foreground hover:text-foreground hover:bg-accent"
                        title="View details"
                      >
                        <FileText className="h-3.5 w-3.5" />
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {total > resources.length && (
        <p className="text-sm text-muted-foreground text-center">
          Showing {resources.length} of {total} resources
        </p>
      )}

      {/* Upload Modal */}
      {showUpload && <UploadModal onClose={() => setShowUpload(false)} admin={!!admin} personaNames={personaNames} />}

      {/* Detail Modal */}
      {detail && (
        <DetailModal
          resource={detail}
          admin={!!admin}
          onClose={() => setDetail(null)}
          onEdit={() => { setEditing(detail); setDetail(null); }}
          onDelete={() => { setDeleting(detail); setDetail(null); }}
        />
      )}

      {/* Edit Modal */}
      {editing && <EditModal resource={editing} onClose={() => setEditing(null)} />}

      {/* Delete Confirm */}
      {deleting && <DeleteConfirm resource={deleting} onClose={() => setDeleting(null)} />}
    </div>
  );
}


// --- Upload Modal ---

function UploadModal({ onClose, admin, personaNames }: { onClose: () => void; admin: boolean; personaNames: string[] }) {
  const upload = useUploadResource();
  const user = useAuthStore((s) => s.user);
  const fileRef = useRef<HTMLInputElement>(null);
  // Users can only upload to their own scope. Admins default to global.
  const [scope, setScope] = useState(admin ? "global" : "user");
  const [selectedPersonas, setSelectedPersonas] = useState<string[]>([]);
  const [userEmails, setUserEmails] = useState("");
  const [cat, setCat] = useState("samples");
  const [customCat, setCustomCat] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [description, setDescription] = useState("");
  const [tagsInput, setTagsInput] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [uploading, setUploading] = useState(false);

  const effectiveCategory = cat === "custom" ? customCat : cat;

  function togglePersona(name: string) {
    setSelectedPersonas((prev) =>
      prev.includes(name) ? prev.filter((p) => p !== name) : [...prev, name],
    );
  }

  // Build the list of (scope, scope_id) pairs to upload to.
  function resolveTargets(): { scope: string; scope_id: string }[] {
    if (scope === "global") return [{ scope: "global", scope_id: "" }];
    if (scope === "persona") {
      return selectedPersonas.map((name) => ({ scope: "persona", scope_id: name }));
    }
    if (scope === "user" && admin) {
      const emails = userEmails.split(",").map((e) => e.trim()).filter(Boolean);
      return emails.map((email) => ({ scope: "user", scope_id: email }));
    }
    // Non-admin user scope: always own user
    return [{ scope: "user", scope_id: user?.user_id || "" }];
  }

  const handleSubmit = useCallback(async () => {
    if (!file) { setError("File is required"); return; }
    if (!displayName.trim()) { setError("Display name is required"); return; }
    if (!description.trim()) { setError("Description is required"); return; }

    const targets = resolveTargets();
    if (targets.length === 0) {
      if (scope === "persona") setError("Select at least one persona");
      else if (scope === "user") setError("Enter at least one email address");
      return;
    }

    setUploading(true);
    setError("");

    const tags = tagsInput.split(",").map((t) => t.trim().toLowerCase()).filter(Boolean);
    const errors: string[] = [];

    for (const target of targets) {
      const fd = new FormData();
      fd.set("scope", target.scope);
      if (target.scope_id) fd.set("scope_id", target.scope_id);
      fd.set("category", effectiveCategory);
      fd.set("display_name", displayName.trim());
      fd.set("description", description.trim());
      fd.set("file", file);
      for (const t of tags) fd.append("tags", t);

      try {
        await upload.mutateAsync(fd);
      } catch (err) {
        errors.push(`${target.scope_id || "global"}: ${err instanceof Error ? err.message : "failed"}`);
      }
    }

    setUploading(false);
    if (errors.length > 0) {
      setError(errors.join("; "));
    } else {
      onClose();
    }
  }, [file, displayName, description, scope, selectedPersonas, userEmails, effectiveCategory, tagsInput, user, upload, onClose]);

  return (
    <Overlay onClose={onClose}>
      <div className="bg-card rounded-lg border shadow-lg w-full p-6 space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-semibold">Upload Resource</h2>
          <button onClick={onClose} className="rounded p-1 hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>

        {error && <p className="text-sm text-destructive bg-destructive/10 rounded-md px-3 py-2">{error}</p>}

        <div className="space-y-3">
          {admin && (
            <label className="block space-y-1">
              <span className="text-xs font-medium text-muted-foreground">Scope</span>
              <select value={scope} onChange={(e) => { setScope(e.target.value); setSelectedPersonas([]); setUserEmails(""); }} className="w-full rounded-md border bg-background px-3 py-2 text-sm">
                <option value="global">Global</option>
                <option value="persona">Persona</option>
                <option value="user">User</option>
              </select>
            </label>
          )}
          {admin && scope === "persona" && (
            <div className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">Personas</span>
              <div className="rounded-md border bg-background p-2 max-h-32 overflow-y-auto space-y-0.5">
                {personaNames.length === 0 ? (
                  <p className="text-xs text-muted-foreground py-1 px-1">No personas configured</p>
                ) : personaNames.map((name) => (
                  <label key={name} className="flex items-center gap-2 rounded px-2 py-1.5 hover:bg-muted cursor-pointer text-sm">
                    <input
                      type="checkbox"
                      checked={selectedPersonas.includes(name)}
                      onChange={() => togglePersona(name)}
                      className="rounded border-muted-foreground"
                    />
                    <Users className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                    {name}
                  </label>
                ))}
              </div>
              {selectedPersonas.length > 0 && (
                <p className="text-xs text-muted-foreground">{selectedPersonas.length} selected — one resource will be created per persona</p>
              )}
            </div>
          )}
          {admin && scope === "user" && (
            <label className="block space-y-1">
              <span className="text-xs font-medium text-muted-foreground">User emails (comma-separated)</span>
              <input
                value={userEmails}
                onChange={(e) => setUserEmails(e.target.value)}
                placeholder="user@example.com, other@example.com"
                className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
              />
              {userEmails.split(",").filter((e) => e.trim()).length > 1 && (
                <p className="text-xs text-muted-foreground">{userEmails.split(",").filter((e) => e.trim()).length} users — one resource will be created per user</p>
              )}
            </label>
          )}
          <div className="grid grid-cols-2 gap-3">
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">Category</span>
              <select value={cat} onChange={(e) => setCat(e.target.value)} className="w-full rounded-md border bg-background px-3 py-2 text-sm">
                {CATEGORIES.map((c) => <option key={c} value={c}>{c}</option>)}
                <option value="custom">Custom...</option>
              </select>
            </label>
            {cat === "custom" && (
              <label className="space-y-1">
                <span className="text-xs font-medium text-muted-foreground">Custom Category</span>
                <input value={customCat} onChange={(e) => setCustomCat(e.target.value.toLowerCase())} placeholder="e.g. guides" className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2" />
              </label>
            )}
          </div>
        </div>

        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Display Name</span>
          <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="Human-readable name" className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2" />
        </label>

        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Description</span>
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What is this and what should the agent do with it?" rows={2} className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2 resize-none" />
        </label>

        <div className="grid grid-cols-2 gap-3">
          <label className="block space-y-1">
            <span className="text-xs font-medium text-muted-foreground">Tags (comma-separated)</span>
            <input value={tagsInput} onChange={(e) => setTagsInput(e.target.value)} placeholder="finance, q4" className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2" />
          </label>
          <div className="space-y-1">
            <span className="text-xs font-medium text-muted-foreground">File</span>
            <div
              onClick={() => fileRef.current?.click()}
              className="flex items-center justify-center gap-2 rounded-md border-2 border-dashed bg-muted/30 px-3 py-2 cursor-pointer hover:border-primary/40 transition-colors"
            >
              {file ? (
                <span className="text-xs truncate">{file.name} ({formatBytes(file.size)})</span>
              ) : (
                <span className="text-xs text-muted-foreground">Choose file (max 100 MB)</span>
              )}
            </div>
            <input ref={fileRef} type="file" className="hidden" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
          </div>
        </div>

        <div className="flex justify-end gap-2 pt-2">
          <button onClick={onClose} className="rounded-md border px-4 py-2 text-sm hover:bg-muted transition-colors">Cancel</button>
          <button
            onClick={handleSubmit}
            disabled={uploading}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50 transition-colors"
          >
            {uploading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Upload
          </button>
        </div>
      </div>
    </Overlay>
  );
}


// --- Detail Modal ---

function DetailModal({ resource: r, onClose, onEdit, onDelete, admin }: { resource: Resource; onClose: () => void; onEdit: () => void; onDelete: () => void; admin: boolean }) {
  const ScopeIcon = scopeIcon(r.scope);
  const currentUser = useAuthStore((s) => s.user);
  // Users can only edit/delete their own resources. Admins can edit/delete any.
  const canModify = admin || r.uploader_sub === currentUser?.user_id;

  return (
    <Overlay onClose={onClose}>
      <div className="bg-card rounded-lg border shadow-lg w-full p-6 space-y-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 flex-1">
            <h2 className="text-lg font-semibold truncate">{r.display_name}</h2>
            <p className="text-xs text-muted-foreground mt-0.5 flex items-center gap-1.5">
              <ScopeIcon className="h-3 w-3" />
              {scopeLabel(r.scope, r.scope_id)} / {r.category} / {r.filename}
            </p>
          </div>
          <button onClick={onClose} className="rounded p-1 hover:bg-muted shrink-0"><X className="h-4 w-4" /></button>
        </div>

        <p className="text-sm text-muted-foreground">{r.description}</p>

        <div className="grid grid-cols-2 gap-3 text-sm">
          <div>
            <span className="text-xs font-medium text-muted-foreground">MIME Type</span>
            <p>{r.mime_type}</p>
          </div>
          <div>
            <span className="text-xs font-medium text-muted-foreground">Size</span>
            <p>{formatBytes(r.size_bytes)}</p>
          </div>
          <div>
            <span className="text-xs font-medium text-muted-foreground">Uploader</span>
            <p className="truncate">{r.uploader_email || r.uploader_sub}</p>
          </div>
          <div>
            <span className="text-xs font-medium text-muted-foreground">Updated</span>
            <p>{new Date(r.updated_at).toLocaleString()}</p>
          </div>
        </div>

        <div>
          <span className="text-xs font-medium text-muted-foreground">URI</span>
          <p className="text-xs font-mono bg-muted rounded px-2 py-1 mt-0.5 break-all">{r.uri}</p>
        </div>

        {r.tags.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {r.tags.map((t) => (
              <span key={t} className="text-[10px] px-2 py-0.5 rounded-full bg-muted text-muted-foreground inline-flex items-center gap-1">
                <Tag className="h-2.5 w-2.5" />{t}
              </span>
            ))}
          </div>
        )}

        <div className="flex items-center gap-2 pt-2 border-t">
          <a
            href={`${BASE_URL}/${r.id}/content`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 rounded-md border px-3 py-2 text-sm hover:bg-muted transition-colors"
          >
            <Download className="h-3.5 w-3.5" />
            Download
          </a>
          {canModify && (
            <>
              <button
                onClick={onEdit}
                className="inline-flex items-center gap-1.5 rounded-md border px-3 py-2 text-sm hover:bg-muted transition-colors"
              >
                <Pencil className="h-3.5 w-3.5" />
                Edit
              </button>
              <button
                onClick={onDelete}
                className="inline-flex items-center gap-1.5 rounded-md border border-destructive/30 text-destructive px-3 py-2 text-sm hover:bg-destructive/10 transition-colors ml-auto"
              >
                <Trash2 className="h-3.5 w-3.5" />
                Delete
              </button>
            </>
          )}
        </div>
      </div>
    </Overlay>
  );
}


// --- Edit Modal ---

function EditModal({ resource: r, onClose }: { resource: Resource; onClose: () => void }) {
  const update = useUpdateResource();
  const [displayName, setDisplayName] = useState(r.display_name);
  const [description, setDescription] = useState(r.description);
  const [tagsInput, setTagsInput] = useState(r.tags.join(", "));
  const [cat, setCat] = useState(r.category);
  const [error, setError] = useState("");

  const handleSave = useCallback(async () => {
    const upd: ResourceUpdate = {};
    if (displayName.trim() !== r.display_name) upd.display_name = displayName.trim();
    if (description.trim() !== r.description) upd.description = description.trim();
    const tags = tagsInput.split(",").map((t) => t.trim().toLowerCase()).filter(Boolean);
    if (JSON.stringify(tags) !== JSON.stringify(r.tags)) upd.tags = tags;
    if (cat !== r.category) upd.category = cat;

    if (Object.keys(upd).length === 0) { onClose(); return; }

    try {
      await update.mutateAsync({ id: r.id, update: upd });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Update failed");
    }
  }, [displayName, description, tagsInput, cat, r, update, onClose]);

  return (
    <Overlay onClose={onClose}>
      <div className="bg-card rounded-lg border shadow-lg w-full p-6 space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-semibold">Edit Resource</h2>
          <button onClick={onClose} className="rounded p-1 hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>

        {error && <p className="text-sm text-destructive bg-destructive/10 rounded-md px-3 py-2">{error}</p>}

        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Display Name</span>
          <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2" />
        </label>

        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Description</span>
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} rows={3} className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2 resize-none" />
        </label>

        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Category</span>
          <select value={cat} onChange={(e) => setCat(e.target.value)} className="w-full rounded-md border bg-background px-3 py-2 text-sm">
            {CATEGORIES.map((c) => <option key={c} value={c}>{c}</option>)}
            {!CATEGORIES.includes(cat as typeof CATEGORIES[number]) && <option value={cat}>{cat}</option>}
          </select>
        </label>

        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Tags (comma-separated)</span>
          <input value={tagsInput} onChange={(e) => setTagsInput(e.target.value)} className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2" />
        </label>

        <div className="flex justify-end gap-2 pt-2">
          <button onClick={onClose} className="rounded-md border px-4 py-2 text-sm hover:bg-muted transition-colors">Cancel</button>
          <button
            onClick={handleSave}
            disabled={update.isPending}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50 transition-colors"
          >
            {update.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Save
          </button>
        </div>
      </div>
    </Overlay>
  );
}


// --- Delete Confirm ---

function DeleteConfirm({ resource: r, onClose }: { resource: Resource; onClose: () => void }) {
  const del = useDeleteResource();
  const [error, setError] = useState("");

  const handleDelete = useCallback(async () => {
    try {
      await del.mutateAsync(r.id);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Delete failed");
    }
  }, [r.id, del, onClose]);

  return (
    <Overlay onClose={onClose}>
      <div className="bg-card rounded-lg border shadow-lg w-full p-6 space-y-4">
        <h2 className="text-lg font-semibold">Delete Resource</h2>
        <p className="text-sm text-muted-foreground">
          Are you sure you want to delete <strong>{r.display_name}</strong>? This will remove both the metadata and the stored file. This action cannot be undone.
        </p>
        {error && <p className="text-sm text-destructive bg-destructive/10 rounded-md px-3 py-2">{error}</p>}
        <div className="flex justify-end gap-2 pt-2">
          <button onClick={onClose} className="rounded-md border px-4 py-2 text-sm hover:bg-muted transition-colors">Cancel</button>
          <button
            onClick={handleDelete}
            disabled={del.isPending}
            className="inline-flex items-center gap-1.5 rounded-md bg-destructive px-4 py-2 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50 transition-colors"
          >
            {del.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Delete
          </button>
        </div>
      </div>
    </Overlay>
  );
}


// --- Shared Overlay ---

function Overlay({ children, onClose }: { children: React.ReactNode; onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center overflow-y-auto py-4">
      <div className="fixed inset-0 bg-black/50" onClick={onClose} />
      <div className="relative z-10 mx-4 w-full max-w-lg">{children}</div>
    </div>
  );
}
