import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

// Capture what the dialog sends to the create-share mutations, so the tests
// assert on the request rather than on the rendered button alone.
const { createAsset, createColl, createPrompt, revoke } = vi.hoisted(() => ({
  createAsset: { mutate: vi.fn(), isPending: false },
  createColl: { mutate: vi.fn(), isPending: false },
  createPrompt: { mutate: vi.fn(), isPending: false },
  revoke: { mutate: vi.fn() },
}));

vi.mock("@/api/portal/hooks", () => ({
  useShares: () => ({ data: [] }),
  useCollectionShares: () => ({ data: [] }),
  usePromptShares: () => ({ data: [] }),
  useCreateShare: () => createAsset,
  useCreateCollectionShare: () => createColl,
  useCreatePromptShare: () => createPrompt,
  useRevokeShare: () => revoke,
}));

vi.mock("@/components/UserPicker", () => ({
  UserPicker: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <input aria-label="Recipient" value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

import { ShareDialog } from "./ShareDialog";

function renderDialog() {
  return render(
    <ShareDialog target={{ type: "asset", id: "ast-1" }} open onOpenChange={() => {}} />,
  );
}

describe("ShareDialog access modes", () => {
  beforeEach(() => {
    createAsset.mutate.mockClear();
  });

  it("creates a signed-in-users link by default, with no anonymous-access warning", () => {
    renderDialog();

    expect(screen.queryByText(/works without signing in/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /create link/i }));

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ assetId: "ast-1", access_mode: "authenticated" }),
    );
  });

  it("warns that a public link opens without sign-in and sends access_mode public", () => {
    renderDialog();

    fireEvent.change(screen.getByLabelText(/who can open this link/i), {
      target: { value: "public" },
    });
    expect(screen.getByText(/works without signing in/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /create link/i }));

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ access_mode: "public" }),
    );
  });

  it("states that a recipient share opens only for that person", () => {
    renderDialog();

    expect(
      screen.getByText(/only this person can open the link/i),
    ).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Recipient"), {
      target: { value: "bob@example.com" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^share$/i }));

    // No access_mode is sent: the server defaults a named-recipient share to
    // restricted, which is the behavior this section describes.
    expect(createAsset.mutate).toHaveBeenCalledWith({
      assetId: "ast-1",
      shared_with_email: "bob@example.com",
      permission: "viewer",
    });
  });
});
