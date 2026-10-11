import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";

vi.mock("@/api/portal/hooks/channels", () => ({
  useRunPosts: vi.fn(),
}));

import { useRunPosts } from "@/api/portal/hooks/channels";
import { ScriptRunPosts } from "./ScriptRunPosts";

afterEach(cleanup);

function posts(list: unknown[] | undefined) {
  vi.mocked(useRunPosts).mockReturnValue({ data: list ? { posts: list } : undefined } as unknown as ReturnType<
    typeof useRunPosts
  >);
}

describe("ScriptRunPosts", () => {
  it("lists each post with what became of it", () => {
    posts([
      { channel: "ops-alerts", title: "Feed stale", queued_at: "2026-07-29T15:10:00Z", status: "sent", deliveries: 1, sent: 1, failed: 0 },
      { channel: "ops-email", title: "Weekly", queued_at: "2026-07-29T15:10:01Z", status: "queued", deliveries: 3, sent: 2, failed: 0 },
      { channel: "hook", title: "Event", queued_at: "2026-07-29T15:10:02Z", status: "failed", deliveries: 1, sent: 0, failed: 1, last_error: "upstream answered HTTP 410 Gone" },
    ]);
    render(<ScriptRunPosts scriptId="s1" runId="r1" />);
    expect(screen.getByText("Posted to ops-alerts")).toBeInTheDocument();
    expect(screen.getByText("2 of 3 delivered")).toBeInTheDocument();
    expect(screen.getByText("failed")).toBeInTheDocument();
    expect(screen.getByText(/HTTP 410 Gone/)).toBeInTheDocument();
    expect(vi.mocked(useRunPosts)).toHaveBeenCalledWith("s1", "r1");
  });

  it("shows nothing for a run that posted nothing", () => {
    posts([]);
    render(<ScriptRunPosts scriptId="s1" runId="r1" />);
    expect(screen.queryByTestId("run-posts")).toBeNull();
    cleanup();
    posts(undefined);
    render(<ScriptRunPosts scriptId="s1" runId="r1" />);
    expect(screen.queryByTestId("run-posts")).toBeNull();
  });
});
