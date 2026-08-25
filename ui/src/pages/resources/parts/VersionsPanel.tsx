import { useRef, useState } from "react";
import { History, RotateCcw, Download, Upload, Loader2 } from "lucide-react";
import { useResourceVersions, useReplaceContent, useRestoreVersion } from "@/api/resources/hooks";
import { resourceFetchRaw } from "@/api/resources/client";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import type { Resource, ResourceVersion } from "@/api/resources/types";

// downloadVersion pulls one revision's bytes and hands them to the browser
// under the resource's filename, which every version shares.
async function downloadVersion(resource: Resource, version: number) {
  const res = await resourceFetchRaw(`/${resource.id}/versions/${version}/content`);
  if (!res.ok) {
    return;
  }
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = url;
  a.download = `v${version}-${resource.filename}`;
  a.click();
  URL.revokeObjectURL(url);
}

// VersionRow renders one revision: who made it, when, how big it was, the
// actions available on it, and, beneath, why the content changed when the
// revision was written on the uploader's behalf. The current revision has no
// restore action — it is already what the resource serves.
//
// Two lines rather than one. The panel sits in the viewer's sidebar (#1470),
// which is narrower than the dialog this trail used to be drawn in; on one line
// the uploader, the timestamp and the size pushed the download and restore
// actions past the column's edge, where nothing could reach them.
function VersionRow({
  resource,
  version: v,
  isCurrent,
  canModify,
  busy,
  onRestore,
}: {
  resource: Resource;
  version: ResourceVersion;
  isCurrent: boolean;
  canModify: boolean;
  busy: boolean;
  onRestore: (version: number) => void;
}) {
  return (
    <li data-testid={`resource-version-${v.version}`} className="text-xs text-muted-foreground">
      <div className="flex items-center gap-2">
        <span className="shrink-0 font-medium text-foreground">v{v.version}</span>
        {isCurrent && (
          <Badge variant="success" className="px-1.5">
            current
          </Badge>
        )}
        {v.restored_from !== undefined && (
          <Badge variant="muted" className="rounded px-1.5">
            restored v{v.restored_from}
          </Badge>
        )}
        <span className="ml-auto flex shrink-0 items-center">
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => void downloadVersion(resource, v.version)}
            title={`Download v${v.version}`}
            aria-label={`Download v${v.version}`}
            data-testid={`download-version-${v.version}`}
          >
            <Download />
          </Button>
          {canModify && !isCurrent && (
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={() => onRestore(v.version)}
              disabled={busy}
              title={`Restore v${v.version}`}
              aria-label={`Restore v${v.version}`}
              data-testid={`restore-version-${v.version}`}
            >
              <RotateCcw />
            </Button>
          )}
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-x-2">
        <span className="max-w-full truncate">{v.uploader_email || v.uploader_sub}</span>
        <span>{new Date(v.created_at).toLocaleString()}</span>
        <span className="tabular-nums">{formatBytes(v.size_bytes)}</span>
      </div>
      {v.change_summary && (
        <p className="italic" data-testid={`resource-version-summary-${v.version}`}>
          {v.change_summary}
        </p>
      )}
    </li>
  );
}

// VersionsTitle names the panel and states how much of the retention budget
// the trail occupies, so a reader knows what has already aged out.
function VersionsTitle({ kept, maxVersions }: { kept: number; maxVersions: number | undefined }) {
  return (
    <span className="flex flex-wrap items-center gap-x-1.5">
      <span className="flex items-center gap-1.5 whitespace-nowrap">
        <History className="h-3 w-3 text-muted-foreground" />
        Version history
      </span>
      {kept > 0 && (
        <span className="font-normal whitespace-nowrap text-muted-foreground">
          ({kept} of {maxVersions} kept)
        </span>
      )}
    </span>
  );
}

// VersionList renders the trail, or the state standing in for it while it
// loads and when a resource has none recorded.
function VersionList({
  resource,
  versions,
  current,
  canModify,
  busy,
  isLoading,
  onRestore,
}: {
  resource: Resource;
  versions: ResourceVersion[];
  current: number;
  canModify: boolean;
  busy: boolean;
  isLoading: boolean;
  onRestore: (version: number) => void;
}) {
  if (isLoading) {
    return <p className="text-xs text-muted-foreground">Loading versions...</p>;
  }
  if (versions.length === 0) {
    return <p className="text-xs text-muted-foreground">No revisions recorded yet.</p>;
  }
  return (
    <ul className="space-y-2.5">
      {versions.map((v) => (
        <VersionRow
          key={v.version}
          resource={resource}
          version={v}
          isCurrent={v.version === current}
          canModify={canModify}
          busy={busy}
          onRestore={onRestore}
        />
      ))}
    </ul>
  );
}

// useVersionActions owns the two mutations the panel offers and the message a
// failed one leaves behind, so the panel body stays a rendering concern.
function useVersionActions(resourceId: string) {
  const replace = useReplaceContent();
  const restore = useRestoreVersion();
  const [error, setError] = useState("");

  const replaceContent = async (file: File | undefined) => {
    if (!file) {
      return;
    }
    setError("");
    try {
      await replace.mutateAsync({ id: resourceId, file });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Replacing content failed");
    }
  };

  const restoreVersion = async (version: number) => {
    setError("");
    try {
      await restore.mutateAsync({ id: resourceId, version });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Restoring the version failed");
    }
  };

  return {
    replaceContent,
    restoreVersion,
    error,
    replacing: replace.isPending,
    busy: replace.isPending || restore.isPending,
  };
}

// ReplaceContentButton is the file picker that adds the next revision.
function ReplaceContentButton({
  busy,
  replacing,
  onPick,
}: {
  busy: boolean;
  replacing: boolean;
  onPick: (file: File | undefined) => void;
}) {
  const fileInput = useRef<HTMLInputElement>(null);
  return (
    <>
      <input
        ref={fileInput}
        type="file"
        className="hidden"
        data-testid="replace-content-input"
        onChange={(e) => onPick(e.target.files?.[0])}
      />
      <Button
        variant="outline"
        size="xs"
        onClick={() => fileInput.current?.click()}
        disabled={busy}
        data-testid="replace-content-button"
      >
        {replacing ? <Loader2 className="animate-spin" /> : <Upload />}
        Replace content
      </Button>
    </>
  );
}

// VersionsPanel is the content-revision surface of a resource: the trail of
// revisions with who made each and when, a download per revision, a restore,
// and the replacement upload that adds the next one.
//
// Replacing content keeps the resource's ID, URI, and filename, which is the
// point: every mcp:resource:<id> citation and prompt attachment pointing at it
// keeps resolving, where delete-plus-re-upload would break all of them. The
// panel says so, because the uploaded file's own name being ignored is
// otherwise surprising.
export function VersionsPanel({ resource, canModify }: { resource: Resource; canModify: boolean }) {
  const { data, isLoading, isError } = useResourceVersions(resource.id);
  const { replaceContent, restoreVersion, error, replacing, busy } = useVersionActions(resource.id);

  // The server answers 503 when a deployment has no version store or no blob
  // storage; there is nothing to show and nothing the viewer can do about it.
  if (isError || (!isLoading && !data)) {
    return null;
  }

  const versions = data?.versions ?? [];
  const current = data?.current ?? 0;

  return (
    <SectionCard
      data-testid="resource-versions"
      title={<VersionsTitle kept={versions.length} maxVersions={data?.max_versions} />}
      action={
        canModify && (
          <ReplaceContentButton
            busy={busy}
            replacing={replacing}
            onPick={(file) => void replaceContent(file)}
          />
        )
      }
    >
      <VersionList
        resource={resource}
        versions={versions}
        current={current}
        canModify={canModify}
        busy={busy}
        isLoading={isLoading}
        onRestore={(version) => void restoreVersion(version)}
      />

      <VersionsFooter canModify={canModify} error={error} />
    </SectionCard>
  );
}

// VersionsFooter explains what revising does to a resource's identity — the
// part a reader cannot infer from the buttons — and reports a failed action.
function VersionsFooter({ canModify, error }: { canModify: boolean; error: string }) {
  return (
    <>
      {canModify && (
        <p className="mt-2 text-xs text-muted-foreground">
          Replacing content keeps this resource&apos;s link and file name, so references and prompt
          attachments keep working. Restoring adds the older content back as a new version.
        </p>
      )}
      {error && (
        <p className="mt-2 text-xs text-destructive" data-testid="resource-versions-error">
          {error}
        </p>
      )}
    </>
  );
}
