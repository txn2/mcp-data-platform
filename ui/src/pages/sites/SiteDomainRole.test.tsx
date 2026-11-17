import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { getResponse } from "msw";
import { handlers } from "@/mocks/handlers";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { renderWithQuery, type SentRequest } from "@/test/applicationHarness";
import { useAuthStore } from "@/stores/auth";
import type { Site, SiteDomain } from "@/api/portal/hooks/sites";
import { SiteDomainsTab } from "./SiteDomains";

// #2687: the primary domain and the domains that redirect to it.

const at = "2026-11-15T12:00:00Z";

function dom(hostname: string, environment: string, over: Partial<SiteDomain> = {}): SiteDomain {
  return { hostname, environment, public: false, primary: false, mode: "serve", created_by: "jane.doe@example.com", created_at: at, ...over };
}

function site(domains: SiteDomain[]): Site {
  return {
    id: "site_marketing", name: "marketing", description: "", owner_email: "jane.doe@example.com", main_environment: "prod",
    environments: [
      { name: "dev", release: 1, requires_approval: false, public_domains: [], internal_view: false, keep_releases: 50, keep_releases_default: true, releases_kept: 1, updated_at: at },
      { name: "stage", release: 1, requires_approval: false, public_domains: [], internal_view: false, keep_releases: 50, keep_releases_default: true, releases_kept: 1, updated_at: at },
      { name: "prod", release: 1, requires_approval: false, public_domains: [], internal_view: false, keep_releases: 50, keep_releases_default: true, releases_kept: 1, updated_at: at },
    ],
    created_by: "jane.doe@example.com", created_at: at, updated_at: at,
    address: "https://marketing.pages.example.net", addresses: {}, pages_addresses: {},
    domains, permissions: { manage: true, own: true, delete: true },
  };
}

// The mock's marketing site: example.com primary on prod, www.example.com
// redirecting to it, go.example.com serving stage.
const marketing = () =>
  site([
    dom("go.example.com", "stage"),
    dom("example.com", "prod", { public: true, primary: true }),
    dom("www.example.com", "prod", { mode: "redirect", redirects_to: "example.com" }),
  ]);

// serve answers every request from the mock handlers and records it.
function serve(): SentRequest[] {
  const sent: SentRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, window.location.origin);
      const body = typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
      sent.push({ method: init?.method ?? "GET", path: url.pathname + url.search, body });
      const res = await getResponse(handlers, new Request(url, init));
      return res ?? new Response(JSON.stringify({ detail: "no handler" }), { status: 404 });
    }),
  );
  return sent;
}

afterEach(() => vi.unstubAllGlobals());

const row = (host: string) => screen.getByText(host).closest("tr")!;

describe("the Domains tab's Role column (#2687)", () => {
  beforeEach(() => {
    resetSitesMock();
    useAuthStore.setState({ user: { email: "jane.doe@example.com", is_admin: true } as never });
  });
  afterEach(cleanup);

  it("shows the primary, where each redirect goes, and the domains that serve", () => {
    renderWithQuery(<SiteDomainsTab site={marketing()} />);
    expect(screen.getByRole("columnheader", { name: "Role" })).toBeInTheDocument();
    expect(within(row("example.com")).getByText("Primary")).toBeInTheDocument();
    expect(within(row("www.example.com")).getByText("Redirect")).toBeInTheDocument();
    expect(within(row("www.example.com")).getByText("301 to example.com")).toBeInTheDocument();
    expect(within(row("go.example.com")).getByText("Serves")).toBeInTheDocument();
  });

  it("offers each domain only the role changes that apply", () => {
    renderWithQuery(<SiteDomainsTab site={marketing()} />);
    const primary = within(row("example.com"));
    expect(primary.queryByRole("button", { name: "Make primary" })).toBeNull();
    expect(primary.queryByRole("button", { name: "Redirect to primary" })).toBeNull();
    const www = within(row("www.example.com"));
    expect(www.getByRole("button", { name: "Make primary" })).toBeInTheDocument();
    expect(www.getByRole("button", { name: "Serve here" })).toBeInTheDocument();
    expect(www.queryByRole("button", { name: "Redirect to primary" })).toBeNull();
    // stage has no primary, so its domain cannot redirect.
    const go = within(row("go.example.com"));
    expect(go.getByRole("button", { name: "Make primary" })).toBeInTheDocument();
    expect(go.queryByRole("button", { name: "Redirect to primary" })).toBeNull();
  });

  it("sends a role change, and shows the server's refusal", async () => {
    const sent = serve();
    renderWithQuery(<SiteDomainsTab site={marketing()} />);
    fireEvent.click(within(row("www.example.com")).getByRole("button", { name: "Serve here" }));
    await waitFor(() =>
      expect(sent).toContainEqual({ method: "PUT", path: "/api/v1/portal/sites/marketing/domains/www.example.com", body: { mode: "serve" } }),
    );
    expect(screen.queryByTestId("domain-role-refused")).toBeNull();
    cleanup();

    // The page believes stage has a primary the server does not hold.
    const stale = site([dom("go.example.com", "stage"), dom("old.example.com", "stage", { primary: true })]);
    renderWithQuery(<SiteDomainsTab site={stale} />);
    fireEvent.click(within(row("go.example.com")).getByRole("button", { name: "Redirect to primary" }));
    expect(await screen.findByTestId("domain-role-refused")).toHaveTextContent("no primary domain to redirect to");
  });

  it("refuses removing a primary a redirect depends on, saying so", async () => {
    serve();
    renderWithQuery(<SiteDomainsTab site={marketing()} />);
    fireEvent.click(within(row("example.com")).getByRole("button", { name: "Remove" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove domain" }));
    expect(await screen.findByText(/is the primary that www\.example\.com redirect to/)).toBeInTheDocument();
  });
});

describe("Add domain's Role choice (#2687)", () => {
  beforeEach(() => {
    resetSitesMock();
    useAuthStore.setState({ user: { email: "jane.doe@example.com", is_admin: true } as never });
  });
  afterEach(cleanup);

  it("offers a redirect to the environment's primary, and adds the domain redirecting", async () => {
    const sent = serve();
    renderWithQuery(<SiteDomainsTab site={marketing()} />);
    fireEvent.click(screen.getByRole("button", { name: "Add domain" }));
    fireEvent.change(screen.getByLabelText("Domain"), { target: { value: "shop.example.com" } });
    expect(screen.getByText(/Takes the place of example\.com, which keeps serving/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("radio", { name: "Redirect to example.com" }));
    const adds = screen.getAllByRole("button", { name: "Add domain" });
    fireEvent.click(adds[adds.length - 1]!);
    await waitFor(() =>
      expect(sent).toContainEqual({
        method: "POST", path: "/api/v1/portal/sites/marketing/domains",
        body: { hostname: "shop.example.com", environment: "prod", mode: "redirect" },
      }),
    );
  });

  it("disables the redirect on an environment with no primary", () => {
    renderWithQuery(<SiteDomainsTab site={site([dom("go.example.com", "stage")])} />);
    fireEvent.click(screen.getByRole("button", { name: "Add domain" }));
    const redirect = screen.getByRole("radio", { name: "Redirect to the primary" });
    expect(redirect).toBeDisabled();
    expect(screen.getByText("prod has no primary domain yet.")).toBeInTheDocument();
  });
});
