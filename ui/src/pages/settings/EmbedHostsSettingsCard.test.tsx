import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";

vi.mock("@/api/admin/hooks", () => ({
  useEmbedHostsSettings: vi.fn(),
  useSetEmbedHosts: vi.fn(),
}));

import { useEmbedHostsSettings, useSetEmbedHosts } from "@/api/admin/hooks";
import { EmbedHostsSettingsCard, hostLines } from "./EmbedHostsSettingsCard";

// Admin > Settings > Embed hosts (#2069).

const mutate = vi.fn();
const configured = ["https://fast.wistia.net", "https://player.vimeo.com"];

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useEmbedHostsSettings).mockReturnValue({
    data: { hosts: ["https://cdn.example.com"], configured },
    isLoading: false,
  } as never);
  vi.mocked(useSetEmbedHosts).mockReturnValue({ mutate, isPending: false } as never);
});

afterEach(cleanup);

describe("EmbedHostsSettingsCard", () => {
  it("saves one host per line, blank lines dropped", () => {
    render(<EmbedHostsSettingsCard isReadOnly={false} />);
    const area = screen.getByLabelText("Hosts, one per line");
    expect(area).toHaveValue("https://cdn.example.com");
    fireEvent.change(area, { target: { value: "https://cdn.example.com\n\n  fast.wistia.net \n" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(mutate.mock.calls[0]![0]).toEqual(["https://cdn.example.com", "fast.wistia.net"]);
  });

  it("restores the configured list into the draft", () => {
    render(<EmbedHostsSettingsCard isReadOnly={false} />);
    fireEvent.click(screen.getByRole("button", { name: "Restore the configured list" }));
    expect(screen.getByLabelText("Hosts, one per line")).toHaveValue(configured.join("\n"));
  });

  it("saves an empty list, which turns embeds off", () => {
    render(<EmbedHostsSettingsCard isReadOnly={false} />);
    fireEvent.change(screen.getByLabelText("Hosts, one per line"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(mutate.mock.calls[0]![0]).toEqual([]);
  });

  it("is read-only for a read-only administrator", () => {
    render(<EmbedHostsSettingsCard isReadOnly />);
    expect(screen.getByLabelText("Hosts, one per line")).toBeDisabled();
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
    expect(screen.queryByRole("button", { name: "Restore the configured list" })).toBeNull();
  });

  it("says it is loading until the settings are read", () => {
    vi.mocked(useEmbedHostsSettings).mockReturnValue({ data: undefined, isLoading: true } as never);
    render(<EmbedHostsSettingsCard isReadOnly={false} />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });

  it("hostLines trims and drops blanks", () => {
    expect(hostLines(" a.example.com \n\n b.example.com")).toEqual(["a.example.com", "b.example.com"]);
  });
});
