import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AssetVersion } from "@/api/portal/types";
import { VersionHistoryPanel } from "./VersionHistoryPanel";
import { comparable, hasRenderedText, renderedText } from "./VersionCompareDialog";

// Comparing an asset's versions on its page (#2600): each older version against
// the current one, the current one against the one before, any two, the
// rendered words of an HTML asset, and never a version that holds only part
// of its content, nor a binary asset.

const CONTENT: Record<number, string> = {
  1: "<h2>Open items</h2>\n<ul>\n<li>Renewal is due on March 1.</li>\n</ul>\n",
  2: "<h2>Open items</h2>\n<ul>\n<li>Renewal is due on March 1.</li>\n<li>They asked for SSO.</li>\n</ul>\n",
  3: "<h2>Open items</h2>\n<ul>\n<li>Renewal is due on February 15.</li>\n<li>They asked for SSO.</li>\n</ul>\n",
};

function version(n: number, metadata?: Record<string, unknown>): AssetVersion {
  return {
    id: `v${n}`,
    asset_id: "a1",
    version: n,
    s3_key: `k${n}`,
    s3_bucket: "b",
    content_type: "text/html",
    size_bytes: 1024,
    created_by: "dana@example.com",
    change_summary: `Edit ${n}`,
    created_at: "2026-11-10T10:00:00Z",
    metadata,
  };
}

function show(contentType: string, versions: AssetVersion[]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <VersionHistoryPanel versions={versions} currentVersion={3} isLoading={false} compare={{ assetId: "a1", assetName: "Call prep", contentType }} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const m = /\/versions\/(\d+)\/content/.exec(String(input));
      const body = m ? CONTENT[Number(m[1])] : undefined;
      return Promise.resolve(body === undefined ? new Response("missing", { status: 404 }) : new Response(body, { status: 200 }));
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("comparing an asset's versions", () => {
  it("shows only what changed between an older version and the current one, with the counts", async () => {
    show("text/html", [version(3), version(2), version(1)]);
    expect(screen.getByRole("button", { name: "What changed in v3" })).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "Compare with current" })[0]!);
    expect(await screen.findByText("+1 lines")).toBeInTheDocument();
    expect(screen.getByText("−1 lines")).toBeInTheDocument();
    const lines = screen.getByTestId("version-compare-lines").textContent ?? "";
    expect(lines).toContain("- <li>Renewal is due on March 1.</li>");
    expect(lines).toContain("+ <li>Renewal is due on February 15.</li>");
  });

  it("compares any two versions, and the rendered words with the markup left out", async () => {
    show("text/html", [version(3), version(2), version(1)]);
    fireEvent.click(screen.getByRole("button", { name: "Compare two versions" }));
    fireEvent.change(await screen.findByLabelText("From"), { target: { value: "1" } });
    expect(await screen.findByText("+2 lines")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Text" }));
    const lines = screen.getByTestId("version-compare-lines").textContent ?? "";
    expect(lines).toContain("+ Renewal is due on February 15.");
    expect(lines).not.toContain("<li>");
    expect(screen.getByText("The words a reader sees, with the markup left out")).toBeInTheDocument();
  });

  it("says a version holding part of its content cannot be compared", () => {
    show("text/html", [version(3), version(2, { truncated: true, limit_applied: 100000, limit_unit: "rows" }), version(1)]);
    expect(screen.getByText("Cannot be compared: this version holds only part of its content.")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Compare with current" })).toHaveLength(1);
    expect(screen.getByRole("button", { name: "What changed in v3" })).toBeInTheDocument();
  });

  it("offers no compare on a binary asset", () => {
    show("image/png", [version(3), version(2), version(1)]);
    expect(screen.queryByRole("button", { name: "Compare with current" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Compare two versions" })).not.toBeInTheDocument();
  });
});

describe("the compare helpers", () => {
  it("reads text assets as comparable and markup as having rendered text", () => {
    expect(comparable("text/csv")).toBe(true);
    expect(comparable("application/json")).toBe(true);
    expect(comparable("image/svg+xml")).toBe(true);
    expect(comparable("application/pdf")).toBe(false);
    expect(hasRenderedText("text/html")).toBe(true);
    expect(hasRenderedText("text/csv")).toBe(false);
  });

  it("keeps one block a line and drops scripts and styles", () => {
    expect(renderedText("<style>p{}</style><h1>Title</h1><p>One  <b>two</b></p><script>x()</script>line<br>next")).toBe(
      "Title\nOne two\nline\nnext",
    );
  });
});
