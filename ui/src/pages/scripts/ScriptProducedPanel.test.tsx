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
      await screen.findByText(/has not written an asset, a collection or a managed resource yet/),
    ).toBeInTheDocument();
  });

  it("lists a collection as a collection and opens it in the collections library", async () => {
    const onNavigate = vi.fn();
    stubApi(
      body(
        item({ target_kind: "collection", target_id: "col-001", name: "Q4 Performance Review" }),
      ),
    );
    renderPanel({ filePath: (kind, id) => `/${kind}s/${id}`, onNavigate });

    expect(await screen.findByText("Q4 Performance Review")).toBeInTheDocument();
    expect(screen.getByText("Collection")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Q4 Performance Review"));
    expect(onNavigate).toHaveBeenCalledWith("/collections/col-001");
  });

  // A transfer that kept the files leaves the script's runs writing into files
  // the script's owner cannot open (#1588, criterion 3). The panel marks each
  // such file with whose it is and says what that means, above the table.
  it("marks a file whose owner is not the script's owner", async () => {
    stubApi(
      body(
        item({ owner_email: "sarah.chen@example.com" }),
        item({
          target_id: "ast-002",
          name: "Weekly Sales",
          owner_email: "Marcus.Webb@example.com",
        }),
        item({ target_kind: "resource", target_id: "res-001", name: "Region map" }),
      ),
    );
    renderPanel({ owner: "marcus.webb@example.com" });

    expect(await screen.findByText("Q4 Revenue Dashboard")).toBeInTheDocument();
    expect(screen.getByText("owned by sarah.chen@example.com")).toBeInTheDocument();
    // The file already the owner's, compared case-insensitively, is not marked;
    // nor is a resource, which records no address.
    expect(screen.queryByText(/owned by marcus/i)).not.toBeInTheDocument();
    expect(screen.getByTestId("script-produced-elsewhere")).toHaveTextContent(
      "One of these files belongs to somebody else. marcus.webb@example.com cannot open, share or delete it, and each run goes on writing a new version into it.",
    );
  });

  it("counts the files that belong to somebody else", async () => {
    stubApi(
      body(
        item({ owner_email: "sarah.chen@example.com" }),
        item({ target_id: "ast-002", name: "Weekly Sales", owner_email: "sarah.chen@example.com" }),
      ),
    );
    renderPanel({ owner: "marcus.webb@example.com" });

    expect(await screen.findByTestId("script-produced-elsewhere")).toHaveTextContent(
      "2 of these files belong to somebody else. marcus.webb@example.com cannot open, share or delete them",
    );
  });

  // Without an owner to compare against nothing is marked: the page passes
  // the script's owner, and a caller that has none has no claim to make.
  it("marks nothing without an owner to compare against", async () => {
    stubApi(body(item({ owner_email: "sarah.chen@example.com" })));
    renderPanel();

    expect(await screen.findByText("Q4 Revenue Dashboard")).toBeInTheDocument();
    expect(screen.queryByText(/owned by/)).not.toBeInTheDocument();
    expect(screen.queryByTestId("script-produced-elsewhere")).not.toBeInTheDocument();
  });

  it("says so when the record cannot be read", async () => {
    stubApi({ status: 500 });
    renderPanel();
    expect(await screen.findByText(/could not be read/)).toBeInTheDocument();
  });
});
