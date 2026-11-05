import { describe, it, expect, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";

// Stable objects: the card resets its draft when the settings object changes,
// so a mock returning a new one per render would loop.
const h = vi.hoisted(() => ({
  save: vi.fn(),
  settings: { data: { channels: ["ops"] }, isLoading: false },
  channels: { data: { channels: [{ name: "ops", enabled: true }, { name: "eng", enabled: true }] }, isLoading: false },
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useAPIChangeSettings: () => h.settings,
    useSaveAPIChangeSettings: () => ({ mutate: h.save, isPending: false }),
    useNotificationChannels: () => h.channels,
  };
});

vi.mock("@/lib/navigationGuard", () => ({ useUnsavedChangesGuard: () => undefined }));

import { ChangeNotificationsCard } from "./ChangeNotificationsCard";

describe("ChangeNotificationsCard", () => {
  it("saves the channels change reports and warnings are posted to", () => {
    render(<ChangeNotificationsCard isReadOnly={false} />);
    expect(screen.getByText("Change notifications")).toBeInTheDocument();
    expect(screen.getByLabelText("Post to ops")).toBeChecked();
    fireEvent.click(screen.getByLabelText("Post to eng"));
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(h.save.mock.calls[0]?.[0]).toEqual({ channels: ["ops", "eng"] });
    const opts = h.save.mock.calls[0]?.[1] as { onSuccess: () => void; onError: (e: Error) => void };
    act(() => opts.onError(new Error("no notification channel is named eng")));
    expect(screen.getByText(/no notification channel is named eng/)).toBeInTheDocument();
    act(() => opts.onSuccess());
  });

  it("offers no save in file config mode", () => {
    render(<ChangeNotificationsCard isReadOnly />);
    expect(screen.queryByRole("button", { name: /save/i })).not.toBeInTheDocument();
  });
});
