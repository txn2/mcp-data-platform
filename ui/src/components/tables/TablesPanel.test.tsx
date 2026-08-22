import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TablesPanel } from "./TablesPanel";
import type { TableConnectionList, TableRegistrationList } from "@/api/tables/types";

// What is asserted here is the panel's four states: a file nothing can hold a
// table for, a file with none registered, one registered against the current
// version, and one left behind by a newer version. The last is the state a
// reader cannot discover from the rows themselves, which is why it is on the
// page at all.

const CONNECTIONS: TableConnectionList = {
  connections: [{ name: "scratch", catalog: "scratch", schema: "uploads" }],
};

const REGISTERED: TableRegistrationList = {
  registrations: [
    {
      id: "reg_1",
      source_kind: "asset",
      source_id: "ast-1",
      connection: "scratch",
      catalog: "scratch",
      schema: "uploads",
      table: "analyst_vendor_keys",
      location: "s3://portal-assets/artifacts/u1/ast-1/",
      columns: [
        { name: "store_id", type: "VARCHAR" },
        { name: "rebate_pct", type: "VARCHAR" },
      ],
      registered_by: "alice@example.com",
      registered_at: "2026-08-20T14:12:00Z",
      query_table: "scratch.uploads.analyst_vendor_keys",
      stale: false,
    },
  ],
};

// stubFetch answers the panel's two reads. Each test declares what the two
// routes return; anything else is a failure rather than a silent empty body.
function stubFetch(connections: unknown, registrations: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/table-connections")) {
        return Promise.resolve(new Response(JSON.stringify(connections), { status: 200 }));
      }
      if (url.includes("/tables")) {
        return Promise.resolve(new Response(JSON.stringify(registrations), { status: 200 }));
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    }),
  );
}

function renderPanel(props: Partial<Parameters<typeof TablesPanel>[0]> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <TablesPanel
        kind="asset"
        id="ast-1"
        contentType="text/csv"
        filename="vendor-keys.csv"
        canModify
        {...props}
      />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  stubFetch(CONNECTIONS, { registrations: [] });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("registering a stored file as a table", () => {
  it("is absent for a file that cannot be a table", async () => {
    renderPanel({ contentType: "text/html" });
    // Nothing renders and nothing is fetched: an HTML asset has no header row
    // to take columns from, so the action was never on offer.
    await waitFor(() => {
      expect(screen.queryByText("Query as a table")).not.toBeInTheDocument();
    });
  });

  it("is absent when no connection can hold a table", async () => {
    stubFetch({ connections: [] }, { registrations: [] });
    renderPanel();
    await waitFor(() => {
      expect(screen.queryByText("Query as a table")).not.toBeInTheDocument();
    });
  });

  it("says so when the file is registered nowhere", async () => {
    renderPanel();
    expect(await screen.findByText("Query as a table")).toBeInTheDocument();
    expect(await screen.findByText(/not registered as a table yet/i)).toBeInTheDocument();
  });

  it("names the table, its connection, and its columns", async () => {
    stubFetch(CONNECTIONS, REGISTERED);
    renderPanel();

    expect(await screen.findByText("scratch.uploads.analyst_vendor_keys")).toBeInTheDocument();
    expect(screen.getByText("store_id")).toBeInTheDocument();
    expect(screen.getByText("rebate_pct")).toBeInTheDocument();
    // No stale warning on a table that points at the current version.
    expect(screen.queryByText(/newer version than the table points at/i)).not.toBeInTheDocument();
  });

  it("warns when the file has moved on since the table was registered", async () => {
    stubFetch(CONNECTIONS, {
      registrations: [{ ...REGISTERED.registrations[0], stale: true }],
    });
    renderPanel();

    expect(await screen.findByText(/newer version than the table points at/i)).toBeInTheDocument();
  });

  // Registering is authority over the file, not access to it, so the routes
  // behind this panel answer a reader as if the file had no tables. Showing
  // them the panel would say "not registered as a table yet" about a file that
  // may well be, so the panel is absent for them entirely.
  it("is absent for a reader who cannot modify the file", async () => {
    const requested: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        requested.push(String(input));
        return Promise.resolve(new Response(JSON.stringify(REGISTERED), { status: 200 }));
      }),
    );
    renderPanel({ canModify: false });

    await waitFor(() => {
      expect(screen.queryByText("Query as a table")).not.toBeInTheDocument();
    });
    expect(requested).toHaveLength(0);
  });

  it("opens a form naming where the table will be created", async () => {
    renderPanel();
    fireEvent.click(await screen.findByRole("button", { name: /register/i }));

    expect(await screen.findByLabelText(/connection/i)).toBeInTheDocument();
    expect(screen.getByText(/scratch\.uploads/)).toBeInTheDocument();
    // The placeholder shows what leaving the name empty produces, rather than
    // an unrelated example.
    expect(screen.getByPlaceholderText("vendor_keys")).toBeInTheDocument();
  });
});
