import { http, HttpResponse } from "msw";
import type { TypecheckCheck, TypecheckView } from "@/api/portal/hooks/siteTypecheck";

// The site type check routes (#2793). Release 12 has three errors, 11 is
// clean, the rest were never checked. `?typecheck=<status>` on the document's
// URL puts release 12's check in that state, for a screenshot of each.

const base = "/api/v1/portal/sites";
const at = "2026-11-08T13:48:00Z";

const errors: TypecheckCheck = {
  id: 7, release: 12, environment: "dev", tsconfig: "tsconfig.json", status: "errors",
  created_at: at, started_at: at, finished_at: at, requested_by: "jane.doe@example.com",
  result: {
    status: "errors",
    diagnostics: [
      { file: "src/billing/Invoices.tsx", line: 48, column: 17, category: "error", code: "TS2322", message: "Type 'string' is not assignable to type 'number'." },
      { file: "src/billing/Invoices.tsx", line: 112, column: 9, category: "error", code: "TS2339", message: "Property 'dueDate' does not exist on type 'Invoice'." },
      { file: "src/lib/format.ts", line: 7, column: 14, category: "error", code: "TS6133", message: "'locale' is declared but its value is never read." },
    ],
    missing: ["date-fns-tz", "@acme/ui"], unverifiable: [], packages: 412, fetched: 0, lockfile: "pnpm-lock.yaml", duration_ms: 11000,
  },
};

const clean: TypecheckCheck = {
  ...errors, id: 6, release: 11,
  result: { status: "clean", diagnostics: [], missing: [], unverifiable: [], packages: 412, fetched: 3, lockfile: "pnpm-lock.yaml", duration_ms: 3000 },
  status: "clean",
};

/** stateOf is release 12's check in the state a screenshot asks for. */
function stateOf(status: string | null): TypecheckCheck {
  switch (status) {
    case "queued":
      return { ...errors, status: "queued", result: undefined, started_at: undefined, finished_at: undefined, ahead: 2 };
    case "running":
      return { ...errors, status: "running", result: undefined, finished_at: undefined };
    case "clean":
      return { ...clean, release: 12 };
    case "over_limit":
    case "unsupported":
    case "failed":
      return {
        ...errors, status,
        result: {
          status, diagnostics: [], missing: [], unverifiable: [], packages: 0, fetched: 0, duration_ms: 0,
          message: {
            over_limit: "The check needed more than the 3072 MiB a check may use and was stopped; the site is not affected, and sites.typecheck.memory_limit raises the limit.",
            unsupported: "tsconfig.json sets baseUrl, which TypeScript 7 no longer reads. Replace it with paths entries that start with ./.",
            failed: "The npm registry did not answer, so the TypeScript compiler and package types could not be downloaded.",
          }[status],
        },
      };
    default:
      return errors;
  }
}

function requested(): string | null {
  return typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("typecheck");
}

function checkOf(release: number): TypecheckCheck | null {
  if (release === 12) return stateOf(requested());
  if (release === 11) return clean;
  return null;
}

export const siteTypecheckHandlers = [
  http.get(`${base}/:site/typechecks`, () => {
    const releases: Record<string, { status: string; errors: number }> = {};
    for (const n of [12, 11]) {
      const c = checkOf(n)!;
      releases[String(n)] = { status: c.status, errors: c.result?.diagnostics.length ?? 0 };
    }
    return HttpResponse.json({ releases });
  }),
  http.get(`${base}/:site/releases/:n/typecheck`, ({ params }) => {
    const release = Number(params.n);
    const view: TypecheckView = { release, check: checkOf(release) };
    return HttpResponse.json(view);
  }),
  http.post(`${base}/:site/releases/:n/typecheck`, ({ params }) => {
    const release = Number(params.n);
    const view: TypecheckView = {
      release,
      check: { ...errors, id: 99, release, status: "queued", result: undefined, finished_at: undefined, started_at: undefined, ahead: 0 },
    };
    return HttpResponse.json(view, { status: 202 });
  }),
];
