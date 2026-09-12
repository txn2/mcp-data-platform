import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import type { APIKeySummary } from "@/api/admin/types";
import { KeysTable } from "./KeysTable";
import { CreatedKeyBanner } from "./CreatedKeyBanner";

afterEach(cleanup);

function renderTable(keys: APIKeySummary[]) {
  render(
    <KeysTable
      keys={keys}
      isReadOnly={false}
      deleteConfirm={null}
      deleting={false}
      onRequestDelete={vi.fn()}
      onCancelDelete={vi.fn()}
      onConfirmDelete={vi.fn()}
    />,
  );
}

// A key whose roles reach no persona authenticates and lists no tools, so the
// listing's no_persona flag has to be visible on its row (#1705).
describe("KeysTable persona resolution", () => {
  it("flags a key whose roles reach no persona and names the persona of one that does", () => {
    renderTable([
      { name: "typo-key", roles: ["admin"], source: "database", no_persona: true },
      { name: "good-key", roles: ["dp_admin"], source: "database", persona: "admin" },
    ]);

    const typoRow = screen.getByText("typo-key").closest("tr")!;
    expect(within(typoRow).getByText("No persona")).toHaveAttribute(
      "title",
      expect.stringContaining("lists no tools"),
    );
    expect(within(typoRow).queryByText(/^as /)).toBeNull();

    const goodRow = screen.getByText("good-key").closest("tr")!;
    expect(within(goodRow).queryByText("No persona")).toBeNull();
    expect(within(goodRow).getByText("as admin")).toBeInTheDocument();
  });
});

describe("CreatedKeyBanner warnings", () => {
  it("shows each warning beside the secret", () => {
    render(
      <CreatedKeyBanner
        response={{
          name: "typo-key",
          key: "mdp_secret",
          roles: ["admin"],
          warning: "Store this key securely. It will not be shown again.",
          warnings: ['No persona carries any of the roles "admin", so this key authenticates and lists no tools.'],
        }}
        onDismiss={vi.fn()}
      />,
    );
    expect(screen.getByRole("note")).toHaveTextContent("lists no tools");
    expect(screen.getByText("mdp_secret")).toBeInTheDocument();
  });

  it("shows no warning for a key that reaches a persona", () => {
    render(
      <CreatedKeyBanner
        response={{
          name: "good-key",
          key: "mdp_secret",
          roles: ["dp_admin"],
          warning: "Store this key securely. It will not be shown again.",
          persona: "admin",
        }}
        onDismiss={vi.fn()}
      />,
    );
    expect(screen.queryByRole("note")).toBeNull();
  });
});
