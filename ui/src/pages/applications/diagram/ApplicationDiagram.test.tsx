import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { renderWithQuery, serveApplications, signIn, type SentRequest } from "@/test/applicationHarness";
import { loadElk } from "@/pages/scripts/flow/flowLayout";
import { ApplicationDetailPage } from "../ApplicationDetailPage";
import { ApplicationParts } from "../ApplicationParts";
import { ReleaseControls } from "../ApplicationReleaseControls";
import type { ApplicationDetail } from "@/api/portal/hooks/applications";
import { mockApplicationDetails } from "@/mocks/data/applications";

// The Parts card's Diagram tab (#2540), against the same MSW handlers the
// dev mock server serves: the tabs and the one remembered, the diagram drawn
// with its findings, a selected node's links and its Flow tab, and the
// version and comparison each request names.

// LAYOUT_WAIT bounds a wait on the diagram: a layout runs asynchronously.
const LAYOUT_WAIT = { timeout: 4_000 };

beforeAll(async () => {
  await loadElk();
}, 30_000);

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function renderPage(app = "app-shop") {
  const onNavigate = vi.fn();
  renderWithQuery(<ApplicationDetailPage app={app} onNavigate={onNavigate} onBack={vi.fn()} />);
  return onNavigate;
}

const clickTab = (name: string) => fireEvent.mouseDown(screen.getByRole("tab", { name }));

async function openDiagram() {
  await screen.findByTestId("part-script-script-001");
  clickTab("Diagram");
  await screen.findByTestId("application-diagram-canvas", {}, LAYOUT_WAIT);
}

function card(id: string): SVGGElement {
  const el = document.querySelector(`[data-node="${id}"]`);
  if (!el) throw new Error(`no card ${id}`);
  return el as SVGGElement;
}

function diagramRequests(sent: SentRequest[]): string[] {
  return sent.filter((r) => r.path.includes("/diagram")).map((r) => r.path);
}

describe("Parts tabs", () => {
  it("opens on List and remembers the tab the viewer chose", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await screen.findByTestId("part-script-script-001");
    expect(screen.getByRole("tab", { name: "List" })).toHaveAttribute("aria-selected", "true");

    clickTab("Diagram");
    expect(screen.getByRole("tab", { name: "Diagram" })).toHaveAttribute("aria-selected", "true");
    expect(localStorage.getItem("application-parts-tab")).toBe("diagram");
    cleanup();

    renderPage();
    await screen.findByTestId("application-diagram", {}, LAYOUT_WAIT);
    expect(screen.getByRole("tab", { name: "Diagram" })).toHaveAttribute("aria-selected", "true");
    clickTab("List");
    expect(localStorage.getItem("application-parts-tab")).toBe("list");
    expect(screen.getByTestId("part-script-script-001")).toBeInTheDocument();
  });

  it("opens on List when the browser refuses storage", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    vi.stubGlobal("localStorage", {
      getItem: () => {
        throw new Error("denied");
      },
      setItem: () => {
        throw new Error("denied");
      },
    });
    renderPage();
    await screen.findByTestId("part-script-script-001");
    expect(screen.getByRole("tab", { name: "List" })).toHaveAttribute("aria-selected", "true");
    clickTab("Diagram");
    expect(screen.getByRole("tab", { name: "Diagram" })).toHaveAttribute("aria-selected", "true");
  });
});

describe("ApplicationDiagram", () => {
  it("draws the groups, each node with its kind and version, a computed node and the findings", async () => {
    const sent = serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await openDiagram();

    expect(diagramRequests(sent)).toEqual(["/api/v1/portal/applications/app-shop/diagram?version=current"]);
    for (const g of ["application", "schema", "outside"]) {
      expect(document.querySelector(`[data-group="${g}"]`)).not.toBeNull();
    }
    expect(document.querySelector('[data-group="outside"]')).toHaveTextContent("Outside the application");
    expect(card("script:script-001")).toHaveTextContent("Daily sales report");
    expect(card("script:script-001")).toHaveTextContent("Automation · v4");
    expect(card("script:script-001")).toHaveTextContent("schedule 0 7 * * * UTC");
    expect(card("table:acme-shop/shop.orders")).toHaveTextContent("Table");
    const computed = card("computed:script:script-001:out0");
    expect(computed).toHaveTextContent("computed");
    expect(computed.querySelector("rect")).toHaveAttribute("stroke-dasharray", "5 3");
    expect(document.querySelector('[data-link="asset:ast-001->script:script-002"]')).toHaveTextContent(
      "calls shop-orders",
    );
    expect(document.querySelector('[data-link="script:script-001->library:report-format"]')).toHaveTextContent(
      "loads v2",
    );

    const findings = screen.getByTestId("diagram-findings");
    expect(findings).toHaveTextContent("table shop.refunds is read by the application's scripts and written by no part");
    expect(within(findings).queryByText("Blocks release")).toBeNull();
  });

  it("shows what a selected node comes from and goes to, and opens an automation's flow", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    const onNavigate = renderPage();
    await openDiagram();

    fireEvent.pointerUp(card("script:script-002"));
    const panel = screen.getByTestId("diagram-panel");
    expect(panel).toHaveTextContent("Order intake API");
    expect(panel).toHaveTextContent("Automation · v2");
    const from = within(panel).getByRole("region", { name: "Comes from" });
    expect(from).toHaveTextContent("eventbilling-events");
    expect(from).toHaveTextContent("calls shop-ordersSales dashboard");
    expect(from).not.toHaveTextContent("writes");
    const to = within(panel).getByRole("region", { name: "Goes to" });
    expect(to).toHaveTextContent("writesorders");
    expect(document.querySelector('[data-link="script:script-002->table:acme-shop/shop.orders"]')).toHaveTextContent(
      "writes",
    );

    fireEvent.click(within(panel).getByRole("button", { name: "Open flow" }));
    expect(onNavigate).toHaveBeenCalledWith("/automations/script-002");
  });

  it("offers no flow for a node that is not an automation, and selects a finding's node", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await openDiagram();

    fireEvent.click(screen.getByRole("button", { name: /table shop\.refunds is read/ }));
    const panel = screen.getByTestId("diagram-panel");
    expect(panel).toHaveTextContent("refunds");
    expect(within(panel).getByRole("region", { name: "Comes from" })).toHaveTextContent("readsDaily sales report");
    expect(within(panel).queryByRole("button", { name: "Open flow" })).toBeNull();
    expect(card("table:acme-shop/shop.refunds")).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(within(panel).getByRole("button", { name: "Close" }));
    expect(screen.queryByTestId("diagram-panel")).toBeNull();
  });

  it("requests the version picked", async () => {
    const sent = serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await openDiagram();

    fireEvent.click(screen.getByRole("combobox", { name: "Version" }));
    const names = (await screen.findAllByRole("option")).map((o) => o.textContent);
    expect(names).toEqual(["Current release", "Latest saved", "Release 3", "Release 2", "Release 1"]);
    fireEvent.click(screen.getByRole("option", { name: "Latest saved" }));

    await waitFor(() =>
      expect(diagramRequests(sent)).toContain("/api/v1/portal/applications/app-shop/diagram?version=latest"),
    );
    await waitFor(() => expect(card("script:script-001")).toHaveTextContent("Automation · v5"), LAYOUT_WAIT);
    expect(document.querySelector('[data-node="output:script-001:weekly-summary"]')).not.toBeNull();
    expect(document.querySelector('[data-link="script:script-001->library:report-format"]')).toHaveTextContent(
      "loads v3",
    );
  });

  it("marks what changed since a release", async () => {
    const sent = serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await openDiagram();

    fireEvent.click(screen.getByRole("switch", { name: "Show changes since" }));
    await waitFor(() =>
      expect(diagramRequests(sent)).toContain("/api/v1/portal/applications/app-shop/diagram?version=current&compare=2"),
    );
    // Release 2 pins script-001 at v5; the current release (3) at v4.
    await waitFor(() => expect(card("script:script-001")).toHaveAttribute("data-change", "changed"), LAYOUT_WAIT);
    expect(card("script:script-001")).toHaveTextContent("v5 → v4");
    expect(card("computed:script:script-001:out0")).toHaveAttribute("data-change", "added");
    expect(card("output:script-001:weekly-summary")).toHaveAttribute("data-change", "removed");
    expect(card("asset:ast-001")).toHaveAttribute("data-change", "unchanged");
    // Release 2's v5 loads report-format v3; the current release's v4 loads v2.
    const load = document.querySelector('[data-link="script:script-001->library:report-format"]');
    expect(load).toHaveAttribute("data-change", "changed");
    expect(load).toHaveTextContent("loads v3 → v2");
    expect(load?.querySelector("path")).toHaveAttribute("stroke", "hsl(var(--chart-4))");
    fireEvent.pointerUp(card("script:script-001"));
    expect(within(screen.getByRole("region", { name: "Goes to" })).getByText("loads v3 → v2")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("combobox", { name: "Changes since release" }));
    fireEvent.click(await screen.findByRole("option", { name: "Release 1" }));
    await waitFor(() =>
      expect(diagramRequests(sent)).toContain("/api/v1/portal/applications/app-shop/diagram?version=current&compare=1"),
    );
    await waitFor(() => expect(card("channel:ops-alerts")).toHaveAttribute("data-change", "added"), LAYOUT_WAIT);
    expect(document.querySelector('[data-link="script:script-001->channel:ops-alerts"]')).toHaveAttribute(
      "data-change",
      "added",
    );
  });

  it("draws an application with no release at its latest saved versions", async () => {
    const sent = serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage("app-intake");
    await screen.findByTestId("part-script-script-004");
    clickTab("Diagram");
    await screen.findByTestId("application-diagram-canvas", {}, LAYOUT_WAIT);
    expect(diagramRequests(sent)).toEqual(["/api/v1/portal/applications/app-intake/diagram?version=latest"]);
    expect(card("script:script-004")).toHaveTextContent("Intake form");
    expect(screen.getByRole("switch", { name: "Show changes since" })).toBeDisabled();
  });

  it("marks a finding that blocks a release", async () => {
    serveApplications();
    const served = globalThis.fetch;
    const blocking = {
      version: "current",
      nodes: [
        { id: "script:s1", kind: "script", label: "Order sync", group: "application", part: true, part_id: "s1", version: 3, computed: false, detail: [] },
      ],
      links: [],
      groups: [{ id: "application", label: "shop" }, { id: "outside", label: "Outside the application" }],
      findings: [{ kind: "library_version", node: "script:s1", message: "script sync loads library fmt at version 2", blocks: true }],
    };
    vi.stubGlobal("fetch", (input: string, init?: RequestInit) =>
      input.includes("/diagram") ? Promise.resolve(new Response(JSON.stringify(blocking))) : served(input, init),
    );
    signIn("j.martinez@example.com", false);
    renderPage();
    await openDiagram();
    const findings = screen.getByTestId("diagram-findings");
    expect(findings).toHaveTextContent("script sync loads library fmt at version 2");
    expect(within(findings).getByText("Blocks release")).toBeInTheDocument();
  });

  it("says an application with no parts has none, and draws nothing", () => {
    const sent = serveApplications();
    signIn("j.martinez@example.com", false);
    localStorage.setItem("application-parts-tab", "diagram");
    const empty = structuredClone(mockApplicationDetails["app-intake"]!);
    empty.application.parts = [];
    empty.parts = [];
    renderWithQuery(<ApplicationParts detail={empty} canEdit={false} isAdmin={false} onNavigate={vi.fn()} />);
    expect(screen.getByRole("tab", { name: "Diagram" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("No parts yet")).toBeInTheDocument();
    expect(diagramRequests(sent)).toEqual([]);
  });
});

describe("Release", () => {
  function detailWith(findings: ApplicationDetail["findings"]): ApplicationDetail {
    const d = structuredClone(mockApplicationDetails["app-shop"]!);
    return { ...d, findings, problems: findings.map((f) => f.message) };
  }

  it("is held back only by a finding that blocks a release", () => {
    signIn("admin@example.com", true);
    const advice = { kind: "unwritten_table" as const, node: "table:x", message: "table x is written by no part", blocks: false };
    const block = {
      kind: "library_version" as const,
      node: "script:y",
      message: "script y loads library z at version 1",
      blocks: true,
    };

    renderWithQuery(<ReleaseControls detail={detailWith([advice])} />);
    expect(screen.getByRole("button", { name: "Release" })).toBeEnabled();
    cleanup();

    renderWithQuery(<ReleaseControls detail={detailWith([advice, block])} />);
    expect(screen.getByRole("button", { name: "Release" })).toBeDisabled();
  });
});
