import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import type { SearchResponse } from "@/api/portal/types";

// The search hook is mocked per-test so the component renders one fixed server
// response; the debounce is bypassed by the query length gate below.
const state: { data: SearchResponse | undefined } = { data: undefined };

vi.mock("@/api/portal/hooks", () => ({
  MIN_SEARCH_LEN: 2,
  useSearch: () => ({ data: state.data, isLoading: false, isError: false }),
}));

vi.mock("@/lib/useDebounced", () => ({
  useDebounced: (value: string) => value,
}));

import { UnifiedSearch } from "./UnifiedSearch";

const NOTICE =
  "2 results are hidden because your persona (analyst) is not granted the connections they " +
  "belong to in catalog and connections. Ask an administrator to grant your persona access " +
  "to the connections you need.";

function renderWith(data: SearchResponse) {
  state.data = data;
  const result = render(<UnifiedSearch onOpen={() => {}} />);
  const input = result.container.querySelector("input");
  if (input) {
    // A query at or above the minimum length activates the result area.
    Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype,
      "value",
    )?.set?.call(input, "orders");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }
  return result;
}

describe("UnifiedSearch withheld results", () => {
  it("explains what the persona hid, with the reason and the remedy", () => {
    const { container } = renderWith({
      groups: [
        { source: "catalog", hits: [{ text: "orders", source: "catalog", ref: "urn:1", score: 1 }] },
      ],
      coverage: [{ source: "catalog", matched: 1, shown: 1, withheld: 1 }],
      count: 1,
      ranking: "lexical",
      withheld_notice: NOTICE,
    });

    const text = container.textContent ?? "";
    expect(text).toContain("2 results are hidden");
    expect(text).toContain("your persona (analyst)");
    expect(text).toContain("Ask an administrator");
    // The per-source count sits on the group header alongside the shown count.
    expect(text).toContain("1 hidden");
  });

  it("shows the notice instead of 'nothing matched' when everything was withheld", () => {
    const { container } = renderWith({
      groups: [],
      coverage: [{ source: "catalog", matched: 0, shown: 0, withheld: 2 }],
      count: 0,
      ranking: "lexical",
      withheld_notice: NOTICE,
    });

    const text = container.textContent ?? "";
    expect(text).toContain("2 results are hidden");
    expect(text).not.toContain("Nothing matched");
  });

  it("says nothing matched when the result is genuinely empty", () => {
    const { container } = renderWith({
      groups: [],
      coverage: [],
      count: 0,
      ranking: "lexical",
    });

    const text = container.textContent ?? "";
    expect(text).toContain("Nothing matched");
    expect(text).not.toContain("hidden");
  });

  it("renders no withheld chrome on an ordinary result", () => {
    const { container } = renderWith({
      groups: [
        { source: "catalog", hits: [{ text: "orders", source: "catalog", ref: "urn:1", score: 1 }] },
      ],
      coverage: [{ source: "catalog", matched: 3, shown: 1 }],
      count: 1,
      ranking: "lexical",
    });

    const text = container.textContent ?? "";
    expect(text).toContain("1 of 3 shown");
    expect(text).not.toContain("hidden");
  });
});
