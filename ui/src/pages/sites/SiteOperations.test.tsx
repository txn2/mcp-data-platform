import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { renderWithQuery } from "@/test/applicationHarness";
import { useAuthStore } from "@/stores/auth";
import type { Site, SiteEnvironment } from "@/api/portal/hooks/sites";
import { RemoveDialog, RenameDialog, EnvironmentMenu } from "./SiteEnvironmentMenu";
import { RedirectDialog, StatusBadge, StatusBlock, TakeOfflineDialog, redirectedTo } from "./SiteStatus";
import { ReplicasSection, SiteTrafficTab, count, ms, slowest } from "./SiteTraffic";
import { SiteHistoryTab } from "./SiteHistory";
import { serveSites } from "./siteTestServer";

// #2631, #2632: an environment's status on its card and in its dialogs.
// #2630: Rename and Remove. #2647: the Traffic view and the replicas.

const at = "2026-11-14T12:00:00Z";

function env(name: string, over: Partial<SiteEnvironment> = {}): SiteEnvironment {
  return {
    name, release: 3, requires_approval: false, public_domains: [], internal_view: false, keep_releases: 50, keep_releases_default: true, releases_kept: 3, updated_at: at,
    ...over,
  };
}

function site(over: Partial<Site> = {}): Site {
  return {
    id: "site_1", name: "marketing", description: "", owner_email: "owner@example.com", main_environment: "prod",
    environments: [env("dev"), env("stage"), env("prod")], created_by: "owner@example.com", created_at: at, updated_at: at,
    address: "https://marketing.pages.example.net", addresses: {}, pages_addresses: { dev: "https://marketing--dev.pages.example.net", stage: "https://marketing--stage.pages.example.net" },
    domains: [], permissions: { manage: true, own: true, delete: true },
    ...over,
  };
}

function signIn(admin: boolean) {
  useAuthStore.setState({ user: { email: "owner@example.com", is_admin: admin } as never });
}

beforeEach(() => {
  resetSitesMock();
  signIn(false);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("where a redirected request lands (#2632)", () => {
  it("keeps the path below the target, and never the request's host", () => {
    expect(redirectedTo("https://status.example.com", false, "/pricing/?a=1")).toBe("https://status.example.com/");
    expect(redirectedTo("https://status.example.com", true, "/pricing/?a=1")).toBe("https://status.example.com/pricing/?a=1");
    expect(redirectedTo("https://status.example.com", true, "//evil.example/x")).toBe("https://status.example.com/evil.example/x");
    expect(redirectedTo("https://status.example.com/base/", true, "/../x")).toBe("https://status.example.com/base/x");
    expect(redirectedTo("not a url", true, "/x")).toBe("");
  });
});

describe("the status on an environment's card (#2631, #2632)", () => {
  it("badges Online, Offline and Redirecting, and nothing on dev", () => {
    const { rerender } = renderWithQuery(<StatusBadge env={env("prod")} />);
    expect(screen.getByText("Online")).toBeInTheDocument();
    rerender(<StatusBadge env={env("prod", { status: "offline" })} />);
    expect(screen.getByText("Offline")).toBeInTheDocument();
    rerender(<StatusBadge env={env("prod", { status: "redirect" })} />);
    expect(screen.getByText("Redirecting")).toBeInTheDocument();
    rerender(<StatusBadge env={env("dev")} />);
    expect(screen.queryByText(/Online|Offline|Redirecting/)).toBeNull();
  });

  it("shows an offline message as text, when it is back, who, and Bring online", async () => {
    const sent = serveSites();
    const off = env("prod", { status: "offline", message: "Back at 2pm\n<b>soon</b>", back_at: "2026-11-14T14:00:00Z", status_changed_by: "jane@example.com", status_changed_at: at });
    renderWithQuery(<StatusBlock site={site()} env={off} canEdit />);
    const block = screen.getByTestId("status-prod");
    expect(block).toHaveTextContent("<b>soon</b>");
    expect(block.querySelector("b")).toBeNull();
    expect(block).toHaveTextContent("Expected back");
    expect(block).toHaveTextContent("by jane@example.com");
    fireEvent.click(within(block).getByRole("button", { name: "Bring online" }));
    await waitFor(() => expect(sent.some((r) => r.method === "PUT" && JSON.stringify(r.body) === JSON.stringify({ status: "online" }))).toBe(true));
  });

  it("shows where a redirect goes, and no Bring online to someone who cannot change it", () => {
    renderWithQuery(<StatusBlock site={site()} env={env("prod", { status: "redirect", redirect_to: "https://status.example.com", keep_path: true })} canEdit={false} />);
    expect(screen.getByText("https://status.example.com")).toBeInTheDocument();
    expect(screen.getByText("The path and query are kept.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Bring online" })).toBeNull();
  });
});

describe("the Take offline and Redirect dialogs", () => {
  it("takes an environment offline with a message and an expected-back time", async () => {
    const sent = serveSites();
    renderWithQuery(<TakeOfflineDialog site={site()} env={env("prod")} onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "Back at 2pm" } });
    fireEvent.click(screen.getByRole("button", { name: "In an hour" }));
    fireEvent.click(screen.getByRole("button", { name: "Take offline" }));
    await waitFor(() => expect(sent.find((r) => r.method === "PUT")).toBeDefined());
    const body = sent.find((r) => r.method === "PUT")!.body as Record<string, string>;
    expect(body.status).toBe("offline");
    expect(body.message).toBe("Back at 2pm");
    expect(body.back_at).toMatch(/Z$/);
  });

  it("refuses a message past 1,000 characters before sending it", () => {
    renderWithQuery(<TakeOfflineDialog site={site()} env={env("prod")} onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "x".repeat(1001) } });
    expect(screen.getByRole("button", { name: "Take offline" })).toBeDisabled();
  });

  it("previews where /pricing/ and //evil.example/x go, and asks for approval where needed", async () => {
    serveSites();
    renderWithQuery(<RedirectDialog site={site()} env={env("prod", { requires_approval: true })} onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Target"), { target: { value: "https://status.example.com" } });
    fireEvent.click(screen.getByLabelText(/Keep the path/));
    const preview = screen.getByTestId("redirect-preview");
    expect(preview).toHaveTextContent("/pricing/?a=1 → https://status.example.com/pricing/?a=1");
    expect(preview).toHaveTextContent("//evil.example/x → https://status.example.com/evil.example/x");
    expect(screen.getByRole("button", { name: "Ask for approval" })).toBeInTheDocument();
  });

  it("lets an administrator redirect an approval environment at once", () => {
    signIn(true);
    renderWithQuery(<RedirectDialog site={site()} env={env("prod", { requires_approval: true })} onClose={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Redirect" })).toBeInTheDocument();
  });
});

describe("the More menu, Rename and Remove (#2630)", () => {
  it("has no menu on dev, and no Rename or Remove on prod", async () => {
    const { rerender } = renderWithQuery(<EnvironmentMenu env={env("dev")} onAct={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "More for dev" })).toBeNull();
    rerender(<EnvironmentMenu env={env("prod")} onAct={vi.fn()} />);
    fireEvent.pointerDown(screen.getByRole("button", { name: "More for prod" }), { button: 0, ctrlKey: false });
    expect(await screen.findByText("Take offline")).toBeInTheDocument();
    expect(screen.queryByText("Rename")).toBeNull();
    expect(screen.getByText(/can't be renamed or removed/)).toBeInTheDocument();
  });

  it("renames, showing both addresses and the references that stop resolving", async () => {
    const sent = serveSites();
    renderWithQuery(<RenameDialog site={site()} env={env("stage")} onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("New name"), { target: { value: "qa" } });
    expect(screen.getByText("marketing--qa.pages.example.net")).toBeInTheDocument();
    expect(screen.getByText("mcp:site:marketing/stage")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(sent.some((r) => r.method === "PUT" && JSON.stringify(r.body) === JSON.stringify({ rename_to: "qa" }))).toBe(true));
  });

  it("names what must go first, and removes when nothing is in the way", async () => {
    const withDomain = site({ domains: [{ hostname: "stage.example.com", environment: "stage", public: false, created_by: "x", created_at: at }] });
    const { unmount } = renderWithQuery(<RemoveDialog site={withDomain} env={env("stage")} onClose={vi.fn()} />);
    expect(screen.getByTestId("remove-blocked")).toHaveTextContent("stage.example.com");
    expect(screen.queryByRole("button", { name: "Remove stage" })).toBeNull();
    unmount();
    renderWithQuery(<RemoveDialog site={site()} env={env("stage", { requires_approval: true })} onClose={vi.fn()} />);
    expect(screen.getByTestId("remove-blocked")).toHaveTextContent("only an administrator");
    cleanup();
    const sent = serveSites();
    renderWithQuery(<RemoveDialog site={site()} env={env("stage")} onClose={vi.fn()} />);
    expect(screen.getByText(/Shares that name only stage/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Remove stage" }));
    await waitFor(() => expect(sent.some((r) => r.method === "DELETE" && r.path.endsWith("/environments/stage"))).toBe(true));
  });
});

describe("the Traffic view (#2647)", () => {
  it("shows the figures, the charts and each environment", async () => {
    serveSites();
    renderWithQuery(<SiteTrafficTab site={site()} />);
    const figures = await screen.findByTestId("traffic-figures");
    expect(figures).toHaveTextContent("Requests");
    expect(figures).toHaveTextContent("p95");
    expect(screen.getByText("By environment")).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "prod" })).toBeInTheDocument();
  });

  it("names the slowest replica only when it stands out", () => {
    expect(slowest([{ instance: "a", p95_ms: 40 }, { instance: "b", p95_ms: 45 }])).toBe("");
    expect(slowest([{ instance: "a", p95_ms: 40 }, { instance: "b", p95_ms: 45 }, { instance: "c", p95_ms: 120 }])).toBe("c");
    expect(slowest([{ instance: "a", p95_ms: 40 }])).toBe("");
  });

  it("formats latency and counts in a reader's words", () => {
    expect(ms(0)).toBe("–");
    expect(ms(4.8)).toBe("4.8 ms");
    expect(ms(48)).toBe("48 ms");
    expect(ms(2500)).toBe("2.5 s");
    expect(count(1200)).toBe("1,200");
  });

  it("lists each replica for an administrator", async () => {
    serveSites();
    renderWithQuery(<ReplicasSection />);
    const table = await screen.findByTestId("replicas");
    expect(table).toHaveTextContent("platform-7d9f-a:8080");
    expect(table).toHaveTextContent("platform-7d9f-b:8080");
  });
});

describe("the History tab", () => {
  // The server writes from_release and to_release; the table read from and to,
  // fields no answer carries, and showed no release on any row.
  it("shows the releases each move went between", async () => {
    serveSites();
    renderWithQuery(<SiteHistoryTab site={site()} />);
    expect(await screen.findByRole("cell", { name: "11 to 12" })).toBeInTheDocument();
    expect(screen.getAllByRole("cell", { name: "5" }).length).toBeGreaterThan(0);
  });
});
