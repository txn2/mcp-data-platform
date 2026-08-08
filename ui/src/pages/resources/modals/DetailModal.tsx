import { Tag, Trash2, Pencil, Download, X } from "lucide-react";
import { resourceFetchRaw } from "@/api/resources/client";
import { useResource } from "@/api/resources/hooks";
import { useAuthStore } from "@/stores/auth";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import type { Resource } from "@/api/resources/types";
import { scopeIcon, scopeLabel } from "./shared";
import { Overlay } from "./Overlay";
import { ResourcePreview } from "./ResourcePreview";
import { UsedByPrompts } from "./UsedByPrompts";
import { UsagePanel } from "./UsagePanel";
import { VersionsPanel } from "./VersionsPanel";

// downloadResource pulls the current content and hands it to the browser under
// the resource's own filename.
async function downloadResource(r: Resource) {
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
}

export function DetailModal({ resource, onClose, onEdit, onDelete, admin }: { resource: Resource; onClose: () => void; onEdit: () => void; onDelete: () => void; admin: boolean }) {
  // The row the list handed over carries no usage: only the detail read
  // consults the audit rollup. Re-read it so the panel has something to show,
  // and fall back to the list's copy while that is in flight (and on a
  // deployment where the detail read fails, where the metadata is still worth
  // showing).
  const { data: detail } = useResource(resource.id);
  const r = detail ?? resource;
  const ScopeIcon = scopeIcon(r.scope);
  const currentUser = useAuthStore((s) => s.user);
  // Users can only edit/delete their own resources. Admins can edit/delete any.
  const canModify = admin || r.uploader_sub === currentUser?.user_id;

  return (
    <Overlay onClose={onClose}>
      <div className="w-full space-y-4 rounded-lg border bg-card p-6 shadow-lg">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 flex-1">
            <h2 className="truncate text-lg font-semibold">{r.display_name}</h2>
            <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
              <ScopeIcon className="h-3 w-3" />
              {scopeLabel(r.scope, r.scope_id)} / {r.category} / {r.filename}
            </p>
          </div>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
            <X />
          </Button>
        </div>

        {r.description && (
          <div className="text-sm text-muted-foreground">
            <CollapsibleMarkdown content={r.description} maxHeightPx={160} />
          </div>
        )}

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
          <p className="mt-0.5 rounded bg-muted px-2 py-1 font-mono text-xs break-all">{r.uri}</p>
        </div>

        <ResourcePreview resource={r} />

        {r.tags.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {r.tags.map((t) => (
              <Badge key={t} variant="muted">
                <Tag />
                {t}
              </Badge>
            ))}
          </div>
        )}

        <UsagePanel usage={r.usage} lastReadAt={r.last_read_at} createdAt={r.created_at} />

        <VersionsPanel resource={r} canModify={canModify} />

        <UsedByPrompts resourceId={r.id} />

        <div className="flex items-center gap-2 border-t pt-2">
          <Button variant="outline" onClick={() => void downloadResource(r)}>
            <Download />
            Download
          </Button>
          {canModify && (
            <>
              <Button variant="outline" onClick={onEdit}>
                <Pencil />
                Edit
              </Button>
              <Button
                variant="outline"
                onClick={onDelete}
                className="ml-auto border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
              >
                <Trash2 />
                Delete
              </Button>
            </>
          )}
        </div>
      </div>
    </Overlay>
  );
}
