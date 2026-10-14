import { describe, it, expect } from "vitest";
import {
  buildRefUrn,
  catalogHref,
  catalogSubForRef,
  columnDataset,
  columnRef,
  quoteRefPart,
  entityHref,
  extractRefUrns,
  isCatalogRefType,
  isRefUrn,
  parseRef,
  refHref,
  trimRefToken,
} from "./entityRefs";

describe("entityHref", () => {
  it("routes asset/collection/prompt/knowledge_page to their views and nothing else", () => {
    expect(entityHref("asset", "a1")).toBe("/assets/a1");
    expect(entityHref("collection", "c1")).toBe("/collections/c1");
    expect(entityHref("prompt", "p1")).toBe("/prompts/p1");
    expect(entityHref("knowledge_page", "kp1")).toBe("/knowledge/pages/kp1"); // #709
    expect(entityHref("connection", "")).toBeNull();
    expect(entityHref("catalog", "")).toBeNull();
    expect(entityHref("asset", "")).toBeNull(); // no id
  });

  it("routes a cited script to its script page (#1855)", () => {
    const id = "6f1c0a52-8d8e-4f7b-9a3e-2b8c1d0e4f55";
    expect(entityHref("script", id)).toBe(`/automations/${id}`);
    expect(entityHref("script", "../x")).toBeNull();
    const parsed = parseRef(`mcp:script:${id}`);
    expect(parsed).toEqual({ urn: `mcp:script:${id}`, type: "script", id, fallbackLabel: id });
    expect(refHref(parsed!.type, parsed!.id, parsed!.urn)).toBe(`/automations/${id}`);
    expect(extractRefUrns(`Kept in sync by [the backfill](mcp:script:${id}).`)).toEqual([`mcp:script:${id}`]);
  });

  it("refuses an unsafe id so a crafted href cannot path-traverse", () => {
    expect(entityHref("asset", "../../admin")).toBeNull();
    expect(entityHref("asset", "a/b")).toBeNull();
    expect(entityHref("collection", "x?y")).toBeNull();
  });
});

describe("buildRefUrn", () => {
  it("serializes a single-id reference and round-trips through parseRef", () => {
    expect(buildRefUrn("asset", "asset-1")).toBe("mcp:asset:asset-1");
    expect(buildRefUrn("knowledge_page", "kp-2")).toBe("mcp:knowledge_page:kp-2");
    expect(parseRef(buildRefUrn("collection", "c-3"))?.type).toBe("collection");
  });
});

describe("isRefUrn", () => {
  it("detects mcp: hrefs and nothing else", () => {
    expect(isRefUrn("mcp:asset:a")).toBe(true);
    expect(isRefUrn("urn:li:dataset:(x)")).toBe(false);
    expect(isRefUrn("https://example.com")).toBe(false);
    expect(isRefUrn(undefined)).toBe(false);
  });
});

describe("parseRef", () => {
  it("parses each reference type with a fallback label", () => {
    expect(parseRef("mcp:asset:asset-001")).toEqual({
      urn: "mcp:asset:asset-001",
      type: "asset",
      id: "asset-001",
      fallbackLabel: "asset-001",
    });
    expect(parseRef("mcp:connection:(trino,warehouse)")).toEqual({
      urn: "mcp:connection:(trino,warehouse)",
      type: "connection",
      id: "",
      fallbackLabel: "warehouse (trino)",
    });
  });

  it("parses a platform catalog reference with the table, column or key as its label", () => {
    expect(parseRef("mcp:dataset:(trino,acme,hive.sales.orders)")).toEqual({
      urn: "mcp:dataset:(trino,acme,hive.sales.orders)",
      type: "catalog",
      id: "",
      fallbackLabel: "orders",
    });
    expect(parseRef("mcp:column:(trino,acme,hive.sales.orders,amount)")?.fallbackLabel).toBe("orders.amount");
    expect(parseRef("mcp:glossary_term:net-revenue")?.fallbackLabel).toBe("net-revenue");
    expect(parseRef("mcp:tag:pii")?.type).toBe("catalog");
    expect(parseRef('mcp:schema_document:(api,billing,"openapi.json")')?.fallbackLabel).toBe("billing openapi.json");
    expect(refHref("catalog", "", "mcp:tag:pii")).toBe("/knowledge/catalog?ref=mcp%3Atag%3Apii#tags");
  });

  it("rejects malformed references", () => {
    expect(parseRef("mcp:")).toBeNull();
    expect(parseRef("mcp:asset:")).toBeNull();
    expect(parseRef("mcp:connection:trino,warehouse")).toBeNull();
    expect(parseRef("not-a-ref")).toBeNull();
    expect(parseRef("urn:li:glossaryTerm:revenue")).toBeNull();
  });
});

describe("extractRefUrns", () => {
  it("extracts distinct references from a markdown body", () => {
    const body = `See [a](mcp:asset:asset-001) and
[warehouse](mcp:connection:(trino,warehouse)) plus the dataset
mcp:dataset:(trino,acme,iceberg.retail.daily_sales)
and <mcp:glossary_term:revenue> and a duplicate [a2](mcp:asset:asset-001).`;
    expect(extractRefUrns(body).sort()).toEqual(
      [
        "mcp:asset:asset-001",
        "mcp:connection:(trino,warehouse)",
        "mcp:dataset:(trino,acme,iceberg.retail.daily_sales)",
        "mcp:glossary_term:revenue",
      ].sort(),
    );
  });

  it("returns nothing for plain prose", () => {
    expect(extractRefUrns("just words, no references")).toEqual([]);
  });

  it("ignores references inside code blocks and spans", () => {
    const body =
      "Real [a](mcp:asset:real-1).\n\n```\nmcp:asset:in-fence\n```\n\nInline `mcp:asset:in-code`.";
    expect(extractRefUrns(body)).toEqual(["mcp:asset:real-1"]);
  });

  it("trims trailing sentence punctuation from inline prose tokens (#704)", () => {
    // Mirrors the server-side trim so the resolved-ref map keys match what the
    // chip url will be; a bare token before a period must not absorb it.
    const body =
      "The fact is in mcp:asset:asset-001. Also tagged mcp:tag:revenue, plus mcp:asset:a.b.c!";
    expect(extractRefUrns(body).sort()).toEqual(
      ["mcp:asset:a.b.c", "mcp:asset:asset-001", "mcp:tag:revenue"].sort(),
    );
  });
});

describe("trimRefToken", () => {
  it("strips a trailing run of sentence punctuation only", () => {
    expect(trimRefToken("mcp:asset:asset-001.")).toBe("mcp:asset:asset-001");
    expect(trimRefToken("mcp:tag:revenue?!")).toBe("mcp:tag:revenue");
    expect(trimRefToken("mcp:asset:a.b.c.")).toBe("mcp:asset:a.b.c"); // internal dots kept
    expect(trimRefToken("mcp:connection:(trino,acme)")).toBe("mcp:connection:(trino,acme)"); // no-op
  });
});

describe("catalogHref", () => {
  it("deep-links a dataset by its whole reference", () => {
    const ref = "mcp:dataset:(trino,acme,iceberg.retail.daily_sales)";
    expect(catalogHref(ref)).toBe(`/knowledge/catalog?ref=${encodeURIComponent(ref)}#tables`);
  });

  it("opens a column on its dataset", () => {
    const col = "mcp:column:(trino,acme,iceberg.retail.daily_sales,amount)";
    const ds = "mcp:dataset:(trino,acme,iceberg.retail.daily_sales)";
    expect(columnDataset(col)).toBe(ds);
    expect(catalogHref(col)).toBe(`/knowledge/catalog?ref=${encodeURIComponent(ds)}#tables`);
  });

  it("opens a column whose name is quoted on its dataset", () => {
    const ds = "mcp:dataset:(trino,acme,a.b.c)";
    expect(columnRef(ds, "amount")).toBe("mcp:column:(trino,acme,a.b.c,amount)");
    expect(columnRef(ds, 'odd,"name')).toBe('mcp:column:(trino,acme,a.b.c,"odd,""name")');
    expect(columnDataset(columnRef(ds, 'odd,"name'))).toBe(ds);
    expect(quoteRefPart(" padded")).toBe('" padded"');
  });

  it("refuses anything that is not a catalog reference", () => {
    // The value lands in a query string that drives a catalog lookup, so only a
    // real catalog reference may be interpolated into it.
    expect(catalogHref("mcp:asset:a1")).toBeNull();
    expect(catalogHref("urn:li:dataset:x")).toBeNull();
    expect(catalogHref("")).toBeNull();
    expect(catalogHref("../../admin")).toBeNull();
  });

  it("tolerates surrounding whitespace", () => {
    expect(catalogHref("  mcp:tag:pii  ")).toBe("/knowledge/catalog?ref=mcp%3Atag%3Apii#tags");
  });

  // A definition is managed on its own Catalog tab, and Tables cannot show
  // one: a term link that landed on Tables would read a dataset that is not
  // there.
  it("opens each definition on the tab that manages it", () => {
    const cases: [string, string, string][] = [
      ["mcp:glossary_term:revenue", "glossary", "glossary"],
      ["mcp:glossary_node:finance", "glossary", "glossary"],
      ["mcp:tag:pii", "tags", "tags"],
      ["mcp:domain:sales", "domains", "domains"],
      ["mcp:data_product:orders", "data_products", "data-products"],
    ];
    for (const [ref, sub, hash] of cases) {
      expect(catalogSubForRef(ref)).toBe(sub);
      expect(catalogHref(ref)).toBe(`/knowledge/catalog?ref=${encodeURIComponent(ref)}#${hash}`);
    }
  });

  it("sends everything else to Tables", () => {
    expect(catalogSubForRef("mcp:dataset:(trino,acme,a.b.c)")).toBe("tables");
    expect(catalogSubForRef('mcp:schema_document:(api,billing,"openapi.json")')).toBe("tables");
    expect(catalogSubForRef("not-a-ref")).toBe("tables");
  });
});

describe("isCatalogRefType", () => {
  it("separates the catalog vocabularies from the portal's own entities", () => {
    // The distinction drives the picker: a catalog type is searched in the
    // catalog, which returns its reference.
    expect(isCatalogRefType("glossary_term")).toBe(true);
    expect(isCatalogRefType("tag")).toBe(true);
    expect(isCatalogRefType("domain")).toBe(true);
    expect(isCatalogRefType("asset")).toBe(false);
    expect(isCatalogRefType("knowledge_page")).toBe(false);
  });
});

describe("refHref", () => {
  it("sends a catalog reference to the catalog tab", () => {
    const ref = "mcp:dataset:(trino,acme,sales.orders)";
    expect(refHref("catalog", "", ref)).toBe(`/knowledge/catalog?ref=${encodeURIComponent(ref)}#tables`);
  });

  it("sends an internal reference to its id-based route", () => {
    expect(refHref("asset", "a1", "mcp:asset:a1")).toBe("/assets/a1");
    expect(refHref("knowledge_page", "kp1", "mcp:knowledge_page:kp1")).toBe("/knowledge/pages/kp1");
  });

  it("has no destination for a connection", () => {
    // Connections have no per-instance portal page, so the reference is drawn
    // and labelled but never presented as a link.
    expect(refHref("connection", "", "mcp:connection:(trino,wh)")).toBeNull();
  });
});
