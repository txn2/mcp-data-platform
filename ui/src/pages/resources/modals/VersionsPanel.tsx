import { useRef, useState } from "react";
import { History, RotateCcw, Download, Upload, Loader2 } from "lucide-react";
import { useResourceVersions, useReplaceContent, useRestoreVersion } from "@/api/resources/hooks";
import { resourceFetchRaw } from "@/api/resources/client";
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

// VersionRow renders one revision: who made it, when, how big it was, and the
// actions available on it. The current revision has no restore action — it is
// already what the resource serves.
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
    <li
      data-testid={`resource-version-${v.version}`}
      className="flex items-center gap-2 text-xs text-muted-foreground"
    >
      <span className="w-8 shrink-0 font-medium text-foreground">v{v.version}</span>
      {isCurrent && (
        <span className="shrink-0 rounded-full bg-emerald-100 px-1.5 py-0.5 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300">
          current
        </span>
      )}
      {v.restored_from !== undefined && (
        <span className="shrink-0 rounded bg-muted px-1.5 py-0.5">restored v{v.restored_from}</span>
      )}
      <span className="truncate">{v.uploader_email || v.uploader_sub}</span>
      <span className="shrink-0">{new Date(v.created_at).toLocaleString()}</span>
      <span className="shrink-0 tabular-nums">{formatBytes(v.size_bytes)}</span>
      <button
        onClick={() => void downloadVersion(resource, v.version)}
        title={`Download v${v.version}`}
        data-testid={`download-version-${v.version}`}
        className="ml-auto shrink-0 rounded p-1 hover:bg-muted hover:text-foreground"
      >
        <Download className="h-3 w-3" />
      </button>
      {canModify && !isCurrent && (
        <button
          onClick={() => onRestore(v.version)}
          disabled={busy}
          title={`Restore v${v.version}`}
          data-testid={`restore-version-${v.version}`}
          className="shrink-0 rounded p-1 hover:bg-muted hover:text-foreground disabled:opacity-50"
        >
          <RotateCcw className="h-3 w-3" />
        </button>
      )}
    </li>
  );
}

// VersionsHeader titles the panel with how much of the retention budget the
// trail occupies, and carries the replacement upload for a caller who may
// revise.
function VersionsHeader({
  kept,
  maxVersions,
  canModify,
  busy,
  replacing,
  onPick,
}: {
  kept: number;
  maxVersions: number | undefined;
  canModify: boolean;
  busy: boolean;
  replacing: boolean;
  onPick: (file: File | undefined) => void;
}) {
  return (
    <div className="flex items-center justify-between gap-2">
      <p className="flex items-center gap-1.5 text-xs font-medium">
        <History className="h-3 w-3 text-muted-foreground" />
        Version history
        {kept > 0 && (
          <span className="text-muted-foreground">
            ({kept} of {maxVersions} kept)
          </span>
        )}
      </p>
      {canModify && <ReplaceContentButton busy={busy} replacing={replacing} onPick={onPick} />}
    </div>
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
    return <p className="mt-2 text-xs text-muted-foreground">Loading versions...</p>;
  }
  if (versions.length === 0) {
    return <p className="mt-2 text-xs text-muted-foreground">No revisions recorded yet.</p>;
  }
  return (
    <ul className="mt-2 space-y-1">
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
      <button
        onClick={() => fileInput.current?.click()}
        disabled={busy}
        data-testid="replace-content-button"
        className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50 transition-colors"
      >
        {replacing ? <Loader2 className="h-3 w-3 animate-spin" /> : <Upload className="h-3 w-3" />}
        Replace content
      </button>
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
    <div className="rounded-md border bg-muted/30 p-3" data-testid="resource-versions">
      <VersionsHeader
        kept={versions.length}
        maxVersions={data?.max_versions}
        canModify={canModify}
        busy={busy}
        replacing={replacing}
        onPick={(file) => void replaceContent(file)}
      />

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
    </div>
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
