import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";

vi.mock("@/api/admin/hooks", () => ({
  useAccessRequestSettings: vi.fn(),
  useSetAccessRequestSettings: vi.fn(),
  useExternalAccountSettings: vi.fn(),
  useSetExternalAccountSettings: vi.fn(),
}));

import {
  useAccessRequestSettings,
  useExternalAccountSettings,
  useSetAccessRequestSettings,
  useSetExternalAccountSettings,
} from "@/api/admin/hooks";
import { AccessRequestsSettingsCard } from "./AccessRequestsSettingsCard";
import { ExternalAccountsSettingsCard } from "./ExternalAccountsSettingsCard";

// The two cards of Admin > Settings > Access: how long an external user's
// session and links last (#2092), and what a request for access shows and
// how long it waits (#2090).

const mutate = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useAccessRequestSettings).mockReturnValue({
    data: { reveal_names: false, expire_days: 14 },
    isLoading: false,
  } as never);
  vi.mocked(useSetAccessRequestSettings).mockReturnValue({
    mutate,
    isPending: false,
  } as never);
  vi.mocked(useExternalAccountSettings).mockReturnValue({
    data: { session_hours: 12, invite_hours: 168, link_minutes: 15 },
    isLoading: false,
  } as never);
  vi.mocked(useSetExternalAccountSettings).mockReturnValue({
    mutate,
    isPending: false,
  } as never);
});

afterEach(cleanup);

describe("AccessRequestsSettingsCard (#2090)", () => {
  it("saves the name shown and the expiry", () => {
    render(<AccessRequestsSettingsCard isReadOnly={false} />);
    expect(screen.getByLabelText("Requests expire after")).toHaveValue(14);
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: /Show the item's name on the request page/,
      }),
    );
    fireEvent.change(screen.getByLabelText("Requests expire after"), {
      target: { value: "30" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(mutate.mock.calls[0]![0]).toEqual({
      reveal_names: true,
      expire_days: 30,
    });
  });

  it("is read-only without a save for a read-only administrator", () => {
    render(<AccessRequestsSettingsCard isReadOnly />);
    expect(screen.getByRole("checkbox")).toBeDisabled();
    expect(screen.getByLabelText("Requests expire after")).toBeDisabled();
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });

  it("says it is loading until the settings are read", () => {
    vi.mocked(useAccessRequestSettings).mockReturnValue({
      data: undefined,
      isLoading: true,
    } as never);
    render(<AccessRequestsSettingsCard isReadOnly={false} />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });
});

describe("ExternalAccountsSettingsCard (#2092)", () => {
  it("saves the session and link lifetimes", () => {
    render(<ExternalAccountsSettingsCard isReadOnly={false} />);
    expect(screen.getByLabelText("Session")).toHaveValue(12);
    expect(screen.getByLabelText("Invitation link")).toHaveValue(168);
    fireEvent.change(screen.getByLabelText("Sign-in link"), {
      target: { value: "30" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(mutate.mock.calls[0]![0]).toEqual({
      session_hours: 12,
      invite_hours: 168,
      link_minutes: 30,
    });
  });

  it("is read-only without a save for a read-only administrator", () => {
    render(<ExternalAccountsSettingsCard isReadOnly />);
    expect(screen.getByLabelText("Session")).toBeDisabled();
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });
});
