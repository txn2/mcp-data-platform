import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Producer, ProducersResponse } from "@/api/portal/hooks/producers";
import { ProducersPanel } from "./ProducersPanel";

// Nothing on the platform could say what produced a file (#1569): an asset's
// provenance answered which data calls its content was built from, and a
// resource recorded an uploader whose value for a script run was the script's
// NAME. What is asserted here is the panel that closes that on the file's own
// page: it names every writer, separates the one that created the file from the
// ones that have only changed it, links a producer that can still be opened,
// and says so when a script that wrote this file no longer exists.

const ASSET_ID = "asset-q4";

function producer(overrides: Partial<Producer> = {}): Producer {
  return {
    kind: "script",
    id: "script-1",
    label: "daily-sales",
    exists: true,
    created: true,
    first_write_at: "2026-07-01T09:00:00Z",
    last_write_at: new Date(Date.now() - 3 * 3_600_000).toISOString(),
    write_count: 41,
    last_version: 8,
    ...overrides,
  };
}

function body(...producers: Producer[]): ProducersResponse {
  return { data: producers, total: producers.length };
}

// stubApi answers the producer route and rejects anything else, so a route the
// panel starts using shows up as a failure rather than as an empty panel.
function stubApi(response: ProducersResponse | { status: number }) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/producers")) {
        if ("status" in response) {
          return Promise.resolve(new Response("{}", { status: response.status }));
        }
        return Promise.resolve(new Response(JSON.stringify(response), { status: 200 }));
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    }),
  );
}

function renderPanel(props: Partial<React.ComponentProps<typeof ProducersPanel>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ProducersPanel target={{ kind: "asset", id: ASSET_ID }} {...props} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  stubApi(body(producer()));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("what produced a file has a surface", () => {
  it("names the writer, how often it wrote, and that it created the file", async () => {
    renderPanel();
    expect(await screen.findByText("daily-sales")).toBeInTheDocument();
    expect(screen.getByText("Written by (1)")).toBeInTheDocument();
    expect(screen.getByText("created")).toBeInTheDocument();
    expect(screen.getByText(/41 writes, last 3h ago/)).toBeInTheDocument();
  });

  // The case the feature exists for: an hourly script and a person both write
  // the same report, and only one of them created it.
  it("lists a script and a person on one file, distinguishing create from modify", async () => {
    stubApi(
      body(
        producer({ kind: "person", id: "user-a", label: "alice@example.com", created: false, write_count: 1 }),
        producer(),
      ),
    );
    renderPanel();

    expect(await screen.findByText("alice@example.com")).toBeInTheDocument();
    expect(screen.getByText("daily-sales")).toBeInTheDocument();
    expect(screen.getByText("Written by (2)")).toBeInTheDocument();
    expect(screen.getByText("created")).toBeInTheDocument();
    expect(screen.getByText("modified")).toBeInTheDocument();
    expect(screen.getByText(/1 write,/)).toBeInTheDocument();
  });

  it("opens the script that wrote the file", async () => {
    const onNavigate = vi.fn();
    renderPanel({ scriptPath: (id) => `/scripts/${id}`, onNavigate });

    fireEvent.click(await screen.findByText("daily-sales"));
    expect(onNavigate).toHaveBeenCalledWith("/scripts/script-1");
  });

  it("opens the session that wrote the file", async () => {
    const onNavigate = vi.fn();
    stubApi(body(producer({ kind: "session", id: "dps_abcdef012345", label: undefined })));
    renderPanel({ sessionPath: (id) => `/activity/sessions/${id}`, onNavigate });

    fireEvent.click(await screen.findByText("Session dps_abcdef01"));
    expect(onNavigate).toHaveBeenCalledWith("/activity/sessions/dps_abcdef012345");
  });

  // A deleted script leaves its rows behind on purpose, so the panel has to
  // render it rather than fail on it -- and must not offer a link to a page
  // that would answer not-found.
  it("reports a script that no longer exists, without linking to it", async () => {
    const onNavigate = vi.fn();
    stubApi(body(producer({ exists: false, label: "quarterly-rollup" })));
    renderPanel({ scriptPath: (id) => `/scripts/${id}`, onNavigate });

    expect(await screen.findByText("quarterly-rollup")).toBeInTheDocument();
    expect(screen.getByText(/This script no longer exists/)).toBeInTheDocument();
    fireEvent.click(screen.getByText("quarterly-rollup"));
    expect(onNavigate).not.toHaveBeenCalled();
  });

  it("names a producer without linking when the surface offers nowhere to go", async () => {
    renderPanel();
    expect(await screen.findByText("daily-sales")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  // A file written before the relation existed has no producer. Saying
  // "nothing wrote this" would be a lie about a file that plainly exists.
  it("shows nothing at all for a file with no recorded producer", async () => {
    stubApi(body());
    const { container } = renderPanel();
    await new Promise((r) => setTimeout(r, 0));
    expect(container.querySelector("[data-testid='producers-panel']")).toBeNull();
  });

  it("shows nothing when the record cannot be read", async () => {
    stubApi({ status: 500 });
    const { container } = renderPanel();
    await new Promise((r) => setTimeout(r, 0));
    expect(container.querySelector("[data-testid='producers-panel']")).toBeNull();
  });

  it("asks the resource route for a resource", async () => {
    const asked: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        asked.push(String(input));
        return Promise.resolve(
          new Response(JSON.stringify(body(producer())), { status: 200 }),
        );
      }),
    );
    renderPanel({ target: { kind: "resource", id: "res-1" } });

    expect(await screen.findByText("daily-sales")).toBeInTheDocument();
    expect(asked.join(" ")).toContain("/resources/res-1/producers");
  });
});
