import { useState } from "react";
import { CatalogList } from "./catalog/CatalogList";
import { DatasetDetail } from "./catalog/DatasetDetail";
import { clearRefFromLocation, deepLinkedRef, setRefInLocation } from "./catalog/utils";

/**
 * CatalogTab is the Tables tab of the Catalog section (#1921): find a dataset
 * across the connections the caller's persona reaches, open it, and read or
 * curate it. Editing is shown when the persona grants apply_knowledge; the
 * routes enforce the same.
 */
export function CatalogTab({ onNavigate }: { onNavigate?: (path: string) => void }) {
  // The open dataset is URL-addressable (?ref=...), so a catalog reference
  // anywhere in the portal can link straight to it.
  const [ref, setRef] = useState<string | null>(() => deepLinkedRef("tables"));
  const open = (next: string) => {
    setRef(next);
    setRefInLocation(next);
  };
  // A link to another dataset (lineage) opens it in place rather than
  // navigating, which would remount the section.
  const navigate = (path: string) => {
    const target = new URL(path, window.location.origin).searchParams.get("ref");
    if (target && path.endsWith("#tables")) open(target);
    else onNavigate?.(path);
  };

  return ref ? (
    <DatasetDetail
      key={ref}
      reference={ref}
      onNavigate={navigate}
      onBack={() => {
        setRef(null);
        clearRefFromLocation();
      }}
    />
  ) : (
    <CatalogList onOpen={open} />
  );
}
