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
    expect(address).toHaveAttribute("title", "marketing.pages.example.net, opens in a new tab");
    expect(address.querySelector(".truncate")).toHaveTextContent("marketing.pages.example.net");
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
    name, release, requires_approval: false, public: false, internal_view: false, keep_releases: 0, keep_releases_default: true, releases_kept: 0, updated_at: "",
  });

  it("folds the environments past the most a row shows into +N, named on hover", () => {
    render(<EnvironmentBadges site={{ environments: [env("prod", 5), env("dev", 7), env("qa", 0), env("stage", 6), env("preview", 2)] }} max={3} />);
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

  it("refuses a two-character name and says why", async () => {
    const input = await typeName("ab");
    const why = screen.getByText(/A two-character name gets addresses like ab--prod, which certificate authorities refuse/);
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAttribute("aria-describedby", why.id);
    expect(screen.getByRole("button", { name: "Create site" })).toBeDisabled();
  });

  it("takes a one-letter name and one of three or more characters", async () => {
    const input = await typeName("a");
    expect(screen.getByRole("button", { name: "Create site" })).toBeEnabled();
    fireEvent.change(input, { target: { value: "abc" } });
    expect(screen.queryByText(/two-character name/)).toBeNull();
    expect(input).not.toHaveAttribute("aria-invalid");
    expect(screen.getByRole("button", { name: "Create site" })).toBeEnabled();
  });
});
