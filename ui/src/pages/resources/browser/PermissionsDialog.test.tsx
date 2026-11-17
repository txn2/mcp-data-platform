import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { PermissionsDialog } from "./PermissionsDialog";

const { state } = vi.hoisted(() => ({
  state: {
    list: { data: undefined as unknown, isLoading: false, error: null as unknown },
    add: vi.fn(),
    remove: vi.fn(),
  },
}));
vi.mock("@/api/resources/folderGrants", () => ({
  useFolderGrants: () => state.list,
  useAddFolderGrant: () => ({ mutate: state.add, isPending: false }),
  useRemoveFolderGrant: () => ({ mutate: state.remove, isPending: false }),
}));

// Permissions on a Global folder, for an administrator (#2684).
describe("PermissionsDialog", () => {
  beforeEach(() => {
    state.add.mockReset();
    state.remove.mockReset();
  });

  it("lists own and inherited grants and names the sites the folder serves", () => {
    state.list = {
      isLoading: false,
      error: null,
      data: {
        folder: "reports",
        sites: ["quarterly"],
        grants: [
          { id: "g1", folder: "reports", email: "jane@example.com", created_by: "admin@example.com", created_at: "", inherited: false },
          { id: "g2", folder: "org", persona: "analysts", created_by: "admin@example.com", created_at: "", inherited: true },
        ],
      },
    };
    render(<PermissionsDialog folder="reports" onClose={vi.fn()} />);
    expect(screen.getByText("Permissions on reports")).toBeTruthy();
    expect(screen.getByTestId("permissions-sites").textContent).toContain("quarterly");
    expect(screen.getByText("jane@example.com")).toBeTruthy();
    expect(screen.getByText("Inherited from org")).toBeTruthy();
    expect(screen.getByText("Change it there")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(screen.getByTestId("grant-remove-confirm").textContent).toContain("Remove jane@example.com from reports?");
    const removes = screen.getAllByRole("button", { name: "Remove" });
    fireEvent.click(removes[removes.length - 1]!);
    expect(state.remove).toHaveBeenCalledWith("g1", expect.anything());
  });

  it("says only administrators write an ungranted folder, and adds a persona", () => {
    state.list = { isLoading: false, error: null, data: { folder: "brand", sites: [], grants: [] } };
    render(<PermissionsDialog folder="brand" onClose={vi.fn()} />);
    expect(screen.getByTestId("grants-empty").textContent).toContain("Only administrators write here");
    fireEvent.change(screen.getByLabelText("Kind"), { target: { value: "persona" } });
    fireEvent.change(screen.getByLabelText("Add a person or persona"), { target: { value: "marketing" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(state.add).toHaveBeenCalledWith({ folder: "brand", persona: "marketing" }, expect.anything());
  });

  it("shows a refusal under the field", () => {
    state.list = { isLoading: false, error: null, data: { folder: "reports", sites: [], grants: [] } };
    state.add.mockImplementation((_b: unknown, o: { onError: (e: Error) => void }) =>
      o.onError(new Error("The vendors persona is external, and an external persona never sees Global.")),
    );
    render(<PermissionsDialog folder="reports" onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Add a person or persona"), { target: { value: "v@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(screen.getByTestId("grant-refusal").textContent).toContain("external");
  });
});
