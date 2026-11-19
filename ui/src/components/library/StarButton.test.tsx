import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StarButton } from "./StarButton";
import { FavoritesNav } from "@/components/layout/sidebar/FavoritesNav";
import { recordOpen } from "@/api/portal/hooks/library";

// The star and the sidebar list read and write the favorites routes (#2504);
// fetch is stubbed so the requests they make are what is checked.

const calls: { method: string; url: string; body?: string }[] = [];
let favorites: unknown[] = [];

beforeEach(() => {
  calls.length = 0;
  favorites = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ method: init?.method ?? "GET", url, body: init?.body as string | undefined });
      if ((init?.method ?? "GET") === "GET") {
        return new Response(JSON.stringify({ items: favorites }), { status: 200 });
      }
      return new Response(null, { status: 204 });
    }),
  );
});

afterEach(() => vi.unstubAllGlobals());

function renderWith(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("StarButton", () => {
  it("stars an item that is not a favorite", async () => {
    renderWith(<StarButton kind="asset" id="a1" />);
    const btn = await screen.findByRole("button", { name: "Add to favorites" });
    fireEvent.click(btn);
    await waitFor(() => expect(calls.some((c) => c.method === "PUT" && c.url.endsWith("/favorites/asset/a1"))).toBe(true));
  });

  it("removes a favorite, as the round button over a card", async () => {
    favorites = [{ kind: "collection", id: "c1", name: "Finance", at: "2026-11-19T00:00:00Z" }];
    renderWith(<StarButton kind="collection" id="c1" variant="icon" />);
    const btn = await screen.findByRole("button", { name: "Remove from favorites" });
    expect(btn.getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(btn);
    await waitFor(() =>
      expect(calls.some((c) => c.method === "DELETE" && c.url.endsWith("/favorites/collection/c1"))).toBe(true),
    );
  });
});

describe("FavoritesNav", () => {
  it("lists the favorites and opens one", async () => {
    favorites = [
      { kind: "asset", id: "a1", name: "Churn dashboard", at: "2026-11-19T00:00:00Z" },
      { kind: "collection", id: "c1", name: "Finance reports", at: "2026-11-19T00:00:00Z" },
    ];
    const onNavigate = vi.fn();
    renderWith(<FavoritesNav onNavigate={onNavigate} />);
    fireEvent.click(await screen.findByText("Finance reports"));
    expect(onNavigate).toHaveBeenCalledWith("/collections/c1");
    fireEvent.click(screen.getByText("Churn dashboard"));
    expect(onNavigate).toHaveBeenCalledWith("/assets/a1");
  });

  it("renders nothing with no favorites", async () => {
    const { container } = renderWith(<FavoritesNav onNavigate={vi.fn()} />);
    await waitFor(() => expect(calls.length).toBeGreaterThan(0));
    expect(container.textContent).toBe("");
  });
});

describe("recordOpen", () => {
  it("posts the open and swallows a failure", async () => {
    await recordOpen("asset", "a1");
    expect(calls[0]).toMatchObject({ method: "POST" });
    expect(JSON.parse(calls[0]!.body!)).toEqual({ kind: "asset", id: "a1" });

    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ detail: "no" }), { status: 404 })));
    await expect(recordOpen("asset", "missing")).resolves.toBeUndefined();
  });
});
