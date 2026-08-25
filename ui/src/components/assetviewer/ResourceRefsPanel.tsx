import { useMemo, useState } from "react";
import { AlertTriangle, ExternalLink, Image, Paperclip, Plus, X } from "lucide-react";
import {
  useAssetResources,
  useRemoveAssetResource,
  type AssetResourceRef,
  type AssetResourceRefsResponse,
} from "@/api/portal/hooks/assetResources";

import { CopyButton } from "@/components/provenance/parts";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import { RefPicker } from "./ResourceRefPicker";
import { ScopeChip } from "./ResourceRefScopeChip";

// ResourceRefsPanel is the person's end of the reference mechanism (#1475):
// which managed resources this asset's content depends on, adding one, and
// removing one.
//
// Before it, a reference could only be declared by an agent at save time and
// was invisible afterwards. An owner could not tell which files their report
// depended on, and someone deciding whether to make an asset public could not
// see that doing so would hand every one of those files to anyone with the
// link.
//
// It never edits the asset's content. A reference is a declaration beside the
// content; the markup has to name the URI for the picture to render, which is
// the author's edit to make, so every row carries the URI with a copy control.
export function ResourceRefsPanel({
  assetId,
  resourcePath,
  onNavigate,
}: {
  assetId: string;
  /** Where a managed resource opens for this reader, per surface. Absent, a
   * row names the resource without linking to it. */
  resourcePath?: (resourceId: string) => string;
  onNavigate?: (path: string) => void;
}) {
  const { data, isLoading, isError } = useAssetResources(assetId);
  const [picking, setPicking] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refs = useMemo(() => data?.data ?? [], [data]);
  const canEdit = data?.can_edit ?? false;

  // An asset with no references and a reader who cannot add one has nothing to
  // show, so the panel is absent rather than empty. A failed read hides it the
  // same way: a reader told nothing is better than one told, wrongly, that the
  // asset depends on nothing.
  //
  // The first load is also nothing, rather than a titled shell. Most assets
  // reference no files, so a shell would put a card on the sidebar of every
  // asset for as long as the read takes and then take it away again.
  if (isError || isLoading || (refs.length === 0 && !canEdit)) {
    return null;
  }

  return (
    <div className="border-t pt-4" data-testid="asset-resource-refs">
      <SectionCard
        title={<PanelTitle count={refs.length} />}
        action={
          canEdit && (
            <Button
              variant="outline"
              size="xs"
              onClick={() => {
                setPicking((p) => !p);
                setError(null);
              }}
            >
              <Plus /> Add
            </Button>
          )
        }
      >
        <PanelBody
          assetId={assetId}
          refs={refs}
          list={data}
          canEdit={canEdit}
          picking={picking}
          error={error}
          resourcePath={resourcePath}
          onNavigate={onNavigate}
          onError={setError}
          onClosePicker={() => setPicking(false)}
        />
      </SectionCard>
    </div>
  );
}

// PanelBody is the section's content in each state it can be in: empty and
// editable, listed, listed with the picker open, and carrying the refusal the
// server gave the last action.
function PanelBody({
  assetId,
  refs,
  list,
  canEdit,
  picking,
  error,
  resourcePath,
  onNavigate,
  onError,
  onClosePicker,
}: {
  assetId: string;
  refs: AssetResourceRef[];
  list?: AssetResourceRefsResponse;
  canEdit: boolean;
  picking: boolean;
  error: string | null;
  resourcePath?: (resourceId: string) => string;
  onNavigate?: (path: string) => void;
  onError: (msg: string | null) => void;
  onClosePicker: () => void;
}) {
  return (
    <div className="space-y-3">
      {canEdit && refs.length === 0 && (
        <EmptyState icon={Image}>
          Reference a logo, photograph, or design element instead of writing its bytes into this
          asset. The content names the file by its URI and the platform resolves it.
        </EmptyState>
      )}

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {refs.length > 0 && (
        <ul className="divide-y rounded-lg border">
          {refs.map((ref) => (
            <RefRow
              key={ref.resource_id}
              assetId={assetId}
              refItem={ref}
              canEdit={canEdit}
              contentScanned={list?.content_scanned ?? false}
              resourcePath={resourcePath}
              onNavigate={onNavigate}
              onError={onError}
            />
          ))}
        </ul>
      )}

      {picking && list && (
        <RefPicker
          assetId={assetId}
          referenced={refs.map((r) => r.resource_id)}
          audience={list.audience}
          notice={list.notice}
          full={refs.length >= list.max}
          onClose={onClosePicker}
          onError={onError}
        />
      )}
    </div>
  );
}

function PanelTitle({ count }: { count: number }) {
  return (
    <span className="flex items-center gap-2">
      <Paperclip className="size-4 text-muted-foreground" />
      Referenced files
      {count > 0 && <Badge variant="muted" className="text-[11px]">{count}</Badge>}
    </span>
  );
}

// RefRow is one referenced file: what it is, where the content names it, and
// the control that stops the asset referencing it.
function RefRow({
  assetId,
  refItem,
  canEdit,
  contentScanned,
  resourcePath,
  onNavigate,
  onError,
}: {
  assetId: string;
  refItem: AssetResourceRef;
  canEdit: boolean;
  /** Whether the server could read the asset's content to find the URI. */
  contentScanned: boolean;
  resourcePath?: (resourceId: string) => string;
  onNavigate?: (path: string) => void;
  onError: (msg: string | null) => void;
}) {
  const remove = useRemoveAssetResource(assetId);
  const [confirming, setConfirming] = useState(false);
  const occurrences = refItem.occurrences ?? [];
  // Removing without asking is only safe on a scan that ran and found nothing.
  // An unread content -- binary, too large, or a storage fault -- reports no
  // occurrences either, and treating that as proof of absence would withdraw
  // a grant from a live report in one unconfirmed click.
  const needsConfirm = occurrences.length > 0 || !contentScanned;

  function doRemove() {
    onError(null);
    setConfirming(false);
    remove.mutate(refItem.resource_id, {
      onError: (err) => onError(err instanceof Error ? err.message : "Remove failed"),
    });
  }

  return (
    <li className="space-y-2 px-3 py-3" data-testid="asset-resource-ref">
      <div className="flex items-start gap-3">
        <Thumbnail refItem={refItem} />
        <div className="min-w-0 flex-1">
          {refItem.broken ? (
            <BrokenLabel resourceId={refItem.resource_id} />
          ) : (
            <ResourceLabel
              refItem={refItem}
              resourcePath={resourcePath}
              onNavigate={onNavigate}
            />
          )}
        </div>
        {canEdit && (
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={`Remove ${refItem.display_name || refItem.uri}`}
            disabled={remove.isPending}
            onClick={() => (needsConfirm ? setConfirming(true) : doRemove())}
            className="shrink-0 text-muted-foreground hover:text-destructive"
          >
            <X />
          </Button>
        )}
      </div>

      <UriLine uri={refItem.uri} />

      {occurrences.length > 0 && !confirming && <InContentNote count={occurrences.length} />}

      {confirming && (
        <RemoveWarning
          refItem={refItem}
          contentScanned={contentScanned}
          onCancel={() => setConfirming(false)}
          onConfirm={doRemove}
        />
      )}
    </li>
  );
}

// Thumbnail shows an image reference as itself. It loads through the
// reference's own serving URL, which is the grant the asset already makes, so
// it renders for a reader who has no direct access to the file.
function Thumbnail({ refItem }: { refItem: AssetResourceRef }) {
  const isImage = (refItem.mime_type ?? "").startsWith("image/");
  if (refItem.broken || !isImage || !refItem.content_url) {
    return null;
  }
  return (
    <img
      src={refItem.content_url}
      alt={refItem.display_name || refItem.filename || "referenced image"}
      data-testid="asset-resource-thumb"
      className="size-10 shrink-0 rounded border bg-muted object-contain"
    />
  );
}

// ResourceLabel names the file, its scope and its type, linking to the resource
// where this reader could open it on its own.
function ResourceLabel({
  refItem,
  resourcePath,
  onNavigate,
}: {
  refItem: AssetResourceRef;
  resourcePath?: (resourceId: string) => string;
  onNavigate?: (path: string) => void;
}) {
  const name = refItem.display_name || refItem.filename || refItem.resource_id;
  const linkable = refItem.readable && resourcePath && onNavigate;
  return (
    <>
      {linkable ? (
        <button
          type="button"
          onClick={() => onNavigate(resourcePath(refItem.resource_id))}
          className="flex max-w-full items-center gap-1 truncate text-sm font-medium text-primary hover:underline"
        >
          <span className="truncate">{name}</span>
          <ExternalLink className="size-3 shrink-0" />
        </button>
      ) : (
        <p className="truncate text-sm font-medium">{name}</p>
      )}
      <p className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <ScopeChip scope={refItem.scope} scopeId={refItem.scope_id} />
        {refItem.mime_type && <span>{refItem.mime_type}</span>}
        {refItem.size_bytes ? <span>{formatBytes(refItem.size_bytes)}</span> : null}
      </p>
    </>
  );
}

// BrokenLabel marks a reference whose resource was deleted. The asset is
// serving with that file missing, and this row is where its owner finds out.
function BrokenLabel({ resourceId }: { resourceId: string }) {
  return (
    <div className="flex items-start gap-2" data-testid="asset-resource-broken">
      <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-amber-500" />
      <div className="min-w-0">
        <p className="text-sm text-amber-600 dark:text-amber-400">Missing file</p>
        <p className="text-xs text-muted-foreground">
          {resourceId} was deleted. This asset renders without it. Remove the reference or
          upload a replacement.
        </p>
      </div>
    </div>
  );
}

// UriLine is the address the content has to name for the reference to render,
// with the copy control that makes it usable.
function UriLine({ uri }: { uri: string }) {
  return (
    <div className="flex items-center gap-1">
      <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 text-[11px]">{uri}</code>
      <CopyButton text={uri} label="Copy URI" />
    </div>
  );
}

function InContentNote({ count }: { count: number }) {
  return (
    <p className="text-xs text-muted-foreground" data-testid="asset-resource-in-content">
      Written in this asset&apos;s content on {count} {count === 1 ? "line" : "lines"}.
    </p>
  );
}

// RemoveWarning names where the content still writes the URI before the
// reference is withdrawn. Removing is allowed anyway: the markup and the
// declaration are two things the author keeps in step, and being unable to
// withdraw a grant until a document had been edited would be worse.
function RemoveWarning({
  refItem,
  contentScanned,
  onCancel,
  onConfirm,
}: {
  refItem: AssetResourceRef;
  contentScanned: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const occurrences = refItem.occurrences ?? [];
  const truncated = occurrences.some((o) => o.truncated);
  return (
    <Alert data-testid="asset-resource-remove-warning">
      <AlertTriangle />
      <AlertDescription className="space-y-2">
        {contentScanned ? (
          <p>
            This asset&apos;s content still writes this URI
            {truncated ? " on at least " : " on "}
            {occurrences.length} {occurrences.length === 1 ? "line" : "lines"}. Removing the
            reference leaves that content pointing at a file the asset can no longer load.
          </p>
        ) : (
          <p data-testid="asset-resource-unchecked-warning">
            This asset&apos;s content could not be checked for this URI. If the content names it,
            removing the reference leaves that part of the asset pointing at a file it can no
            longer load.
          </p>
        )}
        <ul className="space-y-1">
          {occurrences.map((o) => (
            <li key={o.line} className="truncate font-mono text-[11px] text-muted-foreground">
              line {o.line}: {o.snippet}
            </li>
          ))}
        </ul>
        <div className="flex gap-2 pt-1">
          <Button variant="outline" size="xs" onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="destructive" size="xs" onClick={onConfirm}>
            Remove anyway
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  );
}
