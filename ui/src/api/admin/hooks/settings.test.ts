import { describe, it, expect, vi, afterEach } from "vitest";
import { createElement } from "react";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { lastDeliveryFailed, useFailingChannelCount } from "./settings";

const json = (body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }));

describe("channel delivery status (#2093)", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reads a failure as one newer than the last delivery", () => {
    const base = { name: "c", sent_last_hour: 0, max_per_hour: 60, pending: 0, suppressed: 0 };
    expect(lastDeliveryFailed(base)).toBe(false);
    expect(lastDeliveryFailed({ ...base, last_error_at: "2026-10-11T02:00:00Z" })).toBe(true);
    expect(lastDeliveryFailed({ ...base, last_error_at: "2026-10-11T02:00:00Z", last_delivered_at: "2026-10-11T03:00:00Z" })).toBe(false);
    expect(lastDeliveryFailed({ ...base, last_error_at: "2026-10-11T04:00:00Z", last_delivered_at: "2026-10-11T03:00:00Z" })).toBe(true);
  });

  it("counts the channels whose last delivery failed", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((url: string) => {
        if (url.endsWith("/notification-channels")) {
          return json({ channels: [{ name: "ops" }, { name: "team" }], kinds: [] });
        }
        if (url.includes("/ops/status")) {
          return json({ name: "ops", last_error_at: "2026-10-11T02:00:00Z", sent_last_hour: 0, max_per_hour: 60, pending: 0, suppressed: 0 });
        }
        return json({ name: "team", last_delivered_at: "2026-10-11T02:00:00Z", sent_last_hour: 1, max_per_hour: 60, pending: 0, suppressed: 0 });
      }),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { result } = renderHook(() => useFailingChannelCount(), {
      wrapper: ({ children }) => createElement(QueryClientProvider, { client: qc }, children),
    });
    await vi.waitFor(() => expect(result.current).toBe(1));
  });
});
