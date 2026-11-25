import { describe, it, expect, vi, afterEach } from "vitest";
import { createElement } from "react";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  usePlatformFeedbackList,
  useResendPlatformFeedback,
  useSetPlatformFeedbackSettings,
  useSetPlatformFeedbackStatus,
} from "./platformFeedback";

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) => createElement(QueryClientProvider, { client: qc }, children);
}

const json = (body: unknown, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));

describe("platform feedback hooks (#2820)", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("lists posts with the filter as query parameters", async () => {
    const fetchMock = vi.fn((_url: string) => json({ posts: [], total: 0, stats: { open: 0, reports_this_week: 0, refused_this_week: 0, delivery_failures: 0 } }));
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderHook(() => usePlatformFeedbackList({ status: "open", tool: "manage_asset", q: "shared" }), { wrapper: wrapper() });
    await vi.waitFor(() => expect(result.current.isSuccess).toBe(true));
    const url = fetchMock.mock.calls[0]![0];
    expect(url).toContain("/api/v1/admin/platform-feedback?");
    expect(url).toContain("status=open");
    expect(url).toContain("tool=manage_asset");
    expect(url).toContain("q=shared");
  });

  it("saves the settings with PUT", async () => {
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) => json({ enabled: true, channels: ["ops"], personas: [], session_limit: 5, persona_daily_limit: 50 }));
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderHook(() => useSetPlatformFeedbackSettings(), { wrapper: wrapper() });
    await act(() => result.current.mutateAsync({ enabled: true, channels: ["ops"], personas: [], session_limit: 5, persona_daily_limit: 50 }));
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toContain("/api/v1/admin/settings/platform-feedback");
    expect(init?.method).toBe("PUT");
    expect(JSON.parse(String(init?.body))).toMatchObject({ enabled: true, channels: ["ops"] });
  });

  it("sets a status and resends to a channel through routes that answer 204", async () => {
    const fetchMock = vi.fn((_url: string, _init?: RequestInit) => Promise.resolve(new Response(null, { status: 204 })));
    vi.stubGlobal("fetch", fetchMock);
    const status = renderHook(() => useSetPlatformFeedbackStatus("pfb_1"), { wrapper: wrapper() });
    await act(() => status.result.current.mutateAsync("planned"));
    expect(fetchMock.mock.calls[0]![0]).toContain("/platform-feedback/pfb_1/status");
    expect(JSON.parse(String(fetchMock.mock.calls[0]![1]?.body))).toEqual({ status: "planned" });
    const resend = renderHook(() => useResendPlatformFeedback("pfb_1"), { wrapper: wrapper() });
    await act(() => resend.result.current.mutateAsync("ops"));
    expect(fetchMock.mock.calls[1]![0]).toContain("/platform-feedback/pfb_1/resend");
    expect(JSON.parse(String(fetchMock.mock.calls[1]![1]?.body))).toEqual({ channel: "ops" });
  });

  it("throws the server's refusal", async () => {
    vi.stubGlobal("fetch", vi.fn(() => json({ detail: "status is open, planned, done or declined" }, 400)));
    const { result } = renderHook(() => useSetPlatformFeedbackStatus("pfb_1"), { wrapper: wrapper() });
    await expect(act(() => result.current.mutateAsync("open"))).rejects.toThrow("status is open, planned, done or declined");
  });
});
