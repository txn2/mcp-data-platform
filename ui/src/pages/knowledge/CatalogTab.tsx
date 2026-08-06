import { useState } from "react";
import { useCatalogEntity } from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { useAuthStore } from "@/stores/auth";
import { CatalogList } from "./catalog/CatalogList";
import { EntityBody } from "./catalog/sections";
import { BackToList } from "./catalog/governance";
import { ListSkeleton } from "./catalog/primitives";
import { clearURNFromLocation, deepLinkedURN } from "./catalog/utils";

/**
 * CatalogTab is the Tables tab of the Catalog section (#719, #1194):
 * browse/search the catalog's tables and view/edit their metadata. Editing
 * (description, tags, owners, glossary terms, domain) is shown only when the
 * persona grants datahub_update and the selected connection is write-enabled;
 * the API enforces the same. The connection is chosen by CatalogSection, which
 * renders this only once one is selected.
 */
export function CatalogTab({ conn }: { conn: string }) {
  // The open entity is URL-addressable (?urn=...), so a catalog reference
  // anywhere in the portal — a knowledge-page chip, a node in the knowledge
  // graph — can link straight to it. Held in state as well so opening one from
  // the list does not depend on a router.
  const [urn, setUrn] = useState<string | null>(() => deepLinkedURN("tables"));
  const writable = useConnectionWritable(conn);
  const hasWriteTool = useAuthStore(
    (s) => (s.user?.tools?.includes("datahub_update") ?? false) || s.isAdmin(),
  );
  const canEdit = writable && hasWriteTool;

  return (
    <div className="space-y-4">
      {urn ? (
        <CatalogEntityDetail
          conn={conn}
          urn={urn}
          canEdit={canEdit}
          onBack={() => {
            setUrn(null);
            clearURNFromLocation();
          }}
        />
      ) : (
        <CatalogList conn={conn} onOpen={setUrn} />
      )}
    </div>
  );
}

function CatalogEntityDetail({
  conn,
  urn,
  canEdit,
  onBack,
}: {
  conn: string;
  urn: string;
  canEdit: boolean;
  onBack: () => void;
}) {
  const { data, isLoading, isError } = useCatalogEntity(conn, urn);

  return (
    <div className="space-y-4">
      <BackToList label="Back to tables" onBack={onBack} />

      {isError || !data ? (
        isLoading ? (
          <ListSkeleton />
        ) : (
          <p className="text-sm text-destructive">Failed to load this entity.</p>
        )
      ) : (
        <EntityBody conn={conn} entity={data} canEdit={canEdit} />
      )}
    </div>
  );
}
