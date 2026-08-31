import { Tag } from "lucide-react";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { TablesPanel } from "@/components/tables/TablesPanel";
import { Badge } from "@/components/ui/badge";
import { DetailRow } from "@/components/viewer/DetailRow";
import { ThumbnailPanel } from "@/components/thumbnail/ThumbnailPanel";
import { formatBytes } from "@/lib/format";
import { resourceSubject } from "@/lib/thumbnailSupport";
import type { Resource } from "@/api/resources/types";
import { UsagePanel } from "./UsagePanel";
import { UsedByAssets } from "@/components/references/UsedByAssets";
import { UsedByPrompts } from "./UsedByPrompts";
import { VersionsPanel } from "./VersionsPanel";

/** Everything about a managed resource that is not its content, for the
 * viewer sidebar: what it is, what reads it, what it has been, and what it is
 * registered and attached to. */
export function ResourceSidebar({
  resource: r,
  canModify,
}: {
  resource: Resource;
  canModify: boolean;
}) {
  return (
    <>
      <div className="space-y-2">
        <h3 className="text-sm font-medium">Details</h3>
        {r.description && (
          <div className="text-sm text-muted-foreground">
            <CollapsibleMarkdown content={r.description} maxHeightPx={120} />
          </div>
        )}
        <dl className="space-y-1.5 text-sm">
          <DetailRow label="Type">
            <span className="font-mono text-xs">{r.mime_type}</span>
          </DetailRow>
          <DetailRow label="Size">{formatBytes(r.size_bytes)}</DetailRow>
          <DetailRow label="Uploader">
            <span className="block max-w-[160px] truncate text-xs">
              {r.uploader_email || r.uploader_sub}
            </span>
          </DetailRow>
          <DetailRow label="Created">{new Date(r.created_at).toLocaleString()}</DetailRow>
          <DetailRow label="Updated">{new Date(r.updated_at).toLocaleString()}</DetailRow>
        </dl>
      </div>

      <div className="space-y-2">
        <h3 className="text-sm font-medium">URI</h3>
        {/* The address this resource is cited by. Revising its content keeps
            it, which is what makes a citation written against it keep
            resolving. */}
        <p className="rounded bg-muted px-2 py-1 font-mono text-xs break-all">{r.uri}</p>
      </div>

      {r.tags.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-sm font-medium">Tags</h3>
          <div className="flex flex-wrap gap-1.5">
            {r.tags.map((t) => (
              <Badge key={t} variant="muted">
                <Tag />
                {t}
              </Badge>
            ))}
          </div>
        </div>
      )}

      <UsagePanel usage={r.usage} lastReadAt={r.last_read_at} createdAt={r.created_at} />

      <TablesPanel
        kind="resource"
        id={r.id}
        contentType={r.mime_type}
        filename={r.filename}
        canModify={canModify}
      />

      <VersionsPanel resource={r} canModify={canModify} />

      <UsedByPrompts resourceId={r.id} />

      <UsedByAssets target={{ kind: "resource", id: r.id }} />

      {/*
        The tile everyone else sees of this file, and the way back from one that
        shows the wrong thing. A resource is captured by the same capturer as an
        asset and stored under the same rule, and had neither the picture nor
        the button until this (#1568). It decides for itself whether to render:
        a reader who may not change the file, and a type nothing rasterizes, are
        shown nothing.
      */}
      <ThumbnailPanel subject={resourceSubject(r)} canModify={canModify} />
    </>
  );
}
