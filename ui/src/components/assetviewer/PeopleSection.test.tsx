import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

const summaryEnabled = vi.fn();
let external = false;
let summary: unknown;

vi.mock("@/api/portal/hooks", () => ({
  useAssetPeople: () => ({
    data: {
      owner: { email: "owner@example.com" },
      contributors: [
        { email: "dana@example.com", versions: 2, last_change: new Date().toISOString() },
        { email: "script:nightly-sync", versions: 1, last_change: new Date().toISOString() },
      ],
    },
  }),
  useAssetShareSummary: (_id: string, enabled: boolean) => {
    summaryEnabled(enabled);
    return { data: summary };
  },
  useDirectoryNames: () => ({ "owner@example.com": "Olive Owner", "dana@example.com": "Dana Doe" }),
}));

vi.mock("@/hooks/usePortalAccess", () => ({ usePortalAccess: () => ({ external }) }));

import { PeopleSection } from "./PeopleSection";

function many(n: number) {
  return Array.from({ length: n }, (_, i) => ({ email: `p${String(i).padStart(2, "0")}@example.com`, permission: "viewer" }));
}

beforeEach(() => {
  external = false;
  summaryEnabled.mockReset();
  summary = {
    total: 4,
    personas: [{ persona: "finance", permission: "viewer" }],
    people: [{ email: "lee@example.com", permission: "editor", expires_at: "2026-12-01T00:00:00Z" }, { email: "kim@example.com", permission: "viewer" }],
    links: [{ access_mode: "public", created_by: "owner@example.com", opened: 41 }],
  };
});

describe("PeopleSection (#2531)", () => {
  it("names the owner and the contributors with their counts", () => {
    render(<PeopleSection assetId="a1" />);
    expect(screen.getByTestId("asset-owner")).toHaveTextContent("Olive Owner");
    expect(screen.getByTestId("asset-owner")).toHaveTextContent("Owner");
    const contributors = screen.getByTestId("asset-contributors");
    expect(contributors).toHaveTextContent("Dana Doe");
    expect(contributors).toHaveTextContent("2 versions");
    expect(contributors).toHaveTextContent("Automation nightly-sync");
  });

  it("keeps Shared with closed, and opened lists personas, people and links by kind", () => {
    render(<PeopleSection assetId="a1" />);
    const toggle = screen.getByRole("button", { name: /Shared with 4/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveTextContent("1 persona · 2 people · 1 link");
    fireEvent.click(toggle);
    const block = screen.getByTestId("asset-shared-with");
    expect(block).toHaveTextContent("finance");
    expect(block).toHaveTextContent("Editor · expires");
    expect(block).toHaveTextContent("Public link");
    expect(block).toHaveTextContent("Created by Olive Owner · 41 opens");
    expect(block).toHaveTextContent("Link addresses are shown only to the owner, in Share.");
  });

  it("filters a long list and shows the rest on request", () => {
    summary = { total: 40, personas: [], people: many(40), links: [] };
    render(<PeopleSection assetId="a1" />);
    fireEvent.click(screen.getByRole("button", { name: /Shared with 40/ }));
    expect(screen.getByText("Show 30 more")).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText("Filter by name or address"), { target: { value: "p07" } });
    expect(screen.getByText("p07@example.com")).toBeInTheDocument();
    expect(screen.queryByText("p08@example.com")).toBeNull();
  });

  it("shows an external reader no Shared with block and never asks for it", () => {
    external = true;
    render(<PeopleSection assetId="a1" />);
    expect(screen.getByTestId("asset-owner")).toBeInTheDocument();
    expect(screen.queryByTestId("asset-shared-with")).toBeNull();
    expect(summaryEnabled).not.toHaveBeenCalled();
  });
});
