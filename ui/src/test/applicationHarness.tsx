import { vi } from "vitest";
import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { getResponse } from "msw";
import { applicationHandlers } from "@/mocks/handlers/applications";
import { useAuthStore } from "@/stores/auth";

// The Applications pages' tests answer every request from the same MSW
// handlers the dev mock server and the screenshots use, so a page is tested
// against the routes and shapes the mocks hold to the API's swagger.

export interface SentRequest {
  method: string;
  path: string;
  body: unknown;
}

/** serveApplications routes fetch to the application handlers and records each request. */
export function serveApplications(): SentRequest[] {
  const sent: SentRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, window.location.origin);
      const body = typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
      sent.push({ method: init?.method ?? "GET", path: url.pathname + url.search, body });
      const res = await getResponse(applicationHandlers, new Request(url, init));
      return res ?? new Response(JSON.stringify({ detail: "no handler" }), { status: 404 });
    }),
  );
  return sent;
}

/** signIn sets who is reading. */
export function signIn(email: string, isAdmin: boolean) {
  useAuthStore.setState({
    user: { user_id: email, email, roles: [], is_admin: isAdmin },
    authMethod: "cookie",
    apiKey: "",
  });
}

export function renderWithQuery(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}
