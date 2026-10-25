import { http, HttpResponse } from "msw";
import type { EndpointRange } from "@/api/admin/types";
import { mockAdminEndpoints, mockEndpointDetail } from "../data/endpoints";

// The admin Endpoints routes (#2208):
//   GET /endpoints?range=day|week         (every binding with its traffic)
//   GET /endpoints/:name?range=day|week   (one endpoint's page)

const ADMIN_BASE = "/api/v1/admin";

function rangeOf(request: Request): EndpointRange | undefined {
  const range = new URL(request.url).searchParams.get("range") ?? "day";
  return range === "day" || range === "week" ? range : undefined;
}

const badRange = () =>
  HttpResponse.json({ type: "about:blank", title: "Bad Request", status: 400, detail: "range must be day or week" }, { status: 400 });

export const endpointHandlers = [
  http.get(`${ADMIN_BASE}/endpoints`, ({ request }) => {
    const range = rangeOf(request);
    if (!range) return badRange();
    // Filters and pages as the server does (#2251).
    const q = new URL(request.url).searchParams;
    const access = q.get("access") ?? "";
    const status = q.get("status") ?? "";
    const matched = mockAdminEndpoints.filter(
      (e) => (access === "" || e.auth_mode === access) && (status === "" || (status === "on") === e.enabled),
    );
    const perPage = Math.min(Number(q.get("per_page")) || 50, 200);
    const page = Math.max(Number(q.get("page")) || 1, 1);
    return HttpResponse.json({
      range,
      since: new Date().toISOString(),
      endpoints: matched.slice((page - 1) * perPage, page * perPage),
      total: matched.length,
      page,
      per_page: perPage,
    });
  }),
  http.get(`${ADMIN_BASE}/endpoints/:name`, ({ params, request }) => {
    const range = rangeOf(request);
    if (!range) return badRange();
    const detail = mockEndpointDetail(String(params.name), range);
    if (!detail) {
      return HttpResponse.json({ type: "about:blank", title: "Not Found", status: 404, detail: "no endpoint has this name" }, { status: 404 });
    }
    return HttpResponse.json(detail);
  }),
];
