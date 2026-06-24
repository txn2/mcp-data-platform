import { describe, it, expect } from "vitest";
import { extractRefUrns, parseRef, isRefUrn, buildRefUrn, entityHref } from "./entityRefs";

describe("entityHref", () => {
  it("routes asset/collection/prompt to their viewers and nothing else", () => {
    expect(entityHref("asset", "a1")).toBe("/assets/a1");
    expect(entityHref("collection", "c1")).toBe("/collections/c1");
    expect(entityHref("prompt", "p1")).toBe("/prompts/p1");
    expect(entityHref("knowledge_page", "kp1")).toBeNull(); // no URL route
    expect(entityHref("connection", "")).toBeNull();
    expect(entityHref("datahub", "")).toBeNull();
    expect(entityHref("asset", "")).toBeNull(); // no id
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
  it("detects mcp: and urn: hrefs", () => {
    expect(isRefUrn("mcp:asset:a")).toBe(true);
    expect(isRefUrn("urn:li:dataset:(x)")).toBe(true);
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
    expect(
      parseRef("urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)")?.fallbackLabel,
    ).toBe("iceberg.retail.daily_sales");
    expect(parseRef("urn:li:glossaryTerm:revenue")?.fallbackLabel).toBe("revenue");
  });

  it("rejects malformed references", () => {
    expect(parseRef("mcp:")).toBeNull();
    expect(parseRef("mcp:asset:")).toBeNull();
    expect(parseRef("mcp:connection:trino,warehouse")).toBeNull();
    expect(parseRef("not-a-ref")).toBeNull();
  });
});

describe("extractRefUrns", () => {
  it("extracts distinct references from a markdown body", () => {
    const body = `See [a](mcp:asset:asset-001) and
[warehouse](mcp:connection:(trino,warehouse)) plus the dataset
urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)
and <urn:li:glossaryTerm:revenue> and a duplicate [a2](mcp:asset:asset-001).`;
    expect(extractRefUrns(body).sort()).toEqual(
      [
        "mcp:asset:asset-001",
        "mcp:connection:(trino,warehouse)",
        "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)",
        "urn:li:glossaryTerm:revenue",
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
});
