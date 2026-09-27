import { describe, expect, it } from "vitest";
import {
  CARD_WIDTH,
  FONT_MONO,
  cardHeight,
  cardText,
  excerpt,
  fitText,
  lit,
  nodeLabel,
  nodeLines,
  nodesForLines,
  purposeShort,
  roundedPath,
  textWidth,
} from "./flowModel";
import { node, sampleGraph } from "./testGraph";

describe("flowModel: what a card says", () => {
  it("drops the script-name prefix authors put on a purpose", () => {
    expect(purposeShort("Daily load: look up each account.")).toBe("look up each account.");
    expect(purposeShort("No prefix here.")).toBe("No prefix here.");
    expect(purposeShort(undefined)).toBe("");
    const long = `${"x".repeat(60)}: the rest`;
    expect(purposeShort(long)).toBe(long);
  });

  it("puts the purpose, the operation, the tables and the chips on the card", () => {
    const g = sampleGraph();
    const api = cardText(g.nodes[2]!);
    expect(api.lines.map((l) => l.text)).toEqual(["look up each account.", "GET /v1/accounts"]);
    expect(api.lines[1]!.mono).toBe(true);
    expect(api.chips).toEqual(["fetch()", "↻ d in days"]);

    const q = cardText(node({ id: "q", detail: ["a", "b", "c", "d", "e"] }));
    expect(q.lines.map((l) => l.text)).toEqual(["a", "b", "c", "d"]);
    expect(q.chips).toEqual([]);

    const state = cardText(g.nodes[0]!);
    expect(state.lines[0]).toMatchObject({ mono: false, muted: true });
  });

  it("sizes a card to what it says", () => {
    const plain = cardHeight(node({ id: "a" }));
    expect(cardHeight(node({ id: "b", detail: ["t"] }))).toBe(plain + 17);
    expect(cardHeight(node({ id: "c", wrapper: "w" }))).toBe(plain + 24);
  });

  it("cuts a line to the width it is drawn in", () => {
    const text = "hive.sales.a_table_name_long_enough_to_need_cutting_at_the_card_edge";
    const cut = fitText(text, CARD_WIDTH - 28, FONT_MONO);
    expect(cut.endsWith("…")).toBe(true);
    expect(textWidth(cut, FONT_MONO)).toBeLessThanOrEqual(CARD_WIDTH - 28);
    expect(fitText("short", 200, FONT_MONO)).toBe("short");
  });
});

describe("flowModel: routes", () => {
  it("rounds the corners of an orthogonal route", () => {
    expect(roundedPath([])).toBe("");
    expect(roundedPath([{ x: 0, y: 0 }, { x: 10, y: 0 }])).toBe("M0,0 L10,0");
    expect(
      roundedPath([
        { x: 0, y: 0 },
        { x: 40, y: 0 },
        { x: 40, y: 40 },
      ]),
    ).toBe("M0,0 L32,0 Q40,0 40,8 L40,40");
  });
});

describe("flowModel: what a selection lights up", () => {
  it("lights a card and the edges on it", () => {
    const g = sampleGraph();
    const l = lit(g, { kind: "node", id: "op:2" }, null);
    expect([...l.nodes]).toEqual(["op:2"]);
    expect([...l.edges].map((e) => `${e.from}>${e.to}`)).toEqual(["op:1>op:2", "op:2>op:3"]);
    expect(l.dimOthers).toBe(false);
  });

  it("lights exactly the steps a parameter's value reaches, and dims the rest", () => {
    const g = sampleGraph();
    const l = lit(g, { kind: "param", name: "day" }, null);
    expect([...l.nodes]).toEqual(["op:1"]);
    expect(l.dimOthers).toBe(true);
    expect([...lit(g, { kind: "param", name: "mode" }, null).nodes]).toEqual([]);
  });

  it("lights the cards some source lines produced, a folded wrapper's call site included", () => {
    const g = sampleGraph();
    expect(nodesForLines(g, { from: 6, to: 6 })).toEqual(["op:1"]);
    expect(nodesForLines(g, { from: 9, to: 9 })).toEqual(["op:2"]);
    expect(nodesForLines(g, { from: 10, to: 12 })).toEqual(["op:3", "op:4"]);
    const l = lit(g, null, { from: 9, to: 9 });
    expect([...l.nodes]).toEqual(["op:2"]);
    expect(l.dimOthers).toBe(true);
    expect(lit(g, null, { from: 19, to: 20 }).dimOthers).toBe(false);
    expect(lit(g, { kind: "group", id: "/load" }, { from: 5, to: 5 }).nodes.size).toBe(0);
  });

  it("names a card's source lines and a card in a list", () => {
    const g = sampleGraph();
    expect(nodeLines(g.nodes[2]!)).toEqual([
      { from: 14, to: 14 },
      { from: 9, to: 9 },
    ]);
    expect(nodeLines(node({ id: "x", line: 0 }))).toEqual([]);
    expect(nodeLabel(g, "op:2")).toBe("API crm · GET /v1/accounts");
    expect(nodeLabel(g, "op:1")).toBe("Query trino");
    expect(nodeLabel(g, "nope")).toBe("nope");
  });

  it("numbers an excerpt of the source", () => {
    const src = Array.from({ length: 12 }, (_, i) => `line ${i + 1}`).join("\n");
    expect(excerpt(src, 10, 1, 4)).toBe(" 9  line 9\n10  line 10\n11  line 11\n12  line 12");
    expect(excerpt("", 3, 1, 1)).toBe("");
  });
});
