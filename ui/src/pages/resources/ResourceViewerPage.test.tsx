import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAuthStore, type UserProfile } from "@/stores/auth";
import type { Resource } from "@/api/resources/types";
import { ResourceViewerPage } from "./ResourceViewerPage";

// A managed resource used to open in a dialog, where its preview was capped at
// half the viewport and the reader could not link to it (#1470). What is
// asserted here is the page that replaced it: it reads the resource it is
// addressed by, it carries the sections the dialog carried, it offers Download,
// Edit and Delete under the rule the dialog applied, and an id that names no
// resource says so rather than rendering an empty page.

const UPLOADER = "rachel-analyst";

const RESOURCE: Resource = {
  id: "res-1",
  scope: "persona",
  scope_id: "inventory-analyst",
  category: "references",
  filename: "seasonal-factors.csv",
  display_name: "Seasonal Factors",
  description: "Monthly demand multipliers.",
  mime_type: "text/csv",
  size_bytes: 64,
  s3_key: "resources/persona/inventory-analyst/references/seasonal-factors.csv",
  uri: "mcp://resources/inventory-analyst/seasonal-factors.csv",
  tags: ["demand"],
  uploader_sub: UPLOADER,
  uploader_email: "rachel.thompson@example.com",
  created_at: "2026-08-03T10:00:00Z",
  updated_at: "2026-08-17T10:00:00Z",
  usage: { reads_30d: 7, reads_90d: 19 },
};

const CSV = "month,factor\njanuary,0.82\nfebruary,0.91\n";

// stubApi answers every read the page makes, most specific path first. Anything
// unrecognized rejects, so a route the page starts using without this test
// knowing shows up as a failure rather than as a silent empty panel.
function stubApi(detail: Resource | null) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      const json = (body: unknown, status = 200) =>
        Promise.resolve(new Response(JSON.stringify(body), { status }));

      if (url.endsWith("/content")) {
        return Promise.resolve(new Response(CSV, { status: 200 }));
      }
      if (url.endsWith("/versions")) {
        return json({
          current: 2,
          max_versions: 10,
          versions: [
            {
              resource_id: "res-1",
              version: 2,
              mime_type: "text/csv",
              size_bytes: 64,
              s3_key: "k2",
              uploader_sub: UPLOADER,
              uploader_email: "rachel.thompson@example.com",
              created_at: "2026-08-17T10:00:00Z",
            },
          ],
        });
      }
      if (url.endsWith("/tables")) return json({ registrations: [] });
      if (url.endsWith("/table-connections")) {
        return json({
          connections: [{ name: "warehouse", catalog: "scratch", schema: "uploads" }],
        });
      }
      if (url.endsWith("/assets")) {
        return json({
          data: [{ id: "asset-q4", name: "Q4 report", public: true }],
          total: 1,
          hidden: 0,
        });
      }
      if (url.endsWith("/prompts")) {
        return json({ data: [{ id: "p-1", name: "restock", display_name: "Restock", scope: "user" }] });
      }
      if (url.includes("/api/v1/resources/")) {
        return detail ? json(detail) : json({ error: "not found" }, 404);
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    }),
  );
}

function signIn(overrides: Partial<UserProfile> = {}) {
  useAuthStore.setState({
    user: {
      user_id: UPLOADER,
      email: "rachel.thompson@example.com",
      roles: ["dp_analyst"],
      is_admin: false,
      persona: "inventory-analyst",
      ...overrides,
    },
  });
}

function renderPage(props: { admin?: boolean; onBack?: () => void } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ResourceViewerPage
        resourceId="res-1"
        admin={props.admin}
        onBack={props.onBack ?? (() => {})}
      />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  // jsdom has no matchMedia; the loading indicator reads it for theme detection.
  window.matchMedia = vi.fn().mockReturnValue({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }) as unknown as typeof window.matchMedia;
  signIn();
  stubApi(RESOURCE);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  useAuthStore.setState({ user: null });
});

describe("a managed resource opens as a page", () => {
  it("renders the resource's content in one scroll region, not two nested ones", async () => {
    renderPage();
    const content = await screen.findByTestId("resource-content");
    expect(await screen.findByText("january")).toBeTruthy();
    // The dialog wrapped this same renderer in a max-h-[50vh] scrolling box,
    // itself inside the scrolling column that held every other section. On the
    // page nothing between the content and the document caps its height or
    // scrolls it: the portal's page area is the one scroll region.
    const capped: string[] = [];
    for (let el = content.parentElement; el; el = el.parentElement) {
      if (/max-h-|overflow-y-auto|overflow-auto/.test(el.className)) capped.push(el.className);
    }
    expect(capped).toEqual([]);
  });

  it("carries the sections the dialog carried", async () => {
    renderPage();
    expect(await screen.findByTestId("resource-usage")).toBeTruthy();
    expect(await screen.findByTestId("resource-versions")).toBeTruthy();
    expect(await screen.findByTestId("resource-used-by-prompts")).toBeTruthy();
    // Beside the prompts list, the assets whose content references this file
    // (#1475): the same question asked of a different consumer.
    expect(await screen.findByTestId("resource-used-by-assets")).toBeTruthy();
    expect(await screen.findByText("Query as a table")).toBeTruthy();
    expect(screen.getByText(RESOURCE.uri)).toBeTruthy();
  });

  it("says so when the id names no resource", async () => {
    stubApi(null);
    renderPage();
    expect(await screen.findByTestId("resource-not-found")).toBeTruthy();
    expect(screen.getByText("Resource not found")).toBeTruthy();
  });

  it("returns to the library from the not-found page", async () => {
    stubApi(null);
    const onBack = vi.fn();
    renderPage({ onBack });
    fireEvent.click(await screen.findByRole("button", { name: "Back" }));
    expect(onBack).toHaveBeenCalled();
  });
});

describe("what the page lets the reader do to the resource", () => {
  it("offers Download, Edit and Delete to the uploader", async () => {
    renderPage();
    expect(await screen.findByRole("button", { name: "Download" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Edit" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete" })).toBeTruthy();
  });

  it("offers only Download to somebody else's reader", async () => {
    signIn({ user_id: "someone-else", email: "someone.else@example.com" });
    renderPage();
    expect(await screen.findByRole("button", { name: "Download" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
  });

  it("offers all three to an administrator reading somebody else's resource", async () => {
    signIn({ user_id: "operator", email: "operator@example.com", is_admin: true });
    renderPage({ admin: true });
    expect(await screen.findByRole("button", { name: "Edit" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete" })).toBeTruthy();
  });

  it("leaves the page once the resource it addresses is deleted", async () => {
    const onBack = vi.fn();
    renderPage({ onBack });
    fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(onBack).toHaveBeenCalled());
  });
});
