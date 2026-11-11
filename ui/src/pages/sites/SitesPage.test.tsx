import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { getResponse } from "msw";
import { handlers } from "@/mocks/handlers";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { renderWithQuery } from "@/test/applicationHarness";
import { SitesPage } from "./SitesPage";

// #2587: the whole row of the Sites list opens the site's page, from a click
// or from Enter, and the address in it still opens the live site.

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
