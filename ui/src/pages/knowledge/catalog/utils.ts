import { MIN_SEARCH_LEN, type DefinitionKind } from "@/api/portal/catalog";
import {
  CATALOG_REF_PARAM,
  catalogSubForRef,
  isCatalogRef,
  parseRef,
  type CatalogSubTab,
} from "@/lib/entityRefs";

export const MIN_SEARCH = MIN_SEARCH_LEN;

/** KIND_NAMES names each definition kind in a sentence. */
export const KIND_NAMES: Record<DefinitionKind, string> = {
  glossary_term: "glossary term",
  glossary_node: "glossary node",
  tag: "tag",
  domain: "domain",
  data_product: "data product",
};

/** refLabel names a catalog reference when no display name is known: the
 * table, the column or the definition key it carries. */
export function refLabel(ref: string): string {
  return parseRef(ref)?.fallbackLabel ?? ref;
}

/** refFromLocation reads the deep-linked catalog reference, if any. Only a
 * well-formed catalog reference is accepted, so nothing else in the query
 * string can be coerced into a catalog lookup. */
function refFromLocation(): string | null {
  if (typeof window === "undefined") return null;
  const value = new URLSearchParams(window.location.search).get(CATALOG_REF_PARAM);
  return value && isCatalogRef(value) ? value : null;
}

/**
 * deepLinkedRef returns the deep-linked reference when it is one the given
 * inner tab can open, and null otherwise (#1159).
 *
 * Every Catalog inner tab reads the entity from the same query parameter, and
 * the kind of reference decides which tab shows it, so each tab claims only its
 * own kinds. A glossary term arriving with the Tables hash (a hand-edited or
 * stale link) therefore opens the Tables list rather than a dataset read that
 * cannot succeed.
 */
export function deepLinkedRef(sub: CatalogSubTab): string | null {
  const ref = refFromLocation();
  return ref && catalogSubForRef(ref) === sub ? ref : null;
}

/** clearRefFromLocation drops the deep link when the reader goes back to the
 * list, so a refresh does not reopen the entity they just left. */
export function clearRefFromLocation() {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  if (!url.searchParams.has(CATALOG_REF_PARAM)) return;
  url.searchParams.delete(CATALOG_REF_PARAM);
  window.history.replaceState(window.history.state, "", url.toString());
}

/** setRefInLocation records the open entity in the URL, so a refresh or a
 * copied link reopens it. */
export function setRefInLocation(ref: string) {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  url.searchParams.set(CATALOG_REF_PARAM, ref);
  window.history.replaceState(window.history.state, "", url.toString());
}
