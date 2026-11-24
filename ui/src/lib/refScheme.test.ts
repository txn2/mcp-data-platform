import { afterEach, describe, expect, it } from "vitest";
import { canonRef, displayRef, referenceScheme, refOf, setReferenceScheme } from "./refScheme";
import { assetLinkId, contentAssetLinks } from "./assetLinks";
import { buildRefUrn, catalogKind, columnDataset, extractRefUrns, isRefUrn, parseRef } from "./entityRefs";

afterEach(() => setReferenceScheme(undefined));

describe("refScheme", () => {
  it("is mcp and changes nothing by default", () => {
    expect(referenceScheme()).toBe("mcp");
    expect(canonRef("acme:asset:a")).toBe("acme:asset:a");
    expect(displayRef("mcp:asset:a")).toBe("mcp:asset:a");
    expect(buildRefUrn("asset", "a")).toBe("mcp:asset:a");
  });

  it("writes the deployment's scheme and reads both", () => {
    setReferenceScheme("acme");
    expect(refOf("call", "c1")).toBe("acme:call:c1");
    expect(buildRefUrn("knowledge_page", "p1")).toBe("acme:knowledge_page:p1");
    expect(canonRef(" acme:asset:a ")).toBe("mcp:asset:a");
    for (const ref of ["acme:asset:a1", "mcp:asset:a1"]) {
      expect(isRefUrn(ref)).toBe(true);
      expect(parseRef(ref)?.type).toBe("asset");
      expect(assetLinkId(ref)).toBe("a1");
    }
    expect(catalogKind("acme:tag:pii")).toBe("tag");
    expect(columnDataset("acme:column:(trino,w,a.b.c,x)")).toBe("acme:dataset:(trino,w,a.b.c)");
    expect(extractRefUrns("see acme:asset:a1 and mcp:prompt:p2.")).toEqual(["acme:asset:a1", "mcp:prompt:p2"]);
    expect(contentAssetLinks(`<a href="acme:asset:x1">x</a>`)).toEqual(["x1"]);
    expect(isRefUrn("other:asset:a")).toBe(false);
  });

  it("ignores a scheme of the wrong shape", () => {
    setReferenceScheme("Not A Scheme");
    expect(referenceScheme()).toBe("mcp");
  });
});
