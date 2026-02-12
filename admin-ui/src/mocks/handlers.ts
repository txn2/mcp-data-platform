import { http, HttpResponse } from "msw";
import {
  mockAuditEvents,
  mockTimeseries,
  mockToolBreakdown,
  mockUserBreakdown,
  mockPersonaBreakdown,
  mockConnectionBreakdown,
  mockToolkitBreakdown,
  mockOverview,
  mockPerformance,
} from "./data/audit";
import { mockSystemInfo, mockTools, mockConnections } from "./data/system";

const BASE = "/api/v1/admin";

export const handlers = [
  // System
  http.get(`${BASE}/system/info`, () => HttpResponse.json(mockSystemInfo)),

  http.get(`${BASE}/tools`, () =>
    HttpResponse.json({ tools: mockTools, total: mockTools.length }),
  ),

  http.get(`${BASE}/connections`, () =>
    HttpResponse.json({
      connections: mockConnections,
      total: mockConnections.length,
    }),
  ),

  // Audit events
  http.get(`${BASE}/audit/events`, ({ request }) => {
    const url = new URL(request.url);
    const page = parseInt(url.searchParams.get("page") ?? "1", 10);
    const perPage = parseInt(url.searchParams.get("per_page") ?? "20", 10);
    const userId = url.searchParams.get("user_id");
    const toolName = url.searchParams.get("tool_name");
    const success = url.searchParams.get("success");

    let filtered = [...mockAuditEvents];
    if (userId) filtered = filtered.filter((e) => e.user_id === userId);
    if (toolName) filtered = filtered.filter((e) => e.tool_name === toolName);
    if (success !== null && success !== undefined && success !== "")
      filtered = filtered.filter((e) => String(e.success) === success);

    const start = (page - 1) * perPage;
    const data = filtered.slice(start, start + perPage);

    return HttpResponse.json({
      data,
      total: filtered.length,
      page,
      per_page: perPage,
    });
  }),

  // Audit metrics
  http.get(`${BASE}/audit/metrics/timeseries`, () =>
    HttpResponse.json(mockTimeseries),
  ),

  http.get(`${BASE}/audit/metrics/breakdown`, ({ request }) => {
    const url = new URL(request.url);
    const groupBy = url.searchParams.get("group_by");
    switch (groupBy) {
      case "user_id":
        return HttpResponse.json(mockUserBreakdown);
      case "persona":
        return HttpResponse.json(mockPersonaBreakdown);
      case "connection":
        return HttpResponse.json(mockConnectionBreakdown);
      case "toolkit_kind":
        return HttpResponse.json(mockToolkitBreakdown);
      default:
        return HttpResponse.json(mockToolBreakdown);
    }
  }),

  http.get(`${BASE}/audit/metrics/overview`, () =>
    HttpResponse.json(mockOverview),
  ),

  http.get(`${BASE}/audit/metrics/performance`, () =>
    HttpResponse.json(mockPerformance),
  ),
];
