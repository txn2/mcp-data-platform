import { http, HttpResponse } from "msw";
import type { Secret, SecretInput } from "@/api/admin/types";
import { mockSecrets } from "../data/secrets";

// Stored secret admin routes (#2051):
//   GET    /secrets        (list)
//   GET    /secrets/:name  (one)
//   PUT    /secrets/:name  (create 201 or change 200)
//   DELETE /secrets/:name  (delete)
//   GET    /secrets/:name/code  (an authenticator seed's current code, #2065)
// Like the server, nothing returned ever carries a value.

const ADMIN_BASE = "/api/v1/admin";
const secrets: Secret[] = [...mockSecrets];

const problem = (status: number, title: string, detail: string) =>
  HttpResponse.json({ type: "about:blank", title, status, detail }, { status });

export const secretHandlers = [
  http.get(`${ADMIN_BASE}/secrets`, () => HttpResponse.json({ secrets })),

  http.get(`${ADMIN_BASE}/secrets/:name`, ({ params }) => {
    const found = secrets.find((s) => s.name === params.name);
    return found
      ? HttpResponse.json(found)
      : problem(404, "Not Found", "secret not found");
  }),

  http.put(`${ADMIN_BASE}/secrets/:name`, async ({ params, request }) => {
    const name = String(params.name);
    const body = (await request.json()) as SecretInput;
    const at = secrets.findIndex((s) => s.name === name);
    if (at < 0 && !body.value)
      return problem(
        400,
        "Bad Request",
        "invalid secret: value is required when a secret is created",
      );
    if (body.allow_connections.length === 0) {
      return problem(
        400,
        "Bad Request",
        "invalid secret: allow_connections must name at least one connection",
      );
    }
    const now = new Date().toISOString();
    const prior = at >= 0 ? secrets[at]! : undefined;
    const kind = body.kind ?? prior?.kind ?? "value";
    const saved: Secret = {
      name,
      description: body.description,
      kind,
      ...(kind === "totp" ? { totp: prior?.totp ?? { algorithm: "SHA1", digits: 6, period: 30 } } : {}),
      allow_connections: body.allow_connections,
      allow_personas: body.allow_personas,
      created_by: prior?.created_by ?? "admin@example.com",
      updated_by: "admin@example.com",
      created_at: prior?.created_at ?? now,
      updated_at: now,
    };
    if (at >= 0) secrets[at] = saved;
    else secrets.push(saved);
    return HttpResponse.json(saved, { status: at >= 0 ? 200 : 201 });
  }),

  http.get(`${ADMIN_BASE}/secrets/:name/code`, ({ params }) => {
    const found = secrets.find((s) => s.name === params.name);
    if (!found) return problem(404, "Not Found", "secret not found");
    if (found.kind !== "totp" || !found.totp)
      return problem(409, "Conflict", "this secret holds a value, not an authenticator seed, so it has no code");
    const period = found.totp.period;
    const left = period - (Math.floor(Date.now() / 1000) % period);
    return HttpResponse.json({ code: "482913", seconds_left: left, totp: found.totp });
  }),

  http.delete(`${ADMIN_BASE}/secrets/:name`, ({ params }) => {
    const at = secrets.findIndex((s) => s.name === params.name);
    if (at < 0) return problem(404, "Not Found", "secret not found");
    secrets.splice(at, 1);
    return new HttpResponse(null, { status: 204 });
  }),
];
