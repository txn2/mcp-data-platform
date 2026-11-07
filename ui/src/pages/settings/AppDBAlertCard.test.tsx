import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

const save = vi.fn();

vi.mock("@/api/admin/hooks", async () => {
  const { mockAppDBHealthSettings } = await import("@/mocks/handlers/appdb");
  return {
    useAppDBHealthSettings: () => ({ data: mockAppDBHealthSettings }),
    useSaveAppDBHealthSettings: () => ({ mutate: save, isPending: false }),
    useNotificationChannels: () => ({ data: [] }),
  };
});

import { AppDBAlertCard } from "./AppDBAlertCard";

describe("AppDBAlertCard (#2528)", () => {
  it("holds every application database's thresholds and saves an edit", () => {
    render(<AppDBAlertCard isReadOnly={false} />);
    expect(screen.getByText("Application databases")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Sample every (seconds)"), { target: { value: "120" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ interval_seconds: 120 }), expect.anything());
  });

  it("is read-only without a database", () => {
    render(<AppDBAlertCard isReadOnly />);
    expect(screen.getByLabelText("Sample every (seconds)")).toBeDisabled();
    expect(screen.queryByRole("button", { name: /Save/ })).toBeNull();
  });
});
