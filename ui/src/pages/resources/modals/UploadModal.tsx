import { useState, useRef, useCallback } from "react";
import { X, Users, Loader2 } from "lucide-react";
import { useUploadResource } from "@/api/resources/hooks";
import { useAuthStore } from "@/stores/auth";
import { formatBytes } from "@/lib/format";
import { parseTags } from "@/lib/tags";
import { CATEGORIES } from "./shared";
import { Overlay } from "./Overlay";

export function UploadModal({ onClose, admin, personaNames }: { onClose: () => void; admin: boolean; personaNames: string[] }) {
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
  const resolveTargets = useCallback((): { scope: string; scope_id: string }[] => {
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
  }, [scope, selectedPersonas, admin, userEmails, user]);

  const submitting = useRef(false);

  const handleSubmit = useCallback(async () => {
    if (submitting.current) return;
    submitting.current = true;

    if (!file) { setError("File is required"); submitting.current = false; return; }
    if (!displayName.trim()) { setError("Display name is required"); submitting.current = false; return; }
    if (!description.trim()) { setError("Description is required"); submitting.current = false; return; }

    const maxBytes = 100 * 1024 * 1024;
    if (file.size > maxBytes) { setError("File exceeds 100 MB limit"); submitting.current = false; return; }

    const targets = resolveTargets();
    if (targets.length === 0) {
      if (scope === "persona") setError("Select at least one persona");
      else if (scope === "user") setError("Enter at least one email address");
      submitting.current = false;
      return;
    }

    setUploading(true);
    setError("");

    const tags = parseTags(tagsInput);
    const successes: string[] = [];
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
        successes.push(target.scope_id || "global");
      } catch (err) {
        errors.push(`${target.scope_id || "global"}: ${err instanceof Error ? err.message : "failed"}`);
      }
    }

    setUploading(false);
    submitting.current = false;
    if (errors.length > 0) {
      const msg = errors.length === targets.length
        ? errors.join("; ")
        : `Succeeded: ${successes.join(", ")}. Failed: ${errors.join("; ")}`;
      setError(msg);
    } else {
      onClose();
    }
  }, [file, displayName, description, scope, effectiveCategory, tagsInput, upload, onClose, resolveTargets]);

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
