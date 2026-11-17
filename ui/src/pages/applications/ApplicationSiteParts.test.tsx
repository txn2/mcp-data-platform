import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { ApplicationDetail } from "@/api/portal/hooks/applications";
import { ApplicationSiteParts } from "./ApplicationSiteParts";
import { ReleaseControls } from "./ApplicationReleaseControls";

const { calls } = vi.hoisted(() => ({
  calls: { mode: vi.fn(), update: vi.fn(), release: vi.fn() },
}));
vi.mock("@/api/portal/hooks/applications", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/hooks/applications")>()),
  useSetSitePartMode: () => ({ mutate: calls.mode, isPending: false }),
  useUpdateApplication: () => ({ mutate: calls.update, isPending: false }),
  useReleaseApplication: () => ({ mutate: calls.release, isPending: false }),
  useRollbackApplication: () => ({ mutate: vi.fn(), isPending: false }),
}));

function detail(): ApplicationDetail {
  return {
    application: {
      id: "app_1", name: "store", description: "d", owner_email: "o@example.com", connection: "", schema: "",
      schema_adopted_version: 0, current_release: 3, contract_version: 3, created_by: "", created_at: "", updated_at: "", parts: [],
    },
    parts: [], current_release: null, pending: [], problems: [], findings: [], releases: [],
    site_parts: [
      { part_id: "web/prod", release_mode: "linked", serves: 22, step: 24, contract: ">=2 <4", compatible: true },
      { part_id: "brand/prod", release_mode: "linked", serves: 9, step: 9, contract: "=2", compatible: false },
      { part_id: "admin/stage", release_mode: "coupled", serves: 31, step: 33, contract: "", compatible: true },
    ],
  } as ApplicationDetail;
}

// The site parts and the release dialog (#2679).
describe("ApplicationSiteParts", () => {
  beforeEach(() => Object.values(calls).forEach((f) => f.mockReset()));

  it("shows each part's mode, release, range and compatibility, and changes a mode", () => {
    render(<ApplicationSiteParts detail={detail()} canEdit />);
    expect(screen.getByTestId("contract-version").textContent).toBe("Contract 3");
    const web = screen.getByTestId("site-part-web/prod");
    expect(web.textContent).toContain("Linked");
    expect(web.textContent).toContain("release 22");
    expect(web.textContent).toContain("Compatible");
    expect(screen.getByTestId("site-part-brand/prod").textContent).toContain("Needs contract =2");
    expect(screen.getByTestId("site-part-admin/stage").textContent).toContain("Any (declares none)");
    fireEvent.click(screen.getAllByRole("button", { name: "Make coupled" })[0]!);
    expect(calls.mode).toHaveBeenCalledWith({ id: "web/prod", release_mode: "coupled" }, expect.anything());
  });

  it("sets the contract version", () => {
    render(<ApplicationSiteParts detail={detail()} canEdit />);
    fireEvent.click(screen.getByRole("button", { name: "Contract version…" }));
    fireEvent.change(screen.getByLabelText("Contract"), { target: { value: "4" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(calls.update).toHaveBeenCalledWith({ description: "d", contract_version: 4 }, expect.anything());
  });

  it("shows nothing editable to a reader", () => {
    render(<ApplicationSiteParts detail={detail()} canEdit={false} />);
    expect(screen.queryByRole("button", { name: "Contract version…" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Make coupled" })).toBeNull();
  });

  it("releases with the linked parts ticked, and over a contract refusal only when told to", () => {
    render(<ReleaseControls detail={detail()} />);
    fireEvent.click(screen.getByRole("button", { name: "Release" }));
    const list = screen.getByTestId("release-site-parts");
    expect(list.textContent).toContain("31 → 33");
    expect(list.textContent).toContain("stays at 22");
    fireEvent.click(screen.getAllByRole("checkbox")[0]!);
    expect(list.textContent).toContain("22 → 24");
    calls.release.mockImplementationOnce((_i: unknown, o: { onError: (e: Error) => void }) =>
      o.onError(new Error("nothing was released: contract 3 breaks a site part")),
    );
    const buttons = screen.getAllByRole("button", { name: "Release" });
    fireEvent.click(buttons[buttons.length - 1]!);
    expect(calls.release).toHaveBeenCalledWith({ note: undefined, move_sites: ["web/prod"], force: false }, expect.anything());
    const again = screen.getAllByRole("button", { name: "Release" });
    expect(again[again.length - 1]).toBeDisabled();
    fireEvent.click(screen.getByLabelText(/Release anyway/));
    fireEvent.click(again[again.length - 1]!);
    expect(calls.release).toHaveBeenLastCalledWith({ note: undefined, move_sites: ["web/prod"], force: true }, expect.anything());
  });
});
