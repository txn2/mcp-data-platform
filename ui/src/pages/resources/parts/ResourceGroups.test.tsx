import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, within } from "@testing-library/react";
import { fakeLocalStorage } from "@/test/localStorage";
import type { Resource } from "@/api/resources/types";
import { ResourceGroups } from "./ResourceGroups";
import { TILE_INLINE_LIMIT } from "./groups";

// A library holds datasets and photographs as well as documents. What is
// asserted here is what the reader gets for that: a section per category that
// says how much is in it and folds, images shown as images, and an image too
// large to stand in for itself left as a placeholder rather than downloaded
// whole (#1471).

function resource(overrides: Partial<Resource> = {}): Resource {
  return {
    id: "res-1",
    scope: "user",
    scope_id: "analyst@example.com",
    category: "playbooks",
    filename: "runbook.md",
    display_name: "Runbook",
    description: "",
    mime_type: "text/markdown",
    size_bytes: 64,
    s3_key: "k",
    uri: "mcp://resources/analyst/runbook.md",
    tags: [],
    uploader_sub: "analyst@example.com",
    uploader_email: "analyst@example.com",
    created_at: "2026-08-03T10:00:00Z",
    updated_at: "2026-08-17T10:00:00Z",
    ...overrides,
  };
}

const photo = (id: string, extra: Partial<Resource> = {}) =>
  resource({
    id,
    category: "visual",
    display_name: id,
    filename: `${id}.png`,
    mime_type: "image/png",
    ...extra,
  });

function show(
  resources: Resource[],
  opts: { admin?: boolean; complete?: boolean } = {},
) {
  const onOpen = vi.fn<(resource: Resource) => void>();
  render(
    <ResourceGroups
      resources={resources}
      admin={opts.admin ?? false}
      complete={opts.complete ?? true}
      onOpen={onOpen}
    />,
  );
  return onOpen;
}

const header = (category: string) => screen.getByTestId(`resource-group-toggle-${category}`);
const tile = (id: string) => screen.getByTestId(`resource-tile-${id}`);

beforeEach(() => vi.stubGlobal("localStorage", fakeLocalStorage()));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("the library is one section per category", () => {
  it("heads each section with its name, its count, and what the category is for", () => {
    show([resource({ id: "a" }), resource({ id: "b" }), photo("c")]);

    expect(header("playbooks").textContent).toContain("playbooks");
    expect(header("playbooks").textContent).toContain("2");
    expect(header("playbooks").textContent).toContain("procedures the agent should follow");

    expect(header("visual").textContent).toContain("1");
  });

  // The sections are built from the pages loaded so far, so a bare number would
  // present "how many have arrived" as "how many there are".
  it("marks a count taken off a partly-loaded library as a floor", () => {
    show([resource({ id: "a" })], { complete: false });
    expect(header("playbooks").textContent).toContain("1+");
  });

  it("folds a section without hiding that it is there", () => {
    show([resource({ id: "a" })]);
    expect(screen.getByText("Runbook")).toBeTruthy();

    fireEvent.click(header("playbooks"));

    expect(screen.queryByText("Runbook")).toBeNull();
    expect(header("playbooks").getAttribute("aria-expanded")).toBe("false");
    expect(header("playbooks").textContent).toContain("1");
  });

  // A library with one large group and several small ones has to open the way
  // it was left, or the folding is undone by every navigation.
  it("opens folded where the reader left it folded", () => {
    show([resource({ id: "a" })]);
    fireEvent.click(header("playbooks"));

    cleanup();
    show([resource({ id: "a" })]);

    expect(header("playbooks").getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText("Runbook")).toBeNull();
  });
});

describe("a section of images is shown as images", () => {
  it("renders tiles rather than rows", () => {
    show([photo("a"), photo("b")]);

    expect(tile("a")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });

  // Driven by what the section holds, not by what it is called: one written
  // note among the photographs and the section is a table again.
  it("renders rows for a section holding anything that is not an image", () => {
    show([photo("a"), resource({ id: "b", category: "visual" })]);

    expect(screen.queryByTestId("resource-tile-a")).toBeNull();
    expect(screen.getByRole("table")).toBeTruthy();
  });

  it("opens the resource the tile stands for", () => {
    const onOpen = show([photo("a")]);
    fireEvent.click(within(tile("a")).getByRole("button"));
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: "a" }));
  });

  // The administrator's library spans every scope and is read to find dead
  // weight. A tile that dropped the scope and the never-read flag would make an
  // image section the one place those answers go missing.
  it("carries the admin's own signals on a tile", () => {
    const old = new Date(Date.now() - 60 * 86_400_000).toISOString();
    show([photo("a", { created_at: old, scope: "global", scope_id: "" })], { admin: true });

    expect(within(tile("a")).getByText("Global")).toBeTruthy();
    expect(screen.getByTestId("resource-tile-never-read-a")).toBeTruthy();
  });

  it("leaves them off the reader's own library, which has neither column", () => {
    const old = new Date(Date.now() - 60 * 86_400_000).toISOString();
    show([photo("a", { created_at: old })]);

    expect(screen.queryByTestId("resource-tile-never-read-a")).toBeNull();
  });

  // A resource has no stored thumbnail, so a tile is the whole object. Past the
  // cutoff the tile stands in for the image and asks for nothing until the
  // resource itself is opened.
  it("stands in for an image past the tile size cutoff instead of loading it", () => {
    show([
      photo("small", { size_bytes: TILE_INLINE_LIMIT }),
      photo("large", { size_bytes: TILE_INLINE_LIMIT + 1 }),
    ]);

    // preview=1: the library drawing itself is audited as its own surface and
    // does not stamp the resource's last-read time, so browsing a library of
    // photographs cannot clear the never-read flag on every one of them.
    const loaded = tile("small").querySelector("img");
    expect(loaded?.getAttribute("src")).toBe("/api/v1/resources/small/content?preview=1");
    expect(loaded?.getAttribute("loading")).toBe("lazy");

    expect(tile("large").querySelector("img")).toBeNull();
    expect(tile("large").textContent).toContain("2 MB");
  });
});
