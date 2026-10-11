import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";

vi.mock("@/api/admin/hooks", () => ({
  useScriptRunAlert: vi.fn(),
  useSetScriptRunAlert: vi.fn(),
  useNotificationChannels: vi.fn(),
}));

import { useNotificationChannels, useScriptRunAlert, useSetScriptRunAlert } from "@/api/admin/hooks";
import { ScriptRunAlertCard } from "./ScriptRunAlertCard";

const mutate = vi.fn();

function channels(list: { name: string; enabled: boolean }[]) {
  vi.mocked(useNotificationChannels).mockReturnValue({
    data: { channels: list, kinds: [] },
    isLoading: false,
  } as unknown as ReturnType<typeof useNotificationChannels>);
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useScriptRunAlert).mockReturnValue({
    data: { channels: ["gone-channel"], updated_by: "admin@example.com", updated_at: "2026-10-10T09:00:00Z" },
    isLoading: false,
  } as unknown as ReturnType<typeof useScriptRunAlert>);
  vi.mocked(useSetScriptRunAlert).mockReturnValue({ mutate, isPending: false } as unknown as ReturnType<
    typeof useSetScriptRunAlert
  >);
  channels([
    { name: "ops-alerts", enabled: true },
    { name: "pager", enabled: false },
  ]);
});

afterEach(cleanup);

describe("ScriptRunAlertCard", () => {
  it("offers every configured channel and saves the chosen ones", () => {
    render(<ScriptRunAlertCard isReadOnly={false} />);
    expect(screen.getByText("Scheduled run failures")).toBeInTheDocument();
    expect(screen.getByText("disabled")).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("Post to ops-alerts"));
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    expect(mutate).toHaveBeenCalledWith({ channels: ["gone-channel", "ops-alerts"] }, expect.anything());
  });

  it("shows a stored channel that no longer exists so it can be removed", () => {
    render(<ScriptRunAlertCard isReadOnly={false} />);
    expect(screen.getByText("missing")).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText("Post to gone-channel"));
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(mutate).toHaveBeenCalledWith({ channels: [] }, expect.anything());
  });

  it("says where channels come from when none is configured", () => {
    channels([]);
    vi.mocked(useScriptRunAlert).mockReturnValue({
      data: { channels: [] },
      isLoading: false,
    } as unknown as ReturnType<typeof useScriptRunAlert>);
    render(<ScriptRunAlertCard isReadOnly={false} />);
    expect(screen.getByText(/no notification channels are configured/i)).toBeInTheDocument();
  });

  it("reports a refused save", () => {
    mutate.mockImplementation((_input, opts: { onError: (e: Error) => void }) =>
      opts.onError(new Error('channel "x": no notification channel has that name')),
    );
    render(<ScriptRunAlertCard isReadOnly={false} />);
    fireEvent.click(screen.getByLabelText("Post to ops-alerts"));
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(screen.getByText(/no notification channel has that name/)).toBeInTheDocument();
  });

  it("offers no save in file config mode", () => {
    render(<ScriptRunAlertCard isReadOnly />);
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });
});
