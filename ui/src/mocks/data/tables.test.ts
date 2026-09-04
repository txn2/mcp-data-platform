import { describe, it, expect } from "vitest";
import { mockAssets } from "./assets";
import { mockContent } from "./content";
import { fixtureColumns, inspectFixture, normalizeFixture } from "./csvfixture";
import {
  mockRegisterTable,
  mockScratchTableList,
  mockTableRegistrations,
  tornCSVProblem,
  tornCSVRepairSummary,
  tornCSVSourceID,
} from "./tables";
import { mockResourceContent, mockResources, mockResourceVersions } from "./resources";

// The registered-table fixtures answer for a file the viewer is rendering
// beside them, so what they say has to be true of that file. #1617 was the
// state where none of it was: every registration made through the form
// reported the same three columns whatever file it was over, the fixed
// registrations listed columns their files did not have, one of them was over
// an HTML document, and a ten-row store list with no `address` column was
// refused for 94 torn rows in `address`.

/** sourceBody is the bytes the fixture behind a registration serves. */
function sourceBody(kind: string, id: string): string | undefined {
  return kind === "asset" ? mockContent[id] : mockResourceContent[id];
}

/** registerRequest is a register call as the form sends one. */
function registerRequest(body: Record<string, unknown>): Request {
  return new Request("http://localhost/register", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

describe("the fixed registrations", () => {
  const entries = Object.entries(mockTableRegistrations);

  it("covers more than one source, or this suite proves nothing", () => {
    expect(entries.length).toBeGreaterThan(1);
  });

  it.each(entries)("%s is over a file the fixtures actually hold", (id, registrations) => {
    for (const reg of registrations) {
      expect(sourceBody(reg.source_kind, id), `no fixture body for ${id}`).toBeDefined();
    }
  });

  it.each(entries)("%s is over a CSV, which is the only kind a table can read", (id, regs) => {
    for (const reg of regs) {
      if (reg.source_kind === "asset") {
        const asset = mockAssets.find((a) => a.id === id);
        expect(asset?.content_type).toContain("csv");
        continue;
      }
      const resource = mockResources.resources.find((r) => r.id === id);
      expect(resource?.mime_type).toContain("csv");
    }
  });

  it.each(entries)("%s declares the columns its file's header declares", (id, registrations) => {
    const expected = fixtureColumns(sourceBody(registrations[0]!.source_kind, id) ?? "");
    for (const reg of registrations) {
      expect(reg.columns).toEqual(expected);
    }
  });
});

describe("a registration the file has moved on from", () => {
  it("is over a resource whose version trail holds the version it moved on to", () => {
    const stale = Object.entries(mockTableRegistrations).filter(([, regs]) =>
      regs.some((r) => r.stale && r.source_kind === "resource"),
    );
    expect(stale.length).toBeGreaterThan(0);
    for (const [id] of stale) {
      // Staleness is the head having moved past the registered directory, so a
      // file reported as moved on with no recorded revision is a panel
      // contradicting the version history beside it.
      expect(mockResourceVersions[id]?.length ?? 0).toBeGreaterThan(1);
    }
  });
});

describe("where a registration says it reads from", () => {
  it("names the bucket and directory the file itself is in", () => {
    for (const [id, registrations] of Object.entries(mockTableRegistrations)) {
      const resource = mockResources.resources.find((r) => r.id === id);
      const asset = mockAssets.find((a) => a.id === id);
      const uri = resource?.uri ?? (asset ? `s3://${asset.s3_bucket}/${asset.s3_key}` : undefined);
      if (!uri) continue;
      const bucket = uri.split("/")[2];
      for (const reg of registrations) {
        expect(reg.location.startsWith(`s3://${bucket}/`), `${id}: ${reg.location}`).toBe(true);
      }
    }
  });
});

describe("the cross-source listing", () => {
  it("calls each source what its own page calls it", () => {
    for (const row of mockScratchTableList(new URL("http://localhost/tables")).data) {
      if (row.source.missing) continue;
      const name =
        row.source.kind === "asset"
          ? mockAssets.find((a) => a.id === row.source.id)?.name
          : mockResources.resources.find((r) => r.id === row.source.id)?.display_name;
      expect(row.source.name).toBe(name);
    }
  });
});

describe("the file a registration has to correct first", () => {
  it("actually carries the defect it is refused for", () => {
    const defect = inspectFixture(mockResourceContent[tornCSVSourceID] ?? "");
    expect(defect.rows).toBeGreaterThan(0);
    expect(defect.columns.length).toBeGreaterThan(0);
  });

  it("is refused in the count and the column name the file itself gives", () => {
    const defect = inspectFixture(mockResourceContent[tornCSVSourceID] ?? "");
    expect(tornCSVProblem.detail).toContain(
      `${defect.rows} rows in this file have a line break inside a cell (in ${defect.columns.join(", ")})`,
    );
  });

  it("is described as corrected in the number of rows the correction changes", () => {
    const { rowsRepaired } = normalizeFixture(mockResourceContent[tornCSVSourceID] ?? "");
    expect(tornCSVRepairSummary).toBe(`put ${rowsRepaired} rows back onto one line`);
  });

  it("reports the size of the bytes it serves, here and on its head version", () => {
    const resource = mockResources.resources.find((r) => r.id === tornCSVSourceID);
    const body = mockResourceContent[tornCSVSourceID] ?? "";
    expect(resource?.size_bytes).toBe(new TextEncoder().encode(body).length);
    expect(mockResourceVersions[tornCSVSourceID]?.[0]?.size_bytes).toBe(resource?.size_bytes);
  });
});

describe("registering through the form", () => {
  it("answers with the columns of the file registered, not of some other one", async () => {
    const result = await mockRegisterTable(
      "resource",
      "res-015",
      registerRequest({ connection: "acme-scratch", table_name: "glossary_copy" }),
    );
    expect("columns" in result && result.columns).toEqual(
      fixtureColumns(mockResourceContent["res-015"] ?? ""),
    );
  });

  it("names an unnamed table after the file, which is what the form suggests", async () => {
    const result = await mockRegisterTable(
      "resource",
      "res-015",
      registerRequest({ connection: "acme-scratch" }),
    );
    expect("table" in result && result.table).toBe("analyst_glossary");
  });

  it("refuses the torn file, then corrects the bytes when asked and reads them back", async () => {
    const refusal = await mockRegisterTable(
      "resource",
      tornCSVSourceID,
      registerRequest({ connection: "acme-scratch" }),
    );
    expect("status" in refusal && refusal.status).toBe(409);

    const registered = await mockRegisterTable(
      "resource",
      tornCSVSourceID,
      registerRequest({ connection: "acme-scratch", repair: true }),
    );
    expect("repaired" in registered && registered.repaired).toContain(tornCSVRepairSummary);
    // The correction is bytes: the file the viewer reads afterwards is one a
    // line-based reader gets right.
    expect(inspectFixture(mockResourceContent[tornCSVSourceID] ?? "").rows).toBe(0);
    // And the table is over the corrected version, not the one it replaced.
    const head = mockResources.resources.find((r) => r.id === tornCSVSourceID)!.s3_key;
    expect("location" in registered && registered.location).toBe(
      `s3://acme-platform/${head.split("/").slice(0, -1).join("/")}/`,
    );
  });
});
