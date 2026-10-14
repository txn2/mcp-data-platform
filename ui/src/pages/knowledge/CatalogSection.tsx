import { useState } from "react";
import { useCatalogConnections } from "@/api/portal/catalog";
import { InfoHint } from "@/components/patterns/InfoHint";
import { CatalogTab } from "./CatalogTab";
import { DefinitionsTab } from "./DefinitionsTab";
import { GlossaryTab } from "./GlossaryTab";
import { SubTabBar } from "./hub/SubTabBar";
import { catalogSubHash, normalizeCatalogSub, type CatalogSubTab } from "./hubHash";

// SUB_TAB_META labels and explains each inner tab. It is a Record, so a new
// CatalogSubTab fails the build until it has both here, rather than rendering an
// unlabelled tab with no explanation.
const SUB_TAB_META: Record<CatalogSubTab, { label: string; description: string }> = {
  tables: {
    label: "Tables",
    description:
      "The datasets the platform catalogs from your connections: Trino tables, API operations and GraphQL operations, with their curation. Search or open one to read its description, tags, glossary terms, owners, domain, columns, notes, lineage and history, and edit them when your persona grants apply_knowledge. The catalog scan finds datasets; this is where people describe them.",
  },
  glossary: {
    label: "Glossary",
    description:
      "The business glossary: the terms this organization defines, filed under the nodes that organize them. Open a term to see its definition and the datasets and columns it is applied to, and create, describe or delete terms and nodes when your persona grants apply_knowledge.",
  },
  tags: {
    label: "Tags",
    description:
      "The tags datasets and columns carry. Open one to see what it means and what carries it, and create, describe or delete a tag when your persona grants apply_knowledge.",
  },
  domains: {
    label: "Domains",
    description:
      "The business areas datasets are grouped into. Open one to see what it covers and which datasets are in it, and create, describe or delete a domain when your persona grants apply_knowledge.",
  },
  data_products: {
    label: "Data products",
    description:
      "The data products the organization publishes. Open one to read what it is, and create, describe or delete one when your persona grants apply_knowledge.",
  },
};

// SUB_TABS is the inner tab bar in display order (#1194): the described things
// first, the vocabularies that describe them second.
const SUB_TABS = (["tables", "glossary", "tags", "domains", "data_products"] as const).map((key) => ({
  key,
  label: SUB_TAB_META[key].label,
}));

/**
 * CatalogSection is the Catalog tab of the Knowledge hub (#1194, #1921): the
 * platform's own catalog, its datasets and the definitions they are curated
 * with. The inner tabs are addressable in the hash (/knowledge/catalog#tags),
 * so the selection survives a refresh and browser back/forward without making
 * them separate routes.
 */
export function CatalogSection({
  initialSub,
  onNavigate,
}: {
  // The inner tab addressed by the URL hash, if any. Anything unrecognized (and
  // the bare route) opens Tables.
  initialSub?: string;
  onNavigate?: (path: string) => void;
}) {
  const [sub, setSub] = useState<CatalogSubTab>(() => normalizeCatalogSub(initialSub));
  const canEdit = useCatalogConnections().data?.writable ?? false;

  // Reflect the inner tab in the hash so the view is deep-linkable and survives
  // a refresh, without a navigation that would remount the container. Changing
  // tab drops an open entity's ?ref=, which belongs to the tab being left.
  const selectSub = (next: CatalogSubTab) => {
    setSub(next);
    const url = new URL(window.location.href);
    url.searchParams.delete("ref");
    url.hash = catalogSubHash(next);
    window.history.replaceState(null, "", url.toString());
  };

  return (
    // The Catalog surface is one bordered panel: its inner tab bar sits in the
    // panel's header strip, so the third level of navigation visibly belongs to
    // this surface instead of stacking as another free-floating bar.
    <div className="rounded-xl border bg-card shadow-sm">
      <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2">
        <SubTabBar tabs={SUB_TABS} active={sub} onSelect={selectSub} dense />
        <InfoHint label="About this catalog tab">{SUB_TAB_META[sub].description}</InfoHint>
      </div>
      {/* Keyed on the tab, so an entity open on one tab is not carried into
          another that reads the same ?ref= parameter. */}
      <div key={sub} className="p-4">
        {sub === "tables" && <CatalogTab onNavigate={onNavigate} />}
        {sub === "glossary" && <GlossaryTab canEdit={canEdit} onNavigate={onNavigate} />}
        {(sub === "tags" || sub === "domains" || sub === "data_products") && (
          <DefinitionsTab sub={sub} canEdit={canEdit} onNavigate={onNavigate} />
        )}
      </div>
    </div>
  );
}
