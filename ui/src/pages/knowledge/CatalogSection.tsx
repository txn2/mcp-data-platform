import { useCallback, useState } from "react";
import { DataHubConnectionSelect } from "@/components/knowledge/DataHubConnectionSelect";
import { CatalogTab } from "./CatalogTab";
import { ContextDocsTab } from "./ContextDocsTab";
import { TagsTab } from "./TagsTab";
import { DomainsTab } from "./DomainsTab";
import { GlossaryTab } from "./GlossaryTab";
import { SubTabBar } from "./hub/SubTabBar";
import { catalogSubHash, normalizeCatalogSub, type CatalogSubTab } from "./hubHash";

// DH_CONN_STORAGE_KEY persists the selected connection across a refresh and
// across arriving here from another route (a `?urn=` deep link remounts the
// hub), so the section does not silently reset to the first connection.
const DH_CONN_STORAGE_KEY = "mcp-portal-datahub-conn";

// SUB_TAB_META labels and explains each inner tab. It is a Record, so a new
// CatalogSubTab fails the build until it has both here, rather than rendering an
// unlabelled tab with no explanation.
const SUB_TAB_META: Record<CatalogSubTab, { label: string; description: string }> = {
  tables: {
    label: "Tables",
    description:
      "The tables this connection catalogs, with their metadata: description, tags, owners, glossary terms, domain, and columns. Browse or search, open a table, and edit its metadata when your persona grants datahub_update and the connection is writable. Tables originate in source systems, so this is metadata editing, not table create/delete.",
  },
  context_docs: {
    label: "Context Docs",
    description:
      "Markdown notes attached to a catalog entity: a dataset, glossary term, glossary node, or container. Browse or search, and manage documents with full create, edit, and delete when your persona grants the matching datahub tool and the connection is writable.",
  },
  tags: {
    label: "Tags",
    description:
      "The tag vocabulary itself, rather than the tags carried by one table. Browse or filter this connection's tags, open one to see what it means and which tables carry it, and create, describe, or retire a tag when your persona grants the matching datahub tool and the connection is writable.",
  },
  domains: {
    label: "Domains",
    description:
      "The business areas the catalog is grouped into, rather than the domain carried by one table. Browse or filter this connection's domains, open one to see what it covers and which tables are in it, and create, describe, or retire a domain — and move tables in and out of it — when your persona grants the matching datahub tool and the connection is writable.",
  },
  glossary: {
    label: "Glossary",
    description:
      "The business glossary: the terms this organization defines, in the hierarchy of nodes that organizes them. Walk the tree, open a term to see its definition, where it sits, the notes attached to it, and the tables it is applied to, and create, describe, or retire a term or a node when your persona grants the matching datahub tool and the connection is writable.",
  },
};

// SUB_TABS is the inner tab bar in display order (#1194): the described things
// first, the vocabularies that describe them second.
const SUB_TABS = (["tables", "context_docs", "tags", "domains", "glossary"] as const).map((key) => ({
  key,
  label: SUB_TAB_META[key].label,
}));

// storedConn reads the last selected connection: "" when there is none, and
// when storage is unavailable.
function storedConn(): string {
  try {
    return globalThis.localStorage?.getItem(DH_CONN_STORAGE_KEY) ?? "";
  } catch {
    return "";
  }
}

/**
 * CatalogSection is the Catalog tab of the Knowledge hub (#1194): the container
 * holding every DataHub-backed surface in the portal. Everything under Catalog
 * is DataHub; anything the portal's own database backs stays outside it.
 *
 * It owns what its inner tabs share. The connection is picked once here rather
 * than by each tab, and the body renders only once a connection is selected, so
 * a tab never has to gate on a connection it did not choose. The inner tabs are
 * addressable in the hash (/knowledge/catalog#tags), so the selection survives a
 * refresh and browser back/forward without making them separate routes -- which
 * is what would unmount this container and lose the shared connection.
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
  const [conn, setConnState] = useState(() => storedConn());
  const setConn = useCallback((c: string) => {
    setConnState(c);
    try {
      // Optional-chained: storage is absent under jsdom and blocked in some
      // private modes. The in-memory value still applies either way.
      globalThis.localStorage?.setItem(DH_CONN_STORAGE_KEY, c);
    } catch {
      /* storage unavailable; the selection just does not outlive this mount */
    }
  }, []);

  // Reflect the inner tab in the hash so the view is deep-linkable and survives
  // a refresh, without a navigation that would remount the container. The
  // relative form keeps the path and any `?urn=` deep link intact.
  const selectSub = (next: CatalogSubTab) => {
    setSub(next);
    window.history.replaceState(null, "", `#${catalogSubHash(next)}`);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <SubTabBar tabs={SUB_TABS} active={sub} onSelect={selectSub} dense />
        <DataHubConnectionSelect value={conn} onChange={setConn} />
      </div>
      <p className="text-sm text-muted-foreground">{SUB_TAB_META[sub].description}</p>

      {/* Keying on the connection resets each inner tab's own navigation when
          the connection changes: an open table, document, tag, domain, or
          glossary entity belongs to one connection, and leaving it on screen
          would read its detail from the new one. */}
      {conn && (
        <div key={conn}>
          {sub === "tables" && <CatalogTab conn={conn} />}
          {sub === "context_docs" && <ContextDocsTab conn={conn} />}
          {sub === "tags" && <TagsTab conn={conn} onNavigate={onNavigate} />}
          {sub === "domains" && <DomainsTab conn={conn} onNavigate={onNavigate} />}
          {sub === "glossary" && <GlossaryTab conn={conn} onNavigate={onNavigate} />}
        </div>
      )}
    </div>
  );
}
