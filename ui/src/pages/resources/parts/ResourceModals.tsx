import { useState } from "react";
import type { Resource } from "@/api/resources/types";
import type { ScopeTarget } from "../scopes";
import { UploadModal } from "../modals/UploadModal";
import { DetailModal } from "../modals/DetailModal";
import { EditModal } from "../modals/EditModal";
import { DeleteConfirm } from "../modals/DeleteConfirm";

// useResourceModals owns which modal is open over the library. Detail hands off
// to edit and delete, so the three are one piece of state rather than three
// that have to be closed in the right order.
export function useResourceModals() {
  const [uploading, setUploading] = useState(false);
  const [detail, setDetail] = useState<Resource | null>(null);
  const [editing, setEditing] = useState<Resource | null>(null);
  const [deleting, setDeleting] = useState<Resource | null>(null);
  return { uploading, setUploading, detail, setDetail, editing, setEditing, deleting, setDeleting };
}

export type ResourceModalState = ReturnType<typeof useResourceModals>;

// ResourceModals is the library's modal stack: upload, the detail read, and the
// edit and delete it leads to.
export function ResourceModals({
  state,
  admin,
  personaNames,
  destination,
}: {
  state: ResourceModalState;
  admin: boolean;
  personaNames: string[];
  // The library the scope tab in view names, which is where an upload from
  // this page lands when the caller is given no choice. Null on the admin
  // "all" tab, where the dialog's own scope picker chooses.
  destination: ScopeTarget | null;
}) {
  const { uploading, setUploading, detail, setDetail, editing, setEditing, deleting, setDeleting } =
    state;
  return (
    <>
      {uploading && (
        <UploadModal
          onClose={() => setUploading(false)}
          admin={admin}
          personaNames={personaNames}
          destination={destination}
        />
      )}

      {detail && (
        <DetailModal
          resource={detail}
          admin={admin}
          onClose={() => setDetail(null)}
          onEdit={() => {
            setEditing(detail);
            setDetail(null);
          }}
          onDelete={() => {
            setDeleting(detail);
            setDetail(null);
          }}
        />
      )}

      {editing && <EditModal resource={editing} onClose={() => setEditing(null)} />}

      {deleting && <DeleteConfirm resource={deleting} onClose={() => setDeleting(null)} />}
    </>
  );
}
