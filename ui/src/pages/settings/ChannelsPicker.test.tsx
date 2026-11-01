import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";

vi.mock("@/api/admin/hooks", () => ({
  useNotificationChannels: vi.fn(),
}));

import { useNotificationChannels } from "@/api/admin/hooks";
import { ChannelsPicker } from "./ChannelsPicker";

afterEach(cleanup);

function channel(name: string, enabled = true) {
  return { name, kind: "slack", enabled };
}

describe("ChannelsPicker (#2389)", () => {
  it("lists the configured channels by name, and a missing chosen one after them", () => {
    vi.mocked(useNotificationChannels).mockReturnValue({
      data: { channels: [channel("ops-10"), channel("Alerts"), channel("ops-2"), channel("billing", false)] },
      isLoading: false,
    } as never);
    render(<ChannelsPicker selected={["ops-2", "zz-removed", "gone"]} onChange={vi.fn()} help="" />);

    const names = screen.getAllByRole("checkbox").map((c) => c.getAttribute("aria-label"));
    expect(names).toEqual([
      "Post to Alerts",
      "Post to billing",
      "Post to ops-2",
      "Post to ops-10",
      "Post to gone",
      "Post to zz-removed",
    ]);
  });
});
