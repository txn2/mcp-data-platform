import { useState } from "react";
import { ArrowLeft } from "lucide-react";
import { useCatalogEntity } from "@/api/portal/datahub";
import { DataHubConnectionSelect, useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { useAuthStore } from "@/stores/auth";
import { CatalogList } from "./catalog/CatalogList";
import { EntityBody } from "./catalog/sections";
import { ListSkeleton } from "./catalog/primitives";

/**
 * CatalogTab is the Knowledge > Catalog sub-tab (#719): browse/search DataHub
 * datasets and view/edit their metadata. Editing (description, tags, owners,
 * glossary terms, domain) is shown only when the persona grants datahub_update
 * and the selected connection is write-enabled; the API enforces the same.
 */
export function CatalogTab({ conn, onConnChange }: { conn: string; onConnChange: (c: string) => void }) {
  const [urn, setUrn] = useState<string | null>(null);
  const writable = useConnectionWritable(conn);
  const hasWriteTool = useAuthStore(
    (s) => (s.user?.tools?.includes("datahub_update") ?? false) || s.isAdmin(),
  );
  const canEdit = writable && hasWriteTool;

  return (
    <div className="space-y-4">
      <DataHubConnectionSelect value={conn} onChange={onConnChange} />
      {!conn ? null : urn ? (
        <CatalogEntityDetail conn={conn} urn={urn} canEdit={canEdit} onBack={() => setUrn(null)} />
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
      <button
        onClick={onBack}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Back to catalog
      </button>

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
