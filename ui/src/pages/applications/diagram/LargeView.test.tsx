import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { render } from "@testing-library/react";
import type { ApplicationDiagram, ApplicationDiagramNode } from "@/api/portal/hooks/applications";
import { renderWithQuery, serveApplications, signIn } from "@/test/applicationHarness";
import { ApplicationDetailPage } from "../ApplicationDetailPage";
import { LargeView } from "./LargeView";

// A large application's Diagram tab (#2700), through the same MSW handlers the
// dev mock server serves for "warehouse" (twenty automations, twenty tables):
// one band in place of every automation-to-table line, a selected automation's
// tables and how it reaches them, a selected table's writers and readers, the
// Table view, the findings, the comparison, and the phone's list.

const WAIT = { timeout: 4_000 };
const innerWidth = window.innerWidth;

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  localStorage.clear();
  Object.defineProperty(window, "innerWidth", { configurable: true, value: innerWidth });
});

async function openWarehouse() {
  serveApplications();
  signIn("sarah.chen@example.com", false);
  localStorage.setItem("application-parts-tab", "diagram");
  renderWithQuery(<ApplicationDetailPage app="app-warehouse" onNavigate={vi.fn()} onBack={vi.fn()} />);
  return screen.findByTestId("application-diagram-large", {}, WAIT);
}

const row = (id: string) => document.querySelector<HTMLButtonElement>(`[data-node="${id}"]`)!;
const T = (name: string) => `table:acme-shop/wh.${name}`;
const tableLinesDrawn = () =>
  [...document.querySelectorAll("[data-link]")].filter((l) => l.getAttribute("data-link")!.includes("->table:")).length;

describe("a large application's drawing", () => {
  it("shrinks to fit the width it is given, so the tables box stays on screen", async () => {
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(796);
    const drawing = await openWarehouse();
    expect(Number(drawing.getAttribute("data-scale"))).toBeCloseTo(0.8, 1);
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(2000);
    cleanup();
    expect(Number((await openWarehouse()).getAttribute("data-scale"))).toBe(1);
  });

  it("draws one band and no automation-to-table line until something is selected", async () => {
    await openWarehouse();
    expect(screen.getByTestId("diagram-band")).toHaveTextContent("20 automations reach 20 tables. Select one to see which.");
    expect(tableLinesDrawn()).toBe(0);
    // The event that starts an automation is not a table link, and is drawn.
    expect(document.querySelector('[data-link="source:stock-events->script:wh-07"]')).not.toBeNull();
    expect(screen.getByRole("button", { name: "Drawing" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("diagram-key")).toHaveTextContent("writes (thick line)");
    expect(screen.queryByTestId("application-diagram-canvas")).toBeNull();
  });

  it("lights a selected automation's tables, writes thick and reads thin, and lists them", async () => {
    await openWarehouse();
    fireEvent.click(row("script:wh-12"));
    expect(screen.queryByTestId("diagram-band")).toBeNull();
    const writesLine = document.querySelector(`[data-link="script:wh-12->${T("refund")}"]`)!;
    const readsLine = document.querySelector(`[data-link="script:wh-12->${T("payment")}"]`)!;
    expect(Number(writesLine.getAttribute("stroke-width"))).toBeGreaterThan(Number(readsLine.getAttribute("stroke-width")));
    expect(tableLinesDrawn()).toBe(6);
    expect(document.querySelector('[data-link="script:wh-12->computed:script:wh-12:acme-shop"]')).toHaveAttribute("stroke-dasharray", "5 3");
    expect(row(T("refund"))).toHaveAccessibleName("Table refund: writes");
    expect(row(T("payment"))).toHaveAccessibleName("Table payment: reads");
    expect(row(T("customer")).style.opacity).toBe("0.35");

    const panel = screen.getByTestId("diagram-panel");
    expect(within(panel).getByRole("region", { name: "Writes" })).toHaveTextContent("Writes 3refundorder_statustable named when it runs");
    expect(within(panel).getByRole("region", { name: "Reads" })).toHaveTextContent("Reads 4");
    expect(within(panel).getByRole("button", { name: "Open flow" })).toBeInTheDocument();

    fireEvent.keyDown(row("script:wh-12"), { key: "Escape" });
    expect(screen.getByTestId("diagram-band")).toBeInTheDocument();
  });

  it("lists a selected table's writers and readers", async () => {
    await openWarehouse();
    fireEvent.click(row(T("refund")));
    const panel = screen.getByTestId("diagram-panel");
    expect(within(panel).getByRole("region", { name: "Written by" })).toHaveTextContent("Written by 1Post refunds");
    expect(within(panel).getByRole("region", { name: "Read by" })).toHaveTextContent("Read by 4");
    expect(row("script:wh-12")).toHaveAccessibleName("Automation Post refunds: writes");
  });

  it("shows the findings, the run-time writers once, and flags the tables no part writes", async () => {
    await openWarehouse();
    const findings = screen.getByTestId("diagram-findings");
    expect(within(findings).getAllByRole("button")).toHaveLength(2);
    expect(screen.getAllByTestId("diagram-runtime-writes")).toHaveLength(1);
    expect(screen.getByTestId("diagram-runtime-writes")).toHaveTextContent("scripts Post refunds and Sync carriers write tables");
    expect(row(T("refund_reason"))).toHaveAccessibleName("Table refund_reason: no writer · read by 1");
    expect(document.querySelectorAll('[data-node^="computed:script:"]')).toHaveLength(2);
    fireEvent.click(within(findings).getAllByRole("button")[0]!);
    expect(row(T("refund_reason"))).toHaveAttribute("aria-pressed", "true");
  });
});

describe("the Table view", () => {
  it("is automations by tables, R and W in the cells, writers counted under each table", async () => {
    await openWarehouse();
    fireEvent.click(screen.getByRole("button", { name: "Table" }));
    const table = screen.getByRole("table", { name: "Automations by tables" });
    expect(within(table).getAllByRole("columnheader")).toHaveLength(22);
    expect(within(table).getByLabelText("Post refunds, refund: writes")).toHaveTextContent("W");
    expect(within(table).getByLabelText("Post refunds, payment: reads")).toHaveTextContent("R");
    expect(document.querySelector(`[data-written-by="${T("refund_reason")}"]`)).toHaveTextContent("none");
    expect(document.querySelector(`[data-written-by="${T("order_status")}"]`)).toHaveTextContent("2");
    fireEvent.click(within(table).getByRole("button", { name: "refund" }));
    expect(within(screen.getByTestId("diagram-panel")).getByRole("region", { name: "Written by" })).toHaveTextContent("Post refunds");
  });

  it("opens on a phone as a list grouped by automation or by table", async () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 390 });
    serveApplications();
    signIn("sarah.chen@example.com", false);
    localStorage.setItem("application-parts-tab", "diagram");
    renderWithQuery(<ApplicationDetailPage app="app-warehouse" onNavigate={vi.fn()} onBack={vi.fn()} />);
    const view = await screen.findByTestId("diagram-table-view", {}, WAIT);
    fireEvent.click(within(view).getByRole("button", { name: /^Post refunds/ }));
    expect(within(view).getByRole("region", { name: "Writes" })).toHaveTextContent("refund, order_status");
    fireEvent.click(within(view).getByRole("button", { name: "By table" }));
    expect(within(view).getByRole("button", { name: /^refund_reason/ })).toHaveTextContent("no writer · read by 1");
    expect(screen.queryByTestId("diagram-panel")).toBeNull();
  });
});

function n(id: string, kind: ApplicationDiagramNode["kind"], extra: Partial<ApplicationDiagramNode> = {}): ApplicationDiagramNode {
  return { id, kind, label: id.split(":")[1]!, group: kind === "table" ? "schema" : "application", part: kind === "script", computed: false, detail: [], ...extra };
}

describe("the comparison", () => {
  it("draws added and removed links without a selection and says what changed", () => {
    const nodes: ApplicationDiagramNode[] = [];
    const links: ApplicationDiagram["links"] = [];
    for (let i = 0; i < 14; i++) {
      nodes.push(n(`script:s${i}`, "script", { change: "unchanged" }), n(`table:t${i}`, "table", { change: "unchanged" }));
      links.push({ from: `script:s${i}`, to: `table:t${i}`, kind: "writes", change: "unchanged" });
    }
    nodes[0] = n("script:s0", "script", { change: "changed", version: 4, was_version: 3 });
    nodes.push(n("script:new", "script", { change: "added" }), n("script:gone", "script", { change: "removed" }));
    links.push({ from: "script:new", to: "table:t1", kind: "writes", change: "added" });
    links.push({ from: "script:gone", to: "table:t2", kind: "reads", change: "removed" });
    const d: ApplicationDiagram = { version: "latest", compared_with: "4", nodes, links, groups: [], findings: [] };
    render(<LargeView diagram={d} selected={null} onSelect={vi.fn()} onNavigate={vi.fn()} />);

    expect(document.querySelector('[data-link="script:new->table:t1"]')).toHaveAttribute("data-change", "added");
    expect(document.querySelector('[data-link="script:gone->table:t2"]')).toHaveAttribute("data-change", "removed");
    expect(document.querySelector('[data-link="script:s3->table:t3"]')).toBeNull();
    expect(row("script:s0")).toHaveAccessibleName("Automation s0: v3 → v4");
    expect(row("script:new")).toHaveAccessibleName("Automation new: added");
    const changes = screen.getByTestId("diagram-changes");
    expect(changes).toHaveTextContent("Changes since release 4");
    expect(changes).toHaveTextContent("1 added · 1 changed · 1 removed");
  });
});
