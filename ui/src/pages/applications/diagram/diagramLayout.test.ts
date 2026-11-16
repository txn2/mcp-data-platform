import { describe, it, expect } from "vitest";
import type { ApplicationDiagram } from "@/api/portal/hooks/applications";
import { elkDiagram, placeDiagram } from "./diagramLayout";

// #2665: the schema is drawn as its group's heading, its tables inside it,
// and no longer as a card of its own beside them.

function diagram(): ApplicationDiagram {
  return {
    version: "",
    groups: [
      { id: "application", label: "shop" },
      { id: "schema", label: "shop", parent: "application" },
    ],
    nodes: [
      { id: "script:s1", kind: "script", label: "sync-photos", group: "application", part: true },
      { id: "schema:shop", kind: "schema", label: "shop", group: "schema", part: true, part_id: "shop" },
      { id: "table:orders/shop.photos", kind: "table", label: "photos", group: "schema" },
      { id: "table:orders/shop.audit_trail", kind: "table", label: "audit_trail", group: "schema" },
    ],
    links: [{ from: "script:s1", to: "table:orders/shop.photos", kind: "writes" }],
    findings: [],
  } as unknown as ApplicationDiagram;
}

describe("the schema as its group's heading (#2665)", () => {
  it("lays out the tables inside the schema's box and no card for the schema", () => {
    const root = elkDiagram(diagram());
    const app = root.children!.find((c) => c.id === "g:application")!;
    const schema = app.children!.find((c) => c.id === "g:schema")!;
    expect(schema.children!.map((c) => c.id)).toEqual(["table:orders/shop.photos", "table:orders/shop.audit_trail"]);
    expect(schema.layoutOptions!["elk.nodeSize.minimum"]).toBeDefined();
    expect(JSON.stringify(root)).not.toContain('"id":"schema:shop"');
  });

  it("keeps a schema with no table yet as a heading on an empty box", () => {
    const d = diagram();
    d.nodes = d.nodes.filter((n) => n.kind !== "table");
    d.links = [];
    const schema = elkDiagram(d).children![0]!.children!.find((c) => c.id === "g:schema")!;
    expect(schema.children).toEqual([]);
  });

  it("hands the schema node to the placed group as its heading", () => {
    const d = diagram();
    const out = {
      id: "root", x: 0, y: 0, width: 800, height: 400,
      children: [{ id: "g:application", x: 0, y: 0, width: 800, height: 400, children: [{ id: "g:schema", x: 300, y: 40, width: 400, height: 300, children: [] }] }],
    };
    const placed = placeDiagram(d, out);
    const schema = placed.groups.find((g) => g.group.id === "schema")!;
    expect(schema.heading?.id).toBe("schema:shop");
    expect(placed.groups.find((g) => g.group.id === "application")!.heading).toBeUndefined();
  });
});

describe("the schema heading on the canvas (#2665)", () => {
  it("names the schema in its group's heading and selects it like a card", async () => {
    const { render, screen, fireEvent, cleanup } = await import("@testing-library/react");
    const { DiagramCanvas } = await import("./DiagramCanvas");
    const React = await import("react");
    const d = diagram();
    const layout = placeDiagram(d, {
      id: "root", x: 0, y: 0, width: 800, height: 400,
      children: [{ id: "g:application", x: 0, y: 0, width: 800, height: 400, children: [{ id: "g:schema", x: 300, y: 40, width: 400, height: 300, children: [] }] }],
    });
    let picked: string | null = null;
    render(React.createElement(DiagramCanvas, { layout, selected: null, compared: false, onSelect: (id: string | null) => (picked = id) }));
    const heading = screen.getByRole("button", { name: /shop$/ });
    expect(heading).toHaveAttribute("data-heading", "schema:shop");
    fireEvent.click(heading);
    expect(picked).toBe("schema:shop");
    cleanup();
  });
});
