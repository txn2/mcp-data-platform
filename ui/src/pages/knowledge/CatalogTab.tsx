import { useState } from "react";
import { ArrowLeft } from "lucide-react";
import { useCatalogEntity } from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { useAuthStore } from "@/stores/auth";
import { CatalogList } from "./catalog/CatalogList";
import { EntityBody } from "./catalog/sections";
import { ListSkeleton } from "./catalog/primitives";

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
  const [urn, setUrn] = useState<string | null>(() => urnFromLocation());
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

/** CATALOG_URN_PARAM is the query parameter that deep-links one catalog entity. */
export const CATALOG_URN_PARAM = "urn";

/** urnFromLocation reads the deep-linked entity URN, if any. Only a well-formed
 * `urn:` value is accepted, so nothing else in the query string can be coerced
 * into a catalog lookup. */
function urnFromLocation(): string | null {
  if (typeof window === "undefined") return null;
  const value = new URLSearchParams(window.location.search).get(CATALOG_URN_PARAM);
  return value && value.startsWith("urn:") ? value : null;
}

/** clearURNFromLocation drops the deep link when the reader goes back to the
 * list, so a refresh does not reopen the entity they just left. */
function clearURNFromLocation() {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  if (!url.searchParams.has(CATALOG_URN_PARAM)) return;
  url.searchParams.delete(CATALOG_URN_PARAM);
  window.history.replaceState(window.history.state, "", url.toString());
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
        <ArrowLeft className="h-4 w-4" /> Back to tables
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
