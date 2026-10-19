import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { BotCheckSettings } from "@/api/admin/types";

vi.mock("@/api/admin/hooks", () => ({
  useBotCheckSettings: vi.fn(),
  useSetBotCheckSettings: vi.fn(),
  useSecrets: vi.fn(),
}));

import { useBotCheckSettings, useSecrets, useSetBotCheckSettings } from "@/api/admin/hooks";
import { BotCheckSettingsCard } from "./BotCheckSettingsCard";

const mutate = vi.fn();

const stored: BotCheckSettings = {
  provider: "pow",
  site_key: "",
  secret_name: "",
  allow_public_without_check: false,
  difficulty: 16,
  max_difficulty: 22,
  rate_threshold: 30,
  challenge_ttl_seconds: 300,
  updated_by: "sarah.chen@example.com",
  updated_at: "2026-10-16T09:00:00Z",
  key_available: true,
  public_allowed: true,
};

function secret(name: string, connections: string[], personas: string[] = []) {
  return { name, description: "", kind: "value", allow_connections: connections, allow_personas: personas };
}

function withSettings(s: BotCheckSettings) {
  vi.mocked(useBotCheckSettings).mockReturnValue({ data: structuredClone(s), isLoading: false } as never);
}

beforeEach(() => {
  vi.clearAllMocks();
  withSettings(stored);
  vi.mocked(useSetBotCheckSettings).mockReturnValue({ mutate, isPending: false } as never);
  vi.mocked(useSecrets).mockReturnValue({
    data: { secrets: [secret("turnstile-key", ["bot_check"]), secret("crm-token", ["crm"]), secret("scoped", ["bot_check"], ["analyst"])] },
  } as never);
});

afterEach(cleanup);

describe("BotCheckSettingsCard (#2173)", () => {
  it("shows the proof of work and saves an edit without the read-only fields", () => {
    render(<BotCheckSettingsCard isReadOnly={false} />);
    expect(screen.getByLabelText("Provider")).toHaveValue("pow");
    expect(screen.getByLabelText("Difficulty")).toHaveValue(16);
    fireEvent.change(screen.getByLabelText("Rate threshold"), { target: { value: "60" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    const input = mutate.mock.calls[0]![0];
    expect(input.rate_threshold).toBe(60);
    expect(input).not.toHaveProperty("updated_by");
    expect(input).not.toHaveProperty("key_available");
    expect(input).not.toHaveProperty("public_allowed");
  });

  it("offers a provider only the secrets the bot check may read", () => {
    render(<BotCheckSettingsCard isReadOnly={false} />);
    fireEvent.change(screen.getByLabelText("Provider"), { target: { value: "turnstile" } });
    expect(screen.queryByLabelText("Difficulty")).toBeNull();
    const options = Array.from((screen.getByLabelText("Secret") as HTMLSelectElement).options).map((o) => o.value);
    expect(options).toEqual(["", "turnstile-key"]);
    fireEvent.change(screen.getByLabelText("Site key"), { target: { value: "1x00000000000000000000AA" } });
    fireEvent.change(screen.getByLabelText("Secret"), { target: { value: "turnstile-key" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(mutate.mock.calls[0]![0]).toMatchObject({ provider: "turnstile", site_key: "1x00000000000000000000AA", secret_name: "turnstile-key" });
  });

  it("keeps a stored secret the list no longer offers", () => {
    withSettings({ ...stored, provider: "hcaptcha", site_key: "k", secret_name: "gone" });
    render(<BotCheckSettingsCard isReadOnly={false} />);
    expect(screen.getByLabelText("Secret")).toHaveValue("gone");
  });

  it("says when public endpoints are refused, and when proof of work cannot be signed", () => {
    withSettings({ ...stored, provider: "none", public_allowed: false });
    render(<BotCheckSettingsCard isReadOnly={false} />);
    expect(screen.getByText("Public endpoints are refused under these settings.")).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText(/Allow public endpoints without a bot check/));
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(mutate.mock.calls[0]![0].allow_public_without_check).toBe(true);
    cleanup();

    withSettings({ ...stored, key_available: false, public_allowed: false });
    render(<BotCheckSettingsCard isReadOnly={false} />);
    expect(screen.getByText(/no browser-session signing key/)).toBeInTheDocument();
  });

  it("is read-only in file-config mode", () => {
    render(<BotCheckSettingsCard isReadOnly />);
    expect(screen.getByLabelText("Provider")).toBeDisabled();
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });
});
