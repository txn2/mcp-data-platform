import { useState } from "react";
import { Download, FileQuestion, Pencil, Share2, Trash2 } from "lucide-react";
import { useResource } from "@/api/resources/hooks";
import { useSharedResources } from "@/api/resources/shares";
import { NoAccess } from "@/components/access/RequestAccessPanel";
import { ShareDialog } from "@/components/ShareDialog";
import { usePortalAccess } from "@/hooks/usePortalAccess";
import { resourceFetchRaw } from "@/api/resources/client";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";
import { ViewerLayout } from "@/components/viewer/ViewerLayout";
import { useAuthStore } from "@/stores/auth";
import type { Resource } from "@/api/resources/types";
import { DeleteConfirm } from "./modals/DeleteConfirm";
import { EditModal } from "./modals/EditModal";
import { FolderBreadcrumbs } from "./parts/FolderBreadcrumbs";
import { libraryTabFor } from "./parts/libraryUrl";
import { ResourceContent } from "./parts/ResourceContent";
import { ResourceSidebar } from "./parts/ResourceSidebar";
import { canWriteScope } from "./scopes";
import { scopeIcon, scopeLabel } from "./shared";

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
  } catch {
    /* ignore */
  }
}

interface Props {
  resourceId: string;
  /** Where the library this was opened from lives. */
  onBack: () => void;
  /**
   * Opens one level of the file's own folder path in the library it lives in.
   * Absent leaves the trail as plain text, which is what a surface with nowhere
   * to send the reader shows.
   */
  onOpenFolder?: (tab: string, path: string) => void;
  /**
   * Opens another page in this surface, for the links the sidebar carries:
   * today, the script or the session that wrote this file (#1569). Absent
   * leaves a producer named without being linked.
   */
  onNavigate?: (path: string) => void;
  /** Where a script that wrote this file opens, per surface. */
  scriptPath?: (scriptId: string) => string;
  /** Where a session that wrote this file opens, per surface. */
  sessionPath?: (sessionId: string) => string;
}

/**
 * One managed resource, at a route of its own (#1470).
 *
 * A resource is the same kind of object as a portal asset -- a stored file with
 * a content type, a version trail and a table registration -- and now opens the
 * same way: the content at the width of the page, everything else beside it,
 * and an address that can be linked, bookmarked and reloaded. It used to open
 * in a 32rem dialog with its preview capped at half the viewport.
 *
 * Editing and deleting stay dialogs. They are bounded forms, which is the shape
 * ModalShell is for.
 */
export function ResourceViewerPage({
  resourceId,
  onBack,
  onOpenFolder,
  onNavigate,
  scriptPath,
  sessionPath,
}: Props) {
  const { data: resource, isLoading } = useResource(resourceId);
  const currentUser = useAuthStore((s) => s.user);
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [sharing, setSharing] = useState(false);
  const { canModify, canEdit, showShare } = useFileRights(resource, !isLoading);

  if (isLoading) {
    return <LoadingIndicator />;
  }

  if (!resource) {
    return <Unopened resourceId={resourceId} onBack={onBack} />;
  }

  const ScopeIcon = scopeIcon(resource.scope);

  return (
    <>
      <ViewerLayout
        onBack={onBack}
        title={resource.display_name}
        subtitle={
          // The same trail the library's own header renders, with every folder
          // in it clickable. It used to be three words of plain text, and the
          // middle one came from a column that could disagree with the URI
          // printed beside it in the Details panel (#1528).
          <span className="flex min-w-0 items-center gap-1.5">
            <ScopeIcon className="h-3 w-3 shrink-0" />
            <FolderBreadcrumbs
              library={scopeLabel(
                resource.scope,
                resource.scope_id,
                currentUser,
              )}
              path={resource.path}
              onOpen={
                onOpenFolder &&
                ((path) =>
                  onOpenFolder(
                    libraryTabFor(resource.scope, resource.scope_id),
                    path,
                  ))
              }
            />
            <span className="shrink-0 text-muted-foreground">/</span>
            <span className="truncate">{resource.filename}</span>
          </span>
        }
        actions={
          <ResourceActions
            resource={resource}
            showShare={showShare}
            canEdit={canEdit}
            canModify={canModify}
            onShare={() => setSharing(true)}
            onEdit={() => setEditing(true)}
            onDelete={() => setDeleting(true)}
          />
        }
        sidebar={
          <ResourceSidebar
            resource={resource}
            canModify={canModify}
            onNavigate={onNavigate}
            scriptPath={scriptPath}
            sessionPath={sessionPath}
          />
        }
        // A resource is what its metadata says it is, and this page replaced a
        // dialog that showed all of it at once; opening with the column closed
        // would hide behind a toggle everything the dialog put in front.
        sidebarInitiallyOpen
      >
        <ResourceContent resource={resource} canModify={canEdit} />
      </ViewerLayout>

      <ShareDialog target={{ type: "resource", id: resource.id }} open={sharing} onOpenChange={setSharing} />

      {editing && (
        <EditModal resource={resource} onClose={() => setEditing(false)} />
      )}

      {deleting && (
        <DeleteConfirm
          resource={resource}
          onClose={() => setDeleting(false)}
          // The page is addressed by the resource it is showing, so once that
          // is gone there is nothing here to return to.
          onDeleted={onBack}
        />
      )}
    </>
  );
}

/**
 * useFileRights is what the caller may do with a file.
 *
 * canModify is who may edit or delete it, read the way CanModifyResource reads
 * it (pkg/resource/permission.go): whoever uploaded it, and whoever may add to
 * the library it lives in. Which page the viewer was opened from is not part
 * of it -- keying the check on that withheld Edit from a platform
 * administrator looking at a file they did not upload, on a resource the
 * server would have let them rewrite (#1527). An editor share also renames it
 * and writes new versions (canEdit, #2258); moving and deleting stay with
 * canModify. Sharing is offered to a persona that allows it and to a person of
 * an external persona, who shares their own upload back; the server decides.
 */
function useFileRights(resource: Resource | undefined, ready: boolean) {
  const currentUser = useAuthStore((s) => s.user);
  const canShare = useAuthStore((s) => s.canShare());
  const { external } = usePortalAccess();
  const { data: sharedWithMe } = useSharedResources(ready);
  if (!resource) return { canModify: false, canEdit: false, showShare: false };
  const sharedAs = sharedWithMe?.find((s) => s.resource.id === resource.id)?.permission;
  const canModify =
    (resource.uploader_sub !== "" && resource.uploader_sub === currentUser?.user_id) ||
    canWriteScope(currentUser, { scope: resource.scope, scope_id: resource.scope_id });
  return { canModify, canEdit: canModify || sharedAs === "editor", showShare: canModify && (canShare || external) };
}

/**
 * Unopened is the page for a file the caller cannot open: a request to its
 * owner (#2258), or not found when there is no such file.
 */
function Unopened({ resourceId, onBack }: { resourceId: string; onBack: () => void }) {
  return (
    <NoAccess
      type="resource"
      id={resourceId}
      notFound={
        <EmptyState
          icon={FileQuestion}
          data-testid="resource-not-found"
          action={
            <Button variant="outline" size="sm" onClick={onBack}>
              Back
            </Button>
          }
        >
          <p className="font-medium">Resource not found</p>
        </EmptyState>
      }
    />
  );
}

/** ResourceActions is the file page's action row. */
function ResourceActions({
  resource,
  showShare,
  canEdit,
  canModify,
  onShare,
  onEdit,
  onDelete,
}: {
  resource: Resource;
  showShare: boolean;
  canEdit: boolean;
  canModify: boolean;
  onShare: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <div data-testid="resource-detail-actions" className="flex flex-wrap items-center gap-2">
      <Button variant="outline" size="sm" onClick={() => void downloadResource(resource)}>
        <Download />
        Download
      </Button>
      {showShare && (
        <Button variant="outline" size="sm" onClick={onShare}>
          <Share2 />
          Share
        </Button>
      )}
      {canEdit && (
        // Named for what it edits. The content editor is on the content
        // itself, and one control called Edit on a page showing a file is
        // read as the file's (#1775).
        <Button variant="outline" size="sm" onClick={onEdit}>
          <Pencil />
          Edit details
        </Button>
      )}
      {canModify && (
        <Button
          variant="outline"
          size="sm"
          onClick={onDelete}
          className="border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
        >
          <Trash2 />
          Delete
        </Button>
      )}
    </div>
  );
}
