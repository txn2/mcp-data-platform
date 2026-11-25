import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { PlatformFeedbackSettingsView } from "@/api/admin/hooks";

const save = vi.fn();
let view: PlatformFeedbackSettingsView;

vi.mock("@/api/admin/hooks", () => ({
  usePlatformFeedbackSettings: () => ({ data: view, isLoading: false, error: null, refetch: vi.fn() }),
  useSetPlatformFeedbackSettings: () => ({ mutate: save, isPending: false }),
  useNotificationChannels: () => ({ data: { channels: [{ name: "ops-webhook", enabled: true }] }, isLoading: false }),
  usePersonas: () => ({ data: { personas: [{ name: "analyst", display_name: "Data Analyst" }, { name: "admin", display_name: "Administrator" }] } }),
}));

import { PlatformFeedbackCard } from "./PlatformFeedbackCard";

const base: PlatformFeedbackSettingsView = { enabled: false, channels: [], personas: [], session_limit: 5, persona_daily_limit: 50 };

describe("PlatformFeedbackCard (#2820)", () => {
  beforeEach(() => {
    save.mockReset();
    view = { ...base };
  });

  it("is off by default and warns when on with no channel", () => {
    const { rerender } = render(<PlatformFeedbackCard isReadOnly={false} />);
    expect(screen.getByRole("checkbox", { name: /Agents may post platform feedback/ })).not.toBeChecked();
    expect(screen.queryByText(/On, with no channel chosen/)).toBeNull();
    view = { ...base, enabled: true };
    rerender(<PlatformFeedbackCard isReadOnly={false} />);
    expect(screen.getByText(/On, with no channel chosen/)).toBeInTheDocument();
  });

  it("saves the switch, a channel, the chosen personas and both limits", () => {
    render(<PlatformFeedbackCard isReadOnly={false} />);
    fireEvent.click(screen.getByRole("checkbox", { name: /Agents may post platform feedback/ }));
    fireEvent.click(screen.getByRole("checkbox", { name: "Post to ops-webhook" }));
    fireEvent.click(screen.getByRole("radio", { name: "Only these personas" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "Administrator" }));
    fireEvent.change(screen.getByLabelText("Posts per session"), { target: { value: "3" } });
    fireEvent.change(screen.getByLabelText("Posts per persona per day"), { target: { value: "20" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    expect(save).toHaveBeenCalledWith(
      { enabled: true, channels: ["ops-webhook"], personas: ["analyst", "admin"], session_limit: 3, persona_daily_limit: 20 },
      expect.anything(),
    );
  });

  it("keeps the persona boxes off while every persona may post", () => {
    render(<PlatformFeedbackCard isReadOnly={false} />);
    expect(screen.getByRole("radio", { name: "Every persona except service accounts" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Data Analyst" })).toBeDisabled();
  });

  it("shows no Save on read-only settings", () => {
    render(<PlatformFeedbackCard isReadOnly />);
    expect(screen.queryByRole("button", { name: /Save/ })).toBeNull();
  });
});
