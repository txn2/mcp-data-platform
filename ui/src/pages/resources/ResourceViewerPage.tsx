import { useState } from "react";
import { Download, FileQuestion, Pencil, Trash2 } from "lucide-react";
import { useResource } from "@/api/resources/hooks";
import { resourceFetchRaw } from "@/api/resources/client";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";
import { ViewerLayout } from "@/components/viewer/ViewerLayout";
import { useAuthStore } from "@/stores/auth";
import type { Resource } from "@/api/resources/types";
import { DeleteConfirm } from "./modals/DeleteConfirm";
import { EditModal } from "./modals/EditModal";
import { ResourceContent } from "./parts/ResourceContent";
import { ResourceSidebar } from "./parts/ResourceSidebar";
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
  } catch { /* ignore */ }
}

interface Props {
  resourceId: string;
  /** True on the administrator's copy of the section, which is what carries
   * authority over a resource somebody else uploaded. */
  admin?: boolean;
  /** Where the library this was opened from lives. */
  onBack: () => void;
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
export function ResourceViewerPage({ resourceId, admin = false, onBack }: Props) {
  const { data: resource, isLoading } = useResource(resourceId);
  const currentUser = useAuthStore((s) => s.user);
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);

  if (isLoading) {
    return <LoadingIndicator />;
  }

  if (!resource) {
    return (
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
    );
  }

  const ScopeIcon = scopeIcon(resource.scope);
  // Users can only edit/delete their own resources. Admins can edit/delete any.
  const canModify = admin || resource.uploader_sub === currentUser?.user_id;

  return (
    <>
      <ViewerLayout
        onBack={onBack}
        title={resource.display_name}
        subtitle={
          <span className="flex items-center gap-1.5">
            <ScopeIcon className="h-3 w-3" />
            {scopeLabel(resource.scope, resource.scope_id, currentUser)} / {resource.category} /{" "}
            {resource.filename}
          </span>
        }
        actions={
          <div data-testid="resource-detail-actions" className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void downloadResource(resource)}>
              <Download />
              Download
            </Button>
            {canModify && (
              <>
                <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
                  <Pencil />
                  Edit
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setDeleting(true)}
                  className="border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
                >
                  <Trash2 />
                  Delete
                </Button>
              </>
            )}
          </div>
        }
        sidebar={<ResourceSidebar resource={resource} canModify={canModify} />}
        // A resource is what its metadata says it is, and this page replaced a
        // dialog that showed all of it at once; opening with the column closed
        // would hide behind a toggle everything the dialog put in front.
        sidebarInitiallyOpen
      >
        <ResourceContent resource={resource} />
      </ViewerLayout>

      {editing && (
        <EditModal resource={resource} admin={admin} onClose={() => setEditing(false)} />
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
