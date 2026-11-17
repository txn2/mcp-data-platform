import { describe, it, expect, afterEach, vi } from "vitest";
import { screen } from "@testing-library/react";
import { getResponse } from "msw";
import { handlers } from "@/mocks/handlers";
import { renderWithQuery } from "@/test/applicationHarness";
import { AdminDomainsPage } from "./AdminDomainsPage";

// #2687: the administrator's list of every hostname shows a site domain's role
// beside its kind, for the ingress the system administrator adds.

afterEach(() => vi.unstubAllGlobals());

describe("Admin › Sites › Domains (#2687)", () => {
  it("names the primary and where a redirect domain sends visitors", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string, init?: RequestInit) => {
        const res = await getResponse(handlers, new Request(new URL(input, window.location.origin), init));
        return res ?? new Response("{}", { status: 404 });
      }),
    );
    renderWithQuery(<AdminDomainsPage onNavigate={vi.fn()} />);
    expect(await screen.findByText("Domain · Primary")).toBeInTheDocument();
    expect(screen.getByText("Domain · 301 to example.com")).toBeInTheDocument();
    expect(screen.getByText("Application")).toBeInTheDocument();
  });
});
