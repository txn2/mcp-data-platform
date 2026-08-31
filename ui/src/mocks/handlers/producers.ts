import { http, HttpResponse } from "msw";
import { producedByScript, producersByTarget } from "../data/producers";

const PORTAL_BASE = "/api/v1/portal";

// What produced a file, and what a script has produced (#1569), from both ends
// of one relation.

/** listBody answers a producer listing the way the portal route does. */
function listBody(kind: "asset" | "resource", id: string) {
  const data = producersByTarget[`${kind}:${id}`] ?? [];
  return HttpResponse.json({ data, total: data.length });
}

export const producerHandlers = [
  http.get(`${PORTAL_BASE}/assets/:id/producers`, ({ params }) =>
    listBody("asset", String(params.id)),
  ),

  http.get(`${PORTAL_BASE}/resources/:id/producers`, ({ params }) =>
    listBody("resource", String(params.id)),
  ),

  http.get(`${PORTAL_BASE}/scripts/:id/produced`, ({ params }) => {
    const data = producedByScript[String(params.id)] ?? [];
    return HttpResponse.json({ data, total: data.length });
  }),
];
