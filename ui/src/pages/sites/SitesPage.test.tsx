import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { getResponse } from "msw";
import { handlers } from "@/mocks/handlers";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { renderWithQuery } from "@/test/applicationHarness";
import type { SiteEnvironment } from "@/api/portal/hooks/sites";
import { EnvironmentBadges } from "./SiteList";
import { SitesPage } from "./SitesPage";

// #2587: the whole row of the Sites list opens the site's page, from a click
// or from Enter, and the address in it still opens the live site. #2612: every
// row is two lines at the layout the list's width fits, each environment one
// badge, and a phone gets a list.

afterEach(() => {
  vi.unstubAllGlobals();
});

beforeEach(() => {
  resetSitesMock();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const res = await getResponse(handlers, new Request(new URL(input, window.location.origin), init));
      return res ?? new Response(JSON.stringify({ detail: "no handler" }), { status: 404 });
    }),
  );
});

async function row() {
  const onNavigate = vi.fn();
  renderWithQuery(<SitesPage onNavigate={onNavigate} />);
  const found = await screen.findByTestId("site-row-marketing");
  return { found, onNavigate };
}

describe("the Sites list", () => {
  it("opens a site from a click anywhere on its row", async () => {
    const { found, onNavigate } = await row();
    fireEvent.click(found.querySelector("td:nth-child(2)") as HTMLElement);
    expect(onNavigate).toHaveBeenCalledWith("/sites/marketing");
  });

  it("opens a site from Enter on its focused row", async () => {
    const { found, onNavigate } = await row();
    found.focus();
    expect(found).toHaveFocus();
    fireEvent.keyDown(found, { key: "Enter" });
    expect(onNavigate).toHaveBeenCalledWith("/sites/marketing");
  });

  it("leaves the address to open the live site", async () => {
    const { found, onNavigate } = await row();
    const link = found.querySelector("a[target=_blank]") as HTMLAnchorElement;
    expect(link).not.toBeNull();
    fireEvent.click(link);
    expect(onNavigate).not.toHaveBeenCalled();
  });
});

describe("the Sites list's rows", () => {
  it("badges each environment with the release it serves and leaves domains to the site's page", async () => {
    const { found } = await row();
    const r = within(found);
    expect(r.getByText("dev 12")).toBeInTheDocument();
    expect(r.getByText("stage 11")).toBeInTheDocument();
    expect(r.getByTestId("env-badge-prod")).toHaveTextContent("prod 9");
    expect(r.getByTestId("env-badge-prod")).toHaveAttribute("title", expect.stringMatching(/^prod serves release 9, public/));
    expect(r.getByTestId("env-badge-dev")).toHaveAttribute("title", expect.stringMatching(/^dev serves release 12, private/));
    expect(r.queryByText("go.example.com")).toBeNull();
    expect(r.queryByText("www.example.com")).toBeNull();
  });

  it("puts dev first and prod last", async () => {
    const { found } = await row();
    const order = Array.from(found.querySelectorAll('[data-testid^="env-badge-"]')).map((b) => b.getAttribute("data-testid"));
    expect(order).toEqual(["env-badge-dev", "env-badge-stage", "env-badge-prod"]);
  });

  it("keeps the description to one line, whole on hover", async () => {
    const { found } = await row();
    const desc = within(found).getByTestId("site-description");
    expect(desc).toHaveTextContent("Company website");
    expect(desc).toHaveAttribute("title", "Company website");
    expect(desc).toHaveClass("truncate");
  });

  it("names the owner by initials and address, and the address opens on one line", async () => {
    const { found } = await row();
    const owner = within(found).getByRole("img", { name: "jane.doe@example.com" });
    expect(owner).toHaveTextContent("JD");
    const address = within(found).getByTestId("site-address");
    expect(address).toHaveAttribute("title", "example.com, opens in a new tab");
    expect(address.querySelector(".truncate")).toHaveTextContent("example.com");
  });

  it("names the production address, its public domain first, and counts the other domains (#2658)", async () => {
    const { found } = await row();
    expect(within(found).getByTestId("site-address")).toHaveAttribute("href", "https://example.com");
    expect(within(found).getByTestId("site-serves")).toHaveTextContent("serves prod · 1 more domain");
  });

  it("shows the tile of the environment holding the main address, a placeholder until it is drawn (#2658)", async () => {
    const { found } = await row();
    const tile = await within(found).findByTestId(/site-tile/);
    expect(["site-tile-prod", "site-tile-placeholder-prod"]).toContain(tile.getAttribute("data-testid"));
  });
});

describe("searching and paging the Sites list (#2658, #2654)", () => {
  afterEach(() => window.history.replaceState(null, "", "/"));

  it("narrows to a domain on the server, keeps the query in the address and marks the match", async () => {
    await row();
    fireEvent.change(screen.getByRole("searchbox", { name: "Search sites" }), { target: { value: "example.com" } });
    await vi.waitFor(() => expect(window.location.search).toContain("q=example.com"));
    await vi.waitFor(() => expect(screen.getByTestId("sites-pager")).toHaveTextContent("sites match example.com"));
    expect(screen.getByTestId("site-row-marketing").querySelector("mark")).toHaveTextContent("example.com");
  });

  it("reads the query from the address on a reload", async () => {
    window.history.replaceState(null, "", "/?q=marketing");
    renderWithQuery(<SitesPage onNavigate={vi.fn()} />);
    expect(await screen.findByRole("searchbox", { name: "Search sites" })).toHaveValue("marketing");
    await screen.findByTestId("site-row-marketing");
  });

  it("says nothing matched and clears the search", async () => {
    window.history.replaceState(null, "", "/?q=zz-nothing");
    renderWithQuery(<SitesPage onNavigate={vi.fn()} />);
    expect(await screen.findByTestId("sites-none-matched")).toHaveTextContent("No site matches zz-nothing");
    fireEvent.click(screen.getByRole("button", { name: "Clear the search" }));
    await screen.findByTestId("site-row-marketing");
  });

  it("pages a long list", async () => {
    window.history.replaceState(null, "", "/?state=many");
    renderWithQuery(<SitesPage onNavigate={vi.fn()} />);
    const pager = await screen.findByTestId("sites-pager");
    await vi.waitFor(() => expect(pager).toHaveTextContent("1 to 25 of 60 sites"));
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    await vi.waitFor(() => expect(screen.getByTestId("sites-pager")).toHaveTextContent("26 to 50 of 60 sites"));
    expect(screen.getByRole("button", { name: "Previous" })).toBeEnabled();
  });

  it("keeps every header on one line in a column sized for it", async () => {
    await row();
    for (const name of ["Production address", "Requests (24h)", "Environments"]) {
      const head = screen.queryByRole("columnheader", { name });
      if (head) expect(head).toHaveClass("whitespace-nowrap");
    }
  });
});

describe("the Sites list's layout", () => {
  const realObserver = globalThis.ResizeObserver;

  function measureAt(width: number) {
    globalThis.ResizeObserver = class {
      constructor(private readonly cb: ResizeObserverCallback) {}
      observe() {
        this.cb([{ contentRect: { width } } as ResizeObserverEntry], this as unknown as ResizeObserver);
      }
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  }

  afterEach(() => {
    globalThis.ResizeObserver = realObserver;
    cleanup();
  });

  it("shows the owner's address beside the initials with room for it", async () => {
    measureAt(1100);
    const { found } = await row();
    expect(screen.getByTestId("site-table")).toHaveAttribute("data-layout", "wide");
    expect(within(found).getByText("jane.doe@example.com")).toBeInTheDocument();
  });

  it("shows only the owner's initials in the middle width, the address on hover", async () => {
    measureAt(760);
    const { found } = await row();
    expect(screen.getByTestId("site-table")).toHaveAttribute("data-layout", "medium");
    expect(within(found).queryByText("jane.doe@example.com")).toBeNull();
    expect(within(found).getByRole("img", { name: "jane.doe@example.com" })).toHaveAttribute("title", "jane.doe@example.com");
  });

  it("drops the Owner column when narrow and keeps Environments", async () => {
    measureAt(560);
    await row();
    expect(screen.getByTestId("site-table")).toHaveAttribute("data-layout", "narrow");
    expect(screen.queryByRole("columnheader", { name: "Owner" })).toBeNull();
    expect(screen.getByRole("columnheader", { name: "Environments" })).toBeInTheDocument();
  });

  it("is a list where no table fits, and a tap anywhere on a site opens it", async () => {
    measureAt(400);
    const { onNavigate } = await row();
    // The table drawn before the first measurement gives way to the list.
    await screen.findByTestId("site-list");
    expect(screen.queryByTestId("site-table")).toBeNull();
    const found = screen.getByTestId("site-row-marketing");
    expect(within(found).getByText("dev 12")).toBeInTheDocument();
    fireEvent.click(within(found).getByText("Company website"));
    expect(onNavigate).toHaveBeenCalledWith("/sites/marketing");
    fireEvent.click(within(found).getByTestId("site-address"));
    expect(onNavigate).toHaveBeenCalledTimes(1);
  });
});

describe("EnvironmentBadges", () => {
  const env = (name: string, release: number): SiteEnvironment => ({
    name, release, requires_approval: false, public_domains: [], internal_view: false, keep_releases: 0, keep_releases_default: true, releases_kept: 0, updated_at: "",
  });

  it("folds the environments past the most a row shows into +N, named on hover, in the stored order (#2617)", () => {
    render(<EnvironmentBadges site={{ environments: [env("dev", 7), env("preview", 2), env("qa", 0), env("stage", 6), env("prod", 5)] }} max={3} />);
    expect(screen.getByText("dev 7")).toBeInTheDocument();
    expect(screen.getByText("preview 2")).toBeInTheDocument();
    expect(screen.getByText("qa · no release")).toBeInTheDocument();
    const more = screen.getByText("+2");
    expect(more).toHaveAttribute("title", "stage serves release 6, private\nprod serves release 5, private");
    expect(screen.queryByText("prod 5")).toBeNull();
  });

  it("shows every environment with no limit", () => {
    render(<EnvironmentBadges site={{ environments: [env("dev", 1), env("prod", 1)] }} />);
    expect(screen.getByText("dev 1")).toBeInTheDocument();
    expect(screen.getByText("prod 1")).toBeInTheDocument();
    expect(screen.queryByText(/^\+/)).toBeNull();
  });
});

// #2611: a two-character name puts "--" at the 3rd and 4th characters of every
// <site>--<env> address, which DNS reserves, so the dialog refuses it before
// the server does.
describe("the New site dialog", () => {
  async function typeName(name: string) {
    renderWithQuery(<SitesPage onNavigate={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "New site" }));
    const input = await screen.findByLabelText("Name");
    fireEvent.change(input, { target: { value: name } });
    return input;
  }

  // #2636: the person types a name; the server makes the address of it and
  // says why it refuses one. The portal keeps no copy of the rule.
  it("shows the address the server makes of a typed name", async () => {
    await typeName("My Website");
    expect(await screen.findByTestId("site-name-address")).toHaveTextContent("Address: my-website");
    const addresses = await screen.findByTestId("new-site-addresses");
    expect(addresses).toHaveTextContent("my-website--dev.pages.example.net");
    expect(addresses).toHaveTextContent("my-website.pages.example.net");
    expect(screen.getByRole("button", { name: "Create site" })).toBeEnabled();
  });

  it("refuses with the server's reason, invalid and with Create site off", async () => {
    const input = await typeName("ab");
    const why = await screen.findByText(/a site name cannot be two characters/);
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAttribute("aria-describedby", why.id);
    expect(screen.getByRole("button", { name: "Create site" })).toBeDisabled();
  });

  it("keeps Create site off until the answer for the text typed arrives", async () => {
    const input = await typeName("abc");
    expect(screen.getByRole("button", { name: "Create site" })).toBeDisabled();
    await screen.findByTestId("site-name-address");
    expect(screen.getByRole("button", { name: "Create site" })).toBeEnabled();
    fireEvent.change(input, { target: { value: "abcd" } });
    expect(screen.getByRole("button", { name: "Create site" })).toBeDisabled();
  });
});

// #2636: the rule for a site's name lives on the server alone. No portal
// source holds the old pattern, its length or the two-character case.
describe("the portal holds no copy of the site-name rule", () => {
  it("has no site-name pattern, length or two-character check", async () => {
    const files = import.meta.glob("/src/**/*.{ts,tsx}", { query: "?raw", import: "default", eager: true }) as Record<string, string>;
    expect(Object.keys(files).length).toBeGreaterThan(100);
    expect(Object.keys(files)).toContain("/src/pages/sites/SitesPage.tsx");
    for (const [path, text] of Object.entries(files)) {
      if (path.includes(".test.") || path.includes("/mocks/") || path.includes("/generated/")) continue;
      expect(text, path).not.toMatch(/SITE_NAME_RE|siteNameRefusal|\{0,56\}|up to 57/);
    }
  });
});
