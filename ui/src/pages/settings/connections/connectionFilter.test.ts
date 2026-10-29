import { describe, expect, it } from "vitest";
import type { EffectiveConnection } from "@/api/admin/types";
import {
  filterConnections,
  kindLabel,
  kindOptions,
  lacksCatalog,
  matches,
  readFilter,
  writeFilter,
} from "./connectionFilter";

const conn = (kind: string, name: string, extra: Partial<EffectiveConnection> = {}): EffectiveConnection => ({
  kind,
  name,
  connection: name,
  source: "database",
  tools: [],
  ...extra,
});

const list = [
  conn("trino", "acme-warehouse", { description: "Sales and **finance** tables" }),
  conn("api", "acme-crm", { description: "The CRM's REST API", config: { display_name: "Customer records" } }),
  conn("api", "billing"),
  conn("s3", "acme-drop"),
  conn("graphql", "catalog-graph"),
];

describe("connection filter (#2350)", () => {
  it("matches the name, display name, kind and description, every word, any case", () => {
    expect(matches(list[0]!, "WAREHOUSE")).toBe(true);
    expect(matches(list[0]!, "finance")).toBe(true);
    expect(matches(list[1]!, "customer records")).toBe(true);
    expect(matches(list[1]!, "rest crm")).toBe(true);
    expect(matches(list[4]!, "GraphQL")).toBe(true);
    expect(matches(list[2]!, "billing crm")).toBe(false);
    expect(matches(list[2]!, "   ")).toBe(true);
  });

  it("narrows by kind and text together, and an empty filter is the whole list", () => {
    expect(filterConnections(list, { q: "acme", type: "" }).map((c) => c.name)).toEqual([
      "acme-warehouse",
      "acme-crm",
      "acme-drop",
    ]);
    expect(filterConnections(list, { q: "acme", type: "api" }).map((c) => c.name)).toEqual(["acme-crm"]);
    expect(filterConnections(list, { q: "", type: "api" }).map((c) => c.name)).toEqual(["acme-crm", "billing"]);
    expect(filterConnections(list, { q: "", type: "" })).toHaveLength(list.length);
  });

  it("reads and writes the address without touching the selection", () => {
    expect(readFilter("?kind=api&name=billing&q=acme&type=api")).toEqual({ q: "acme", type: "api" });
    expect(readFilter("")).toEqual({ q: "", type: "" });
    const params = writeFilter(new URLSearchParams("kind=api&name=billing&q=old"), { q: "crm tables", type: "" });
    expect(params.get("kind")).toBe("api");
    expect(params.get("name")).toBe("billing");
    expect(params.get("q")).toBe("crm tables");
    expect(params.has("type")).toBe(false);
    expect(readFilter(`?${params.toString()}`)).toEqual({ q: "crm tables", type: "" });
  });

  it("offers the kinds the deployment has, in the create form's order", () => {
    expect(kindOptions(list, ["trino", "s3", "mcp", "api", "graphql"])).toEqual([
      { value: "", label: "All kinds" },
      { value: "trino", label: "Trino" },
      { value: "s3", label: "S3" },
      { value: "api", label: "API" },
      { value: "graphql", label: "GraphQL" },
    ]);
    const withOther = kindOptions([...list, conn("ldap", "dir")], ["trino"], "mcp");
    expect(withOther.map((o) => o.value)).toEqual(["", "trino", "api", "graphql", "ldap", "mcp", "s3"]);
    expect(kindLabel("ldap")).toBe("ldap");
  });
});

describe("lacksCatalog (#2271)", () => {
  it("is an api connection whose catalog_id is absent or blank", () => {
    expect(lacksCatalog(conn("api", "a", { config: {} }))).toBe(true);
    expect(lacksCatalog(conn("api", "platform-admin", { source: "file" }))).toBe(false);
    expect(lacksCatalog(conn("api", "a", { config: { catalog_id: " " } }))).toBe(true);
    expect(lacksCatalog(conn("api", "a", { config: { catalog_id: 7 } }))).toBe(true);
    expect(lacksCatalog(conn("api", "a", { config: { catalog_id: "crm" } }))).toBe(false);
    expect(lacksCatalog(conn("graphql", "g"))).toBe(false);
    expect(lacksCatalog(conn("trino", "t"))).toBe(false);
  });
});
