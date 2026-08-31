import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ProducedItem, ProducedResponse } from "@/api/portal/hooks/producers";
import { ScriptProducedPanel } from "./ScriptProducedPanel";

// A script's page listed what each RUN wrote and nothing else (#1569), so
// "what does this hourly script actually touch" and "if I retire this, what
// goes stale" had to be answered by reading three hundred run histories -- and
// a file the script modified without declaring it as an output appeared in
// none of them. What is asserted here is the one aggregate list that answers
// both, including the files it wrote that have since been deleted.

const SCRIPT_ID = "script-1";

function item(overrides: Partial<ProducedItem> = {}): ProducedItem {
  return {
    target_kind: "asset",
    target_id: "ast-001",
    name: "Q4 Revenue Dashboard",
    created: true,
    first_write_at: "2026-07-01T09:00:00Z",
    last_write_at: "2026-08-20T09:00:00Z",
    write_count: 41,
    last_version: 8,
    ...overrides,
  };
}

function body(...items: ProducedItem[]): ProducedResponse {
  return { data: items, total: items.length };
}

function stubApi(response: ProducedResponse | { status: number }) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/produced")) {
        if ("status" in response) {
          return Promise.resolve(new Response("{}", { status: response.status }));
        }
        return Promise.resolve(new Response(JSON.stringify(response), { status: 200 }));
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    }),
  );
}

function renderPanel(props: Partial<React.ComponentProps<typeof ScriptProducedPanel>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ScriptProducedPanel scriptId={SCRIPT_ID} {...props} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  stubApi(body(item()));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("everything a script has produced", () => {
  it("lists a file with its kind, its write count and whether the script created it", async () => {
    renderPanel();
    expect(await screen.findByText("Q4 Revenue Dashboard")).toBeInTheDocument();
    expect(screen.getByText("Files written (1)")).toBeInTheDocument();
    expect(screen.getByText("Asset")).toBeInTheDocument();
    expect(screen.getByText("41")).toBeInTheDocument();
    expect(screen.getByText("created")).toBeInTheDocument();
  });

  it("lists assets and managed resources in one list", async () => {
    stubApi(
      body(
        item(),
        item({
          target_kind: "resource",
          target_id: "res-029",
          name: "Warehouse Floor Plan",
          created: false,
          write_count: 4,
        }),
      ),
    );
    renderPanel();

    expect(await screen.findByText("Warehouse Floor Plan")).toBeInTheDocument();
    expect(screen.getByText("Asset")).toBeInTheDocument();
    expect(screen.getByText("Resource")).toBeInTheDocument();
    expect(screen.getByText("modified")).toBeInTheDocument();
  });

  it("opens a produced file on row click", async () => {
    const onNavigate = vi.fn();
    renderPanel({ filePath: (kind, id) => `/${kind}s/${id}`, onNavigate });

    fireEvent.click(await screen.findByText("Q4 Revenue Dashboard"));
    expect(onNavigate).toHaveBeenCalledWith("/assets/ast-001");
  });

  // The row stays after the file is gone: that this script wrote it is still
  // true, and it is what somebody deciding whether to retire the script needs.
  it("keeps a deleted file listed, named by its id, and does not link to it", async () => {
    const onNavigate = vi.fn();
    stubApi(body(item({ target_id: "ast-removed", name: undefined, deleted: true })));
    renderPanel({ filePath: (kind, id) => `/${kind}s/${id}`, onNavigate });

    expect(await screen.findByText("ast-removed")).toBeInTheDocument();
    expect(screen.getByText("deleted")).toBeInTheDocument();
    fireEvent.click(screen.getByText("ast-removed"));
    expect(onNavigate).not.toHaveBeenCalled();
  });

  it("says a script has written nothing yet rather than showing an empty table", async () => {
    stubApi(body());
    renderPanel();
    expect(
      await screen.findByText(/has not written an asset or a managed resource yet/),
    ).toBeInTheDocument();
  });

  it("says so when the record cannot be read", async () => {
    stubApi({ status: 500 });
    renderPanel();
    expect(await screen.findByText(/could not be read/)).toBeInTheDocument();
  });
});
