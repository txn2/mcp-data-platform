import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, within } from "@testing-library/react";
import type { Provenance } from "@/api/portal/types";
import { ProvenancePanel } from "./ProvenancePanel";

// The panel answers "what produced this, and what was it for". Since #1320
// that answer is grouped by capture — one per write — and each call carries
// its kind, outcome, stated purpose, and the reference an agent can cite.
//
// Captures are appended, so the last one is the newest write. Since #1422 the
// panel leads with that one and puts every earlier capture behind a disclosure,
// each opening on its own; the fixture below is oldest-first as the API sends it.

const captured: Provenance = {
  session_id: "dps_abc",
  user_id: "user-alice",
  captures: [
    {
      tool: "save_asset",
      captured_at: "2026-08-16T10:05:00Z",
      version: 1,
      session_id: "dps_abc",
      event_ids: ["evt-1", "evt-2"],
      calls: [
        {
          event_id: "evt-1",
          kind: "sql",
          tool: "trino_query",
          connection: "warehouse",
          statement: "SELECT region, revenue FROM sales.quarterly",
          purpose: "Totalling Q3 revenue by region for the board deck.",
          outcome: "error",
          error: "TABLE_NOT_FOUND: sales.quarterly",
          duration_ms: 95,
          timestamp: "2026-08-16T10:00:00Z",
        },
        {
          event_id: "evt-2",
          kind: "api",
          tool: "api_invoke_endpoint",
          connection: "crm",
          method: "GET",
          path: "/v1/accounts",
          operation_id: "listAccounts",
          purpose: "Naming the accounts the revenue split is broken out by.",
          outcome: "success",
          duration_ms: 640,
          timestamp: "2026-08-16T10:02:00Z",
        },
      ],
    },
    {
      tool: "manage_asset",
      captured_at: "2026-08-16T11:00:00Z",
      version: 2,
      explicit: true,
      truncated: true,
      event_ids: ["evt-3"],
      calls: [
        {
          event_id: "evt-3",
          kind: "tool",
          tool: "datahub_get_entity",
          summary: "urn:li:dataset:(sales.quarterly)",
          outcome: "success",
          duration_ms: 210,
          timestamp: "2026-08-16T10:59:00Z",
        },
      ],
    },
  ],
};

// Twelve calls to one operation, told apart by the request each made (#1423).
const apiCalls: Provenance = {
  session_id: "dpx_watch",
  captures: [
    {
      tool: "manage_asset",
      captured_at: "2026-08-22T06:00:10Z",
      version: 9,
      calls: [
        {
          event_id: "evt-w1",
          kind: "api",
          tool: "api_invoke_endpoint",
          connection: "nws",
          method: "GET",
          path: "/gridpoints/LWX/96%2C72/forecast",
          operation_id: "getGridpointForecast",
          request: "GET /gridpoints/LWX/96%2C72/forecast",
          outcome: "success",
          duration_ms: 10,
          timestamp: "2026-08-22T06:00:04Z",
        },
        {
          event_id: "evt-w2",
          kind: "api",
          tool: "api_invoke_endpoint",
          connection: "util",
          operation_id: "fetch_url",
          request:
            'POST /fetch\n{"url":"https://example.com/outages.json"}',
          outcome: "success",
          duration_ms: 220,
          timestamp: "2026-08-22T06:00:06Z",
        },
      ],
    },
  ],
};

const legacy: Provenance = {
  session_id: "dps_old",
  tool_calls: [
    {
      tool_name: "trino_query",
      timestamp: "2026-01-01T00:00:00Z",
      parameters: { sql: "SELECT 1" },
    },
  ],
};

afterEach(cleanup);

/** Open the disclosure the earlier captures sit behind. */
function openEarlier(label: string) {
  fireEvent.click(screen.getByRole("button", { name: label }));
}

/** Open one earlier capture, by the version it produced. */
function openCapture(heading: RegExp) {
  fireEvent.click(screen.getByRole("button", { name: heading }));
}

describe("ProvenancePanel", () => {
  it("leads with the newest capture and counts every capture's calls", () => {
    render(<ProvenancePanel provenance={captured} />);

    // Version 2 is the last capture in the fixture, so it is the newest write.
    expect(screen.getByText("Version 2")).toBeInTheDocument();
    expect(screen.getByText("Cited")).toBeInTheDocument();
    // The truncated capture here is a cited one, where truncation means an id
    // the agent named resolved to no call of theirs — not that the platform
    // stopped short of a longer window.
    expect(
      screen.getByText("Some cited calls were not found and are not recorded."),
    ).toBeInTheDocument();

    // Version 1 is behind the disclosure, but its two calls are still counted.
    expect(screen.queryByText("Version 1")).not.toBeInTheDocument();
    expect(screen.getByText("3 calls")).toBeInTheDocument();

    openEarlier("1 earlier capture");
    expect(
      screen.getByRole("button", { name: /Version 1/ }),
    ).toBeInTheDocument();
    expect(screen.getByText("3 calls")).toBeInTheDocument();
  });

  // A page a scheduled script refreshes hourly gets one capture an hour. All of
  // them expanded made the sidebar scroll for as long as the asset had existed.
  it("collapses many earlier captures behind one disclosure, each opening alone", () => {
    const hourly: Provenance = {
      captures: Array.from({ length: 30 }, (_, i) => ({
        tool: "manage_asset",
        captured_at: `2026-08-16T${String(i % 24).padStart(2, "0")}:00:00Z`,
        version: i + 1,
        calls: [
          {
            event_id: `evt-${i}`,
            kind: "sql" as const,
            tool: "trino_query",
            statement: `SELECT ${i}`,
            outcome: "success" as const,
            timestamp: `2026-08-16T${String(i % 24).padStart(2, "0")}:00:00Z`,
          },
        ],
      })),
    };
    render(<ProvenancePanel provenance={hourly} />);

    // Only the newest capture's heading and calls are on the page.
    expect(screen.getByText("Version 30")).toBeInTheDocument();
    expect(screen.getAllByText("Trino Query")).toHaveLength(1);
    expect(screen.getByText("SELECT 29")).toBeInTheDocument();
    expect(screen.getByText("30 calls")).toBeInTheDocument();

    openEarlier("29 earlier captures");

    // Every earlier capture is now listed, newest first, and still collapsed.
    const headings = screen.getAllByRole("button", { name: /Version \d+/ });
    expect(headings).toHaveLength(29);
    expect(headings[0]).toHaveAccessibleName(/Version 29/);
    expect(headings[28]).toHaveAccessibleName(/Version 1/);
    expect(screen.getAllByText("Trino Query")).toHaveLength(1);

    // Opening one shows that capture's heading and its calls, and only those.
    openCapture(/Version 17/);
    expect(screen.getAllByText("Trino Query")).toHaveLength(2);
    expect(screen.getByText("SELECT 16")).toBeInTheDocument();
    expect(screen.queryByText("SELECT 15")).not.toBeInTheDocument();
  });

  it("offers no disclosure when the asset has a single capture", () => {
    const one: Provenance = { captures: [captured.captures![0]!] };
    render(<ProvenancePanel provenance={one} />);

    expect(screen.getByText("Version 1")).toBeInTheDocument();
    expect(screen.getByText("Trino Query")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /earlier capture/ }),
    ).not.toBeInTheDocument();
  });

  // An export's capture holds the window it swept up AND the statement it
  // streamed. Only the second one produced the file, and the catalog reads that
  // distinction, so the panel has to show it (#1353).
  it("marks the call a windowed capture named as its source", () => {
    const exported: Provenance = {
      captures: [
        {
          tool: "trino_export",
          captured_at: "2026-08-16T10:05:00Z",
          version: 1,
          event_ids: ["evt-window", "evt-streamed"],
          calls: [
            {
              event_id: "evt-window",
              kind: "sql",
              tool: "trino_query",
              statement: "SELECT 1",
              outcome: "success",
              timestamp: "2026-08-16T10:00:00Z",
            },
            {
              event_id: "evt-streamed",
              kind: "sql",
              tool: "trino_export",
              statement: "SELECT region, revenue FROM sales.quarterly",
              outcome: "success",
              cited: true,
              timestamp: "2026-08-16T10:05:00Z",
            },
          ],
        },
      ],
    };
    render(<ProvenancePanel provenance={exported} />);

    const badges = screen.getAllByText("Source");
    expect(badges).toHaveLength(1);
    const card = badges[0]!.closest("button");
    expect(card).not.toBeNull();
    expect(within(card!).getByText("Trino Export")).toBeInTheDocument();
  });

  // On a capture the caller named wholesale, the capture-level badge already
  // says it; repeating it on every call would be noise.
  it("does not repeat the source badge on a wholly cited capture", () => {
    render(<ProvenancePanel provenance={captured} />);

    expect(screen.getByText("Cited")).toBeInTheDocument();
    expect(screen.queryByText("Source")).not.toBeInTheDocument();
  });

  it("says what truncation means when the platform chose the window", () => {
    const wide: Provenance = {
      captures: [
        {
          tool: "save_asset",
          captured_at: "2026-08-16T10:05:00Z",
          version: 1,
          truncated: true,
          calls: [
            {
              event_id: "evt-1",
              kind: "sql",
              tool: "trino_query",
              statement: "SELECT 1",
              outcome: "success",
              timestamp: "2026-08-16T10:00:00Z",
            },
          ],
        },
      ],
    };
    render(<ProvenancePanel provenance={wide} />);
    expect(
      screen.getByText("More calls were made than this capture records."),
    ).toBeInTheDocument();
  });

  it("shows each call's kind, purpose, and failure", () => {
    render(<ProvenancePanel provenance={captured} />);
    openEarlier("1 earlier capture");
    openCapture(/Version 1/);

    expect(screen.getByText("SQL")).toBeInTheDocument();
    expect(screen.getByText("API")).toBeInTheDocument();
    expect(screen.getByText("Tool")).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(
      screen.getByText("Totalling Q3 revenue by region for the board deck."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("SELECT region, revenue FROM sales.quarterly"),
    ).toBeInTheDocument();
    expect(screen.getByText("GET /v1/accounts")).toBeInTheDocument();
  });

  it("opens a call and shows why it ran, how it ended, and how to cite it", () => {
    render(<ProvenancePanel provenance={captured} />);
    openEarlier("1 earlier capture");
    openCapture(/Version 1/);

    fireEvent.click(screen.getByText("Trino Query"));
    const dialog = screen.getByRole("dialog");

    expect(within(dialog).getByText("Stated purpose")).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        "Failed: TABLE_NOT_FOUND: sales.quarterly",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("SQL Query")).toBeInTheDocument();
    expect(within(dialog).getByText("mcp:call:evt-1")).toBeInTheDocument();
  });

  it("copies the statement of the call it opened", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });

    render(<ProvenancePanel provenance={captured} />);
    openEarlier("1 earlier capture");
    openCapture(/Version 1/);
    fireEvent.click(screen.getByText("Api Invoke Endpoint"));
    fireEvent.click(screen.getByLabelText("Copy request"));

    expect(writeText).toHaveBeenCalledWith("GET /v1/accounts");
  });

  // Twelve rows reading "fetch_url" said which tool ran and nothing about what
  // it did. Each one now carries the request it made.
  it("tells apart two calls by the request each made", () => {
    render(<ProvenancePanel provenance={apiCalls} />);

    expect(
      screen.getByText("GET /gridpoints/LWX/96%2C72/forecast"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/POST \/fetch.*outages\.json/),
    ).toBeInTheDocument();
  });

  it("copies the whole request, body included, from the call it opened", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });

    render(<ProvenancePanel provenance={apiCalls} />);
    fireEvent.click(screen.getByText(/POST \/fetch/));
    fireEvent.click(screen.getByLabelText("Copy request"));

    expect(writeText).toHaveBeenCalledWith(
      'POST /fetch\n{"url":"https://example.com/outages.json"}',
    );
  });

  it("renders an asset saved before provenance was captured by reference", () => {
    render(<ProvenancePanel provenance={legacy} />);

    expect(screen.getByText("1 call")).toBeInTheDocument();
    expect(screen.getByText("Trino Query")).toBeInTheDocument();
    expect(screen.getByText("SELECT 1")).toBeInTheDocument();
  });

  it("leads to the session when the reader may open it", () => {
    const onOpenSession = vi.fn();
    render(
      <ProvenancePanel provenance={captured} onOpenSession={onOpenSession} />,
    );

    fireEvent.click(screen.getByText("Open session"));
    expect(onOpenSession).toHaveBeenCalled();
  });

  it("says so when an asset captured nothing", () => {
    render(<ProvenancePanel provenance={{ session_id: "dps_abc" }} />);
    expect(
      screen.getByText("No provenance data available."),
    ).toBeInTheDocument();
  });
});
