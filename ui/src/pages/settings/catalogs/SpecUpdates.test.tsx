import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { APIChange } from "@/api/admin/hooks";

const h = vi.hoisted(() => ({
  decide: vi.fn(),
  changes: [] as APIChange[],
  saveWatch: vi.fn(() => Promise.resolve({})),
  watch: undefined as unknown,
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useAPISpecChanges: () => ({ data: { changes: h.changes }, isLoading: false }),
    useDecideAPIChange: () => ({ mutate: h.decide, isPending: false }),
    useAPISpecWatch: () => ({ data: h.watch }),
    useSaveAPISpecWatch: () => ({ mutateAsync: h.saveWatch, isPending: false }),
  };
});

import { renderHook, act } from "@testing-library/react";
import {
  EMPTY_UPDATES,
  SpecChanges,
  SpecUpdatesFields,
  draftOf,
  intervalValue,
  useSpecUpdates,
  watchInput,
} from "./SpecUpdates";

const pending: APIChange = {
  id: "c2", catalog_id: "acme", spec_name: "pets", status: "pending", trigger: "schedule",
  created_at: "2026-11-05T00:00:00Z", summary: "1 added, 1 removed, 0 changed, 0 deprecated",
  report: {
    added: [{ id: "createPet", method: "POST", path: "/pets" }],
    removed: [{ id: "deletePet", method: "DELETE", path: "/pets/{id}", used_by: [
      { kind: "script", id: "s1", name: "nightly", via: "source" },
      { kind: "script", id: "s1", name: "nightly", via: "run" },
    ] }],
    changed: [], deprecated: [],
  },
};

beforeEach(() => {
  h.decide.mockReset();
  h.saveWatch.mockClear();
  h.changes = [];
  h.watch = undefined;
});

describe("the Updates draft", () => {
  it("reads a stored watch and writes what the route takes", () => {
    expect(draftOf(undefined)).toEqual(EMPTY_UPDATES);
    expect(intervalValue("168h0m0s")).toBe("168h");
    expect(intervalValue("")).toBe("off");
    expect(intervalValue("48h0m0s")).toBe("48h0m0s");
    expect(intervalValue("30m0s")).toBe("30m0s");
    const draft = draftOf({ refresh_interval: "24h0m0s", hold_changes: true, upstream_url: "" });
    expect(watchInput(draft, true)).toEqual({ refresh_interval: "24h", hold_changes: true, upstream_url: "" });
    expect(watchInput({ interval: "off", hold: true, upstreamURL: " https://x.example/s.yaml " }, false)).toEqual({
      refresh_interval: "", hold_changes: false, upstream_url: "https://x.example/s.yaml",
    });
  });
});

describe("SpecUpdatesFields", () => {
  it("offers the hold for a URL spec and the upstream URL for an uploaded one", () => {
    const onChange = vi.fn();
    const { rerender } = render(<SpecUpdatesFields draft={EMPTY_UPDATES} onChange={onChange} isURL />);
    expect(screen.getByText("Check for updates")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("checkbox"));
    expect(onChange).toHaveBeenCalledWith({ ...EMPTY_UPDATES, hold: true });
    expect(screen.queryByText("Upstream URL")).not.toBeInTheDocument();

    rerender(
      <SpecUpdatesFields
        draft={{ ...EMPTY_UPDATES, interval: "48h0m0s" }}
        onChange={onChange}
        isURL={false}
        watch={{ refresh_interval: "48h0m0s", hold_changes: false, upstream_url: "", last_error: "fetching the upstream of acme/pets: status 500",
          last_checked_at: "2026-11-05T00:00:00Z", next_check_at: "2026-11-07T00:00:00Z" }}
      />,
    );
    expect(screen.getByText("Upstream URL")).toBeInTheDocument();
    expect(screen.queryByText("Hold changes for review")).not.toBeInTheDocument();
    expect(screen.getByText(/The last check failed/)).toBeInTheDocument();
    expect(screen.getByText(/Next check/)).toBeInTheDocument();
  });
});

describe("SpecChanges", () => {
  it("lists changes, opens a waiting one, and applies or discards it", () => {
    h.changes = [pending, { ...pending, id: "c1", status: "superseded" }];
    render(<SpecChanges catalogID="acme" specName="pets" isReadOnly={false} />);
    expect(screen.getByText("Waiting for review")).toBeInTheDocument();
    expect(screen.getAllByText(/1 added, 1 removed/)).toHaveLength(1);
    expect(screen.getByText("deletePet")).toBeInTheDocument();
    expect(screen.getByText(/used by script nightly$/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(h.decide.mock.calls[0]?.[0]).toEqual({ catalogID: "acme", specName: "pets", changeID: "c2", decision: "apply" });
    fireEvent.click(screen.getByRole("button", { name: "Discard" }));
    expect(h.decide.mock.calls[1]?.[0]).toMatchObject({ decision: "discard" });
    const onError = (h.decide.mock.calls[1]?.[1] as { onError: (e: Error) => void }).onError;
    act(() => onError(new Error("the change is not waiting for review")));
    expect(screen.getByText("the change is not waiting for review")).toBeInTheDocument();
  });

  it("says when there is nothing yet, and hides the decisions read-only", () => {
    const { unmount } = render(<SpecChanges catalogID="acme" specName="pets" isReadOnly={false} />);
    expect(screen.getByText("No changes found yet.")).toBeInTheDocument();
    unmount();
    h.changes = [pending];
    render(<SpecChanges catalogID="acme" specName="pets" isReadOnly />);
    expect(screen.queryByRole("button", { name: "Apply" })).not.toBeInTheDocument();
  });
});

describe("useSpecUpdates", () => {
  it("saves the watch only when the draft changed", async () => {
    h.watch = { refresh_interval: "168h0m0s", hold_changes: false, upstream_url: "" };
    const { result } = renderHook(() => useSpecUpdates("acme", "pets", true));
    await act(() => result.current.save("pets", true));
    expect(h.saveWatch).not.toHaveBeenCalled();
    act(() => result.current.setDraft({ interval: "24h", hold: true, upstreamURL: "" }));
    await act(() => result.current.save("pets", true));
    expect(h.saveWatch).toHaveBeenCalledWith({
      catalogID: "acme", specName: "pets", input: { refresh_interval: "24h", hold_changes: true, upstream_url: "" },
    });
  });
});
