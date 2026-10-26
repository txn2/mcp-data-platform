import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
import type { BotCheck, BotCheckList } from "@/api/admin/types";

vi.mock("@/api/admin/hooks", () => ({
  useBotChecks: vi.fn(),
  usePutBotCheck: vi.fn(),
  useDeleteBotCheck: vi.fn(),
  useSecrets: vi.fn(),
}));

import { useBotChecks, useDeleteBotCheck, usePutBotCheck, useSecrets } from "@/api/admin/hooks";
import { BotChecksSettingsCard } from "./BotChecksSettingsCard";

// The named bot checks a bot_check endpoint picks from (#2259): a list, an
// editor for one, a new one, and a delete refused while endpoints use it.

const put = vi.fn();
const del = vi.fn();

function check(name: string, over: Partial<BotCheck> = {}): BotCheck {
  return {
    name,
    provider: "pow",
    site_key: "",
    secret_name: "",
    difficulty: 16,
    max_difficulty: 22,
    rate_threshold: 30,
    challenge_ttl_seconds: 300,
    used_by: [],
    usable: true,
    updated_by: "sarah.chen@example.com",
    updated_at: "2026-10-16T09:00:00Z",
    ...over,
  };
}

function secret(name: string, connections: string[], personas: string[] = []) {
  return { name, description: "", kind: "value", allow_connections: connections, allow_personas: personas };
}

function withChecks(list: BotCheckList) {
  vi.mocked(useBotChecks).mockReturnValue({ data: structuredClone(list), isLoading: false } as never);
}

beforeEach(() => {
  vi.clearAllMocks();
  withChecks({
    key_available: true,
    checks: [check("default", { used_by: ["contact"] }), check("signup", { provider: "turnstile", site_key: "site", secret_name: "turnstile-key" })],
  });
  vi.mocked(usePutBotCheck).mockReturnValue({ mutate: put, isPending: false } as never);
  vi.mocked(useDeleteBotCheck).mockReturnValue({ mutate: del, isPending: false } as never);
  vi.mocked(useSecrets).mockReturnValue({
    data: { secrets: [secret("turnstile-key", ["bot_check"]), secret("crm-token", ["crm"]), secret("scoped", ["bot_check"], ["analyst"])] },
  } as never);
});

afterEach(cleanup);

describe("BotChecksSettingsCard (#2259)", () => {
  it("lists every check with its provider and the endpoints using it", () => {
    render(<BotChecksSettingsCard isReadOnly={false} />);
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    expect(within(rows[0]!).getByText("Proof of work")).toBeInTheDocument();
    expect(within(rows[0]!).getByText("contact")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("Cloudflare Turnstile")).toBeInTheDocument();
    expect(within(rows[1]!).getByText("No endpoint")).toBeInTheDocument();
  });

  it("opens a check on row click and saves its edited difficulty", () => {
    render(<BotChecksSettingsCard isReadOnly={false} />);
    fireEvent.click(screen.getByText("default"));
    const editor = screen.getByRole("region", { name: "Bot check default" });
    expect(within(editor).getByText(/Used by contact/)).toBeInTheDocument();
    fireEvent.change(within(editor).getByLabelText("Difficulty"), { target: { value: "20" } });
    fireEvent.click(within(editor).getByRole("button", { name: /Save/ }));
    expect(put).toHaveBeenCalledWith(
      { name: "default", input: expect.objectContaining({ provider: "pow", difficulty: 20, max_difficulty: 22 }) },
      expect.anything(),
    );
  });

  it("refuses to delete a check an endpoint names, and deletes one none does", () => {
    render(<BotChecksSettingsCard isReadOnly={false} />);
    fireEvent.click(screen.getByText("default"));
    const used = screen.getByRole("button", { name: "Delete" });
    expect(used).toBeDisabled();
    expect(used).toHaveAttribute("title", "Used by contact: bind them to another check first");
    fireEvent.click(screen.getByText("signup"));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(del).toHaveBeenCalledWith("signup", expect.anything());
  });

  it("creates a named check, offering only the secrets the bot check may use", () => {
    render(<BotChecksSettingsCard isReadOnly={false} />);
    fireEvent.click(screen.getByRole("button", { name: "New bot check" }));
    const editor = screen.getByRole("region", { name: "New bot check" });
    const save = within(editor).getByRole("button", { name: /Save/ });
    fireEvent.change(within(editor).getByLabelText("Name"), { target: { value: "Bad Name" } });
    expect(save).toBeDisabled();
    fireEvent.change(within(editor).getByLabelText("Name"), { target: { value: "forms" } });
    fireEvent.change(within(editor).getByLabelText("Provider"), { target: { value: "hcaptcha" } });
    const secrets = within(within(editor).getByLabelText("Secret")).getAllByRole("option").map((o) => o.textContent);
    expect(secrets).toEqual(["Choose a stored secret", "turnstile-key"]);
    fireEvent.change(within(editor).getByLabelText("Site key"), { target: { value: "10000000-ffff" } });
    fireEvent.change(within(editor).getByLabelText("Secret"), { target: { value: "turnstile-key" } });
    fireEvent.click(save);
    expect(put).toHaveBeenCalledWith(
      { name: "forms", input: expect.objectContaining({ provider: "hcaptcha", site_key: "10000000-ffff", secret_name: "turnstile-key" }) },
      expect.anything(),
    );
    expect(within(editor).queryByLabelText("Difficulty")).toBeNull();
  });

  it("keeps what was typed into a new check when the list is read again", () => {
    const { rerender } = render(<BotChecksSettingsCard isReadOnly={false} />);
    fireEvent.click(screen.getByRole("button", { name: "New bot check" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "forms" } });
    withChecks({ key_available: true, checks: [check("default")] });
    rerender(<BotChecksSettingsCard isReadOnly={false} />);
    expect(screen.getByLabelText("Name")).toHaveValue("forms");
  });

  it("says when a proof of work cannot be signed, and offers no edits read-only", () => {
    withChecks({ key_available: false, checks: [check("default", { usable: false })] });
    render(<BotChecksSettingsCard isReadOnly />);
    expect(screen.getByText(/cannot sign a proof-of-work challenge/)).toBeInTheDocument();
    expect(screen.getByText("(cannot be issued here)")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New bot check" })).toBeNull();
    fireEvent.click(screen.getByText("default"));
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
    expect(screen.getByLabelText("Difficulty")).toBeDisabled();
  });

  it("says when no check exists", () => {
    withChecks({ key_available: true, checks: [] });
    render(<BotChecksSettingsCard isReadOnly={false} />);
    expect(screen.getByText(/No bot checks exist/)).toBeInTheDocument();
  });
});
