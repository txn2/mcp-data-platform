import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import type { CallRecord } from "@/api/admin/types";
import { callListQuery } from "@/api/admin/types";
import { mockCallRecords, mockCitingAssets } from "@/mocks/data/calls";
import { mockAssets } from "@/mocks/data/assets";
import { CallFilters, NO_CALL_FILTERS } from "./CallFilters";
import { CallsTable } from "./CallsTable";
import { CallDetailView } from "./CallDetailView";
import { connectionsIn } from "./CallsPage";

function record(overrides: Partial<CallRecord> = {}): CallRecord {
  return {
    id: "call-1",
    event_id: "evt-1",
    reference: "mcp:call:evt-1",
    kind: "sql",
    tool_name: "trino_query",
    connection: "acme-warehouse",
    statement: "SELECT region, SUM(amount) FROM sales.orders GROUP BY region",
    targets: ["urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)"],
    purpose: "Sizing Q3 revenue by region for the board deck.",
    user_id: "analyst@example.com",
    user_email: "analyst@example.com",
    session_id: "dps_9f2c1a4b",
    persona: "data-engineer",
    success: true,
    duration_ms: 143,
    response_chars: 2450,
    outcome: "satisfied",
    satisfied_by: "asset",
    artifacts: [{ kind: "asset", id: "ast-001", name: "Q4 Revenue Dashboard" }],
    reuse_count: 2,
    created_at: "2026-08-16T10:00:00Z",
    ...overrides,
  };
}

// A caller and two of the scripts they own: every one of them labelled with
// the same address before #1523.
const PRINCIPAL_FACET = {
  users: ["analyst@example.com", "script:acme-revenue-pulse", "script:acme-top-stores-drop"],
  user_labels: {
    "analyst@example.com": "analyst@example.com",
    "script:acme-revenue-pulse": "analyst@example.com",
    "script:acme-top-stores-drop": "analyst@example.com",
  },
  tools: [],
  toolkit_kinds: [],
  sources: [],
};

afterEach(cleanup);

describe("the call catalog list", () => {
  it("shows what a call was for, what ran, and how it ended", () => {
    render(<CallsTable records={[record()]} isLoading={false} onOpen={vi.fn()} />);

    expect(screen.getByText("Sizing Q3 revenue by region for the board deck.")).toBeTruthy();
    expect(screen.getByText(/SELECT region, SUM\(amount\)/)).toBeTruthy();
    expect(screen.getByText("satisfied")).toBeTruthy();
    expect(screen.getByText("analyst@example.com")).toBeTruthy();
  });

  it("opens the record on row click, like every other portal list", () => {
    const onOpen = vi.fn();
    render(<CallsTable records={[record()]} isLoading={false} onOpen={onOpen} />);

    fireEvent.click(screen.getByText("Sizing Q3 revenue by region for the board deck."));
    expect(onOpen).toHaveBeenCalledWith("call-1");
  });

  it("drops the user column on a reader's own calls", () => {
    render(
      <CallsTable records={[record()]} isLoading={false} onOpen={vi.fn()} showUser={false} />,
    );

    expect(screen.queryByText("analyst@example.com")).toBeNull();
  });

  it("says so when the catalog holds nothing", () => {
    render(<CallsTable records={[]} isLoading={false} onOpen={vi.fn()} />);
    expect(screen.getByText("No calls found")).toBeTruthy();
  });

  it("marks a call a script made rather than reading as its owner", () => {
    render(
      <CallsTable
        records={[record({ user_id: "script:acme-revenue-pulse" })]}
        isLoading={false}
        onOpen={vi.fn()}
      />,
    );

    expect(screen.getByText("script")).toBeTruthy();
    expect(screen.getByText("acme-revenue-pulse - analyst@example.com")).toBeTruthy();
  });

  it("offers only the connections present on the page as a facet", () => {
    expect(connectionsIn([{ connection: "b" }, { connection: "a" }, {}])).toEqual(["a", "b"]);
  });
});

describe("the call catalog filters", () => {
  it("omits the user facet on a reader's own calls", () => {
    render(
      <CallFilters
        value={NO_CALL_FILTERS}
        onChange={vi.fn()}
        showUserFacet={false}
      />,
    );

    expect(screen.queryByLabelText("Filter by user")).toBeNull();
    expect(screen.getByLabelText("Filter by kind")).toBeTruthy();
  });

  it("gives a caller and each script they own a distinguishable option", () => {
    const onChange = vi.fn();
    render(
      <CallFilters filters={PRINCIPAL_FACET} value={NO_CALL_FILTERS} onChange={onChange} />,
    );

    // jsdom has no PointerEvent, so the trigger's pointerdown handler never
    // runs and the listbox is opened from the keyboard (see ui/README.md).
    fireEvent.keyDown(screen.getByLabelText("Filter by user"), { key: "Enter" });
    const labels = screen.getAllByRole("option").map((o) => o.textContent);
    expect(new Set(labels).size).toBe(labels.length);

    fireEvent.click(screen.getByRole("option", { name: /acme-revenue-pulse/ }));
    expect(onChange).toHaveBeenCalledWith({ userId: "script:acme-revenue-pulse" });
  });

  it("asks for the review queue by name rather than as a boolean facet", () => {
    expect(callListQuery({ promotable: true })).toBe("?queue=promotable");
    expect(callListQuery({ kind: "sql", outcome: "satisfied" })).toBe(
      "?kind=sql&outcome=satisfied",
    );
  });
});

describe("one call opened", () => {
  it("shows what was built from it and links to the session that ran it", () => {
    const onNavigate = vi.fn();
    render(
      <CallDetailView
        record={record()}
        sessionPath={(id) => `/activity/sessions/${id}`}
        assetPath={(id) => `/assets/${id}`}
        onNavigate={onNavigate}
      />,
    );

    expect(screen.getByText("Q4 Revenue Dashboard")).toBeTruthy();
    fireEvent.click(screen.getByText("dps_9f2c1a4b"));
    expect(onNavigate).toHaveBeenCalledWith("/activity/sessions/dps_9f2c1a4b");
  });

  it("drops the caller on a reader's own record, as the list drops its column", () => {
    render(<CallDetailView record={record()} showUser={false} />);

    expect(screen.queryByText("analyst@example.com")).toBeNull();
    expect(screen.getByText("Where it came from")).toBeTruthy();
  });

  it("offers the decision only on a record that answered something", () => {
    render(<CallDetailView record={record({ outcome: "ran", satisfied_by: undefined })} onPromote={vi.fn()} />);

    expect(screen.queryByText("Publish")).toBeNull();
    expect(screen.getByText("Not yet publishable")).toBeTruthy();
  });

  it("publishes on request, and reports a refusal rather than swallowing it", () => {
    const onPromote = vi.fn();
    render(
      <CallDetailView record={record()} onPromote={onPromote} actionError="no DataHub connection is configured" />,
    );

    fireEvent.click(screen.getByText("Publish"));
    expect(onPromote).toHaveBeenCalled();
    expect(screen.getByText("no DataHub connection is configured")).toBeTruthy();
  });

  it("shows what a promoted record became instead of offering to promote it again", () => {
    render(
      <CallDetailView
        record={record({ promoted_urn: "urn:li:query:abc", promoted_by: "admin@example.com" })}
        onPromote={vi.fn()}
      />,
    );

    expect(screen.getByText("urn:li:query:abc")).toBeTruthy();
    expect(screen.queryByText("Publish")).toBeNull();
  });

  it("declines with the note the reviewer typed", () => {
    const onReject = vi.fn();
    render(<CallDetailView record={record()} onReject={onReject} />);

    fireEvent.change(screen.getByLabelText("Why this call is not worth publishing"), {
      target: { value: "Superseded by the revenue view." },
    });
    fireEvent.click(screen.getByText("Decline"));
    expect(onReject).toHaveBeenCalledWith("Superseded by the revenue view.");
  });
});

describe("the mock catalog", () => {
  // The fixture names the assets its records are satisfied by. If an id or a
  // name drifts from the asset fixture, the UI would show a record satisfied
  // by an asset that does not exist under that name, which the server could
  // never produce.
  it("cites assets that exist, under the names they carry", () => {
    for (const citing of mockCitingAssets) {
      const asset = mockAssets.find((a) => a.id === citing.assetId);
      expect(asset, `mock asset ${citing.assetId}`).toBeTruthy();
      expect(asset!.name).toBe(citing.assetName);
    }
  });

  // Satisfaction is read off the citing asset's own provenance, so a record
  // the catalog calls satisfied must be named by the asset it names.
  it("gives every satisfied record a citing artifact", () => {
    const satisfied = mockCallRecords.filter((r) => r.outcome === "satisfied");
    expect(satisfied.length).toBeGreaterThan(0);
    for (const rec of satisfied) {
      expect(rec.artifacts?.length, rec.id).toBeGreaterThan(0);
      expect(rec.satisfied_by, rec.id).toBeTruthy();
    }
  });

  it("records the cited call ids on the assets that cite them", () => {
    for (const citing of mockCitingAssets) {
      const asset = mockAssets.find((a) => a.id === citing.assetId)!;
      const cited = mockCallRecords
        .filter((r) => r.artifacts?.some((a) => a.id === citing.assetId))
        .map((r) => r.event_id);
      const recorded = (asset.provenance?.captures ?? []).flatMap(
        (c) => c.event_ids ?? [],
      );
      for (const eventID of cited) {
        expect(recorded, `${citing.assetId} provenance`).toContain(eventID);
      }
    }
  });

  // The server reads satisfaction from an asset having NAMED the call, so a
  // fixture whose assets merely captured it would be showing a state the
  // server cannot produce (#1353).
  it("has the citing assets name the calls, not merely capture them", () => {
    for (const citing of mockCitingAssets) {
      const asset = mockAssets.find((a) => a.id === citing.assetId)!;
      const cited = mockCallRecords
        .filter((r) => r.artifacts?.some((a) => a.id === citing.assetId))
        .map((r) => r.event_id);
      expect(cited.length, citing.assetId).toBeGreaterThan(0);
      for (const eventID of cited) {
        const named = (asset.provenance?.captures ?? []).some(
          (c) =>
            (c.event_ids ?? []).includes(eventID) &&
            (c.explicit === true ||
              (c.calls ?? []).some((call) => call.event_id === eventID && call.cited)),
        );
        expect(named, `${citing.assetId} must name ${eventID}`).toBe(true);
      }
    }
  });

  // Supersession is a read-shaped idea, and an API target carries the resource
  // it addressed rather than the template (#1352).
  it("supersedes only reads, over targets that name a resource", () => {
    const superseded = mockCallRecords.filter((r) => r.outcome === "superseded");
    for (const rec of superseded) {
      expect(rec.targets.length, rec.id).toBeGreaterThan(0);
      if (rec.kind === "api") {
        expect(rec.method, rec.id).toBe("GET");
      } else {
        expect(rec.statement, rec.id).toMatch(/^\s*(with|select|show)/i);
      }
    }
    const mutations = mockCallRecords.filter(
      (r) => r.kind === "sql" && /^\s*insert/i.test(r.statement ?? ""),
    );
    expect(mutations.length).toBeGreaterThan(0);
    for (const rec of mutations) {
      expect(rec.outcome, rec.id).not.toBe("superseded");
    }
    for (const rec of mockCallRecords.filter((r) => r.kind === "api")) {
      for (const target of rec.targets) {
        expect(target, rec.id).not.toContain("{");
      }
    }
  });
});
