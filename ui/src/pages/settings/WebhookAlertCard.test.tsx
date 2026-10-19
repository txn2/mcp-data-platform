import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
import type { WebhookAlertSettings } from "@/api/admin/types";

vi.mock("@/api/admin/hooks", () => ({
  useWebhookAlertSettings: vi.fn(),
  useSetWebhookAlertSettings: vi.fn(),
  useWebhookSources: vi.fn(),
  useNotificationChannels: vi.fn(),
}));

import {
  useNotificationChannels,
  useSetWebhookAlertSettings,
  useWebhookAlertSettings,
  useWebhookSources,
} from "@/api/admin/hooks";
import { WebhookAlertCard } from "./WebhookAlertCard";

const mutate = vi.fn();

const stored: WebhookAlertSettings = {
  channels: ["ops-alerts"],
  spool_fill_percent: { warning: 50, critical: 80 },
  spool_age_percent: { warning: 25, critical: 60 },
  drain_failures: { warning: 3, critical: 10 },
  drain_failing_minutes: 5,
  not_reporting_seconds: 120,
  spool_volume_free_percent: { warning: 25, critical: 10 },
  defaults: {
    refusal_minutes: 5,
    unauthorized_percent: { warning: 10, critical: 0 },
    ack_p99_ms: { warning: 1000, critical: 5000 },
    compaction_overdue_minutes: { warning: 1, critical: 30 },
    silent_after_minutes: 0,
  },
  sources: { "email-events": { silent_after_minutes: 120 } },
  updated_by: "admin@example.com",
  updated_at: "2026-10-12T10:00:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useWebhookAlertSettings).mockReturnValue({ data: structuredClone(stored), isLoading: false } as unknown as ReturnType<
    typeof useWebhookAlertSettings
  >);
  vi.mocked(useSetWebhookAlertSettings).mockReturnValue({ mutate, isPending: false } as unknown as ReturnType<
    typeof useSetWebhookAlertSettings
  >);
  vi.mocked(useWebhookSources).mockReturnValue({
    data: { sources: [{ name: "email-events" }, { name: "crm-contacts" }] },
  } as unknown as ReturnType<typeof useWebhookSources>);
  vi.mocked(useNotificationChannels).mockReturnValue({
    data: { channels: [{ name: "ops-alerts", enabled: true }], kinds: [] },
    isLoading: false,
  } as unknown as ReturnType<typeof useNotificationChannels>);
});

afterEach(cleanup);

describe("WebhookAlertCard (#2149)", () => {
  it("shows every condition with its two thresholds and saves an edit", () => {
    render(<WebhookAlertCard isReadOnly={false} />);
    for (const label of ["Spool fill", "Oldest event waiting", "Drain failures in a row", "Spool volume free", "Acknowledgement p99", "Compaction behind"]) {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    }
    expect(screen.getByLabelText("Spool fill warning")).toHaveValue(50);
    fireEvent.change(screen.getByLabelText("Spool fill critical"), { target: { value: "90" } });
    fireEvent.change(screen.getByLabelText("Acknowledgement p99 warning"), { target: { value: "500" } });
    fireEvent.change(screen.getByLabelText("Not reporting critical"), { target: { value: "60" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    const input = mutate.mock.calls[0]![0];
    expect(input.spool_fill_percent).toEqual({ warning: 50, critical: 90 });
    expect(input.defaults.ack_p99_ms).toEqual({ warning: 500, critical: 5000 });
    expect(input.not_reporting_seconds).toBe(60);
    expect(input).not.toHaveProperty("updated_at");
  });

  it("adds a source's override, which opts it into silence, and removes another", () => {
    render(<WebhookAlertCard isReadOnly={false} />);
    expect(screen.getByTestId("override-email-events")).toBeInTheDocument();
    fireEvent.click(within(screen.getByTestId("override-email-events")).getByRole("button", { name: "Remove email-events override" }));
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(mutate.mock.calls[0]![0].sources).toEqual({});
  });

  it("offers no edit in file config mode", () => {
    render(<WebhookAlertCard isReadOnly />);
    expect(screen.getByLabelText("Spool fill warning")).toBeDisabled();
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });
});
