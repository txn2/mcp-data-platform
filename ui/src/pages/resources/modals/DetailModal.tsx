import { Tag, Trash2, Pencil, Download, X } from "lucide-react";
import { resourceFetchRaw } from "@/api/resources/client";
import { useAuthStore } from "@/stores/auth";
import { formatBytes } from "@/lib/format";
import type { Resource } from "@/api/resources/types";
import { scopeIcon, scopeLabel } from "./shared";
import { Overlay } from "./Overlay";
import { ResourcePreview } from "./ResourcePreview";
import { UsedByPrompts } from "./UsedByPrompts";

export function DetailModal({ resource: r, onClose, onEdit, onDelete, admin }: { resource: Resource; onClose: () => void; onEdit: () => void; onDelete: () => void; admin: boolean }) {
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

        <ResourcePreview resource={r} />

        {r.tags.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {r.tags.map((t) => (
              <span key={t} className="text-xs px-2 py-0.5 rounded-full bg-muted text-muted-foreground inline-flex items-center gap-1">
                <Tag className="h-2.5 w-2.5" />{t}
              </span>
            ))}
          </div>
        )}

        <UsedByPrompts resourceId={r.id} />

        <div className="flex items-center gap-2 pt-2 border-t">
          <button
            onClick={async () => {
              try {
                const res = await resourceFetchRaw(`/${r.id}/content`);
                if (!res.ok) return;
                const blob = await res.blob();
                const url = URL.createObjectURL(blob);
                const a = document.createElement("a");
                a.href = url;
                a.download = r.filename;
                a.click();
                URL.revokeObjectURL(url);
              } catch { /* ignore */ }
            }}
            className="inline-flex items-center gap-1.5 rounded-md border px-3 py-2 text-sm hover:bg-muted transition-colors"
          >
            <Download className="h-3.5 w-3.5" />
            Download
          </button>
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
