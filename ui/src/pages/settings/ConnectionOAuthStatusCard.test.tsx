import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type {
  ConnectionAuthEvent,
  ConnectionOAuthStatus,
} from "@/api/admin/types";

vi.mock("@/api/admin/hooks", () => ({
  useConnectionOAuthStatus: vi.fn(),
  useConnectionAuthEvents: vi.fn(() => ({ data: [], isLoading: false })),
  useReacquireConnectionOAuth: vi.fn(() => ({
    mutateAsync: vi.fn(),
    isPending: false,
  })),
  useStartConnectionOAuth: vi.fn(() => ({
    mutateAsync: vi.fn(),
    isPending: false,
  })),
}));

import { useConnectionOAuthStatus } from "@/api/admin/hooks";
import {
  ConnectionOAuthStatusCard,
  describeVerdictCode,
  renderDetailHint,
  revocationHeadline,
} from "./ConnectionOAuthStatusCard";

const mockStatus = vi.mocked(useConnectionOAuthStatus);

function wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function statusWithRevocation(
  reason: string,
  idp_host = "iam.example.io",
): ConnectionOAuthStatus {
  return {
    configured: true,
    token_acquired: false,
    has_refresh_token: false,
    needs_reauth: true,
    last_revocation: {
      occurred_at: new Date(Date.now() - 6 * 60 * 60 * 1000).toISOString(),
      reason,
      idp_host,
    },
  };
}

describe("revocationHeadline", () => {
  it("returns the locally-decided headline for refresh_expired", () => {
    // refresh_expired ONLY comes from the local short-circuit in
    // pkg/connoauth/source.go — the IdP never returns this. The
    // headline must not claim the IdP rejected anything.
    expect(revocationHeadline("refresh_expired")).toBe(
      "Session reached the IdP-disclosed maximum lifetime.",
    );
  });

  it("returns the IdP-rejected headline for invalid_grant", () => {
    expect(revocationHeadline("invalid_grant")).toBe(
      "Upstream IdP rejected the refresh token.",
    );
  });

  it("returns the no-refresh-token headline for no_refresh_token", () => {
    expect(revocationHeadline("no_refresh_token")).toBe(
      "No refresh token is stored for this connection.",
    );
  });

  it("falls back to a neutral headline for an unknown reason", () => {
    expect(revocationHeadline("rejected")).toBe("Previous session ended.");
    expect(revocationHeadline(undefined)).toBe("Previous session ended.");
  });
});

describe("describeVerdictCode", () => {
  it("translates refresh_expired to a locally-decided phrase", () => {
    // The string "refresh_expired" reads to operators as an IdP error
    // code. It is not — the platform reached this verdict from a
    // previously-disclosed deadline without calling the IdP again.
    expect(describeVerdictCode("refresh_expired")).toBe(
      "IdP-disclosed deadline reached",
    );
  });

  it("translates no_refresh_token to a locally-decided phrase", () => {
    expect(describeVerdictCode("no_refresh_token")).toBe(
      "no refresh token stored",
    );
  });

  it("passes invalid_grant through verbatim (the IdP did return it)", () => {
    expect(describeVerdictCode("invalid_grant")).toBe("invalid_grant");
  });

  it("passes any unknown code through verbatim", () => {
    expect(describeVerdictCode("server_error")).toBe("server_error");
    expect(describeVerdictCode("")).toBe("");
  });
});

function makeEvent(overrides: Partial<ConnectionAuthEvent>): ConnectionAuthEvent {
  return {
    id: "evt-1",
    occurred_at: "2026-05-14T10:10:05Z",
    event_type: "refresh_failed_revoked",
    actor: "system:background-refresh",
    ...overrides,
  };
}

describe("renderDetailHint", () => {
  it("translates idp_error_code=refresh_expired so the row doesn't blame the IdP", () => {
    const hint = renderDetailHint(
      makeEvent({ detail: { idp_error_code: "refresh_expired" } }),
    );
    expect(hint).toBe("(IdP-disclosed deadline reached)");
  });

  it("translates reason=refresh_expired on token_deleted_revoked rows", () => {
    const hint = renderDetailHint(
      makeEvent({
        event_type: "token_deleted_revoked",
        detail: { reason: "refresh_expired" },
      }),
    );
    expect(hint).toBe("(IdP-disclosed deadline reached)");
  });

  it("preserves invalid_grant verbatim — that one came from the IdP", () => {
    const hint = renderDetailHint(
      makeEvent({ detail: { idp_error_code: "invalid_grant" } }),
    );
    expect(hint).toBe("(invalid_grant)");
  });

  it("renders rotated-refresh hint with duration", () => {
    const hint = renderDetailHint(
      makeEvent({
        event_type: "refresh_succeeded",
        detail: { rotated_refresh: true, duration_ms: 116 },
      }),
    );
    expect(hint).toBe("(rotated refresh, 116ms)");
  });

  it("returns empty string when nothing notable is in detail", () => {
    expect(renderDetailHint(makeEvent({ detail: {} }))).toBe("");
    expect(renderDetailHint(makeEvent({}))).toBe("");
  });
});

// Render-level tests prove the JSX assembly produces a banner whose
// visible text matches what the wording function returned — the pure
// unit tests above can't catch mistakes like wiring the headline to
// the wrong branch or losing the host in the JSX.
describe("ConnectionOAuthStatusCard banner rendering", () => {
  it("for refresh_expired, the banner does NOT claim the IdP returned an error", () => {
    mockStatus.mockReturnValue({
      data: statusWithRevocation("refresh_expired", "iam.example.io"),
      isLoading: false,
    } as unknown as ReturnType<typeof useConnectionOAuthStatus>);

    render(
      <ConnectionOAuthStatusCard kind="api" name="Test API" authMode="oauth2_authorization_code" />,
      { wrapper },
    );

    expect(
      screen.getByText("Session reached the IdP-disclosed maximum lifetime."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/Previous session rejected by the upstream IdP/),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/returned refresh_expired/)).not.toBeInTheDocument();
    expect(screen.getByText("iam.example.io")).toBeInTheDocument();
  });

  it("for invalid_grant, the banner attributes the rejection to the IdP", () => {
    mockStatus.mockReturnValue({
      data: statusWithRevocation("invalid_grant", "iam.example.io"),
      isLoading: false,
    } as unknown as ReturnType<typeof useConnectionOAuthStatus>);

    render(
      <ConnectionOAuthStatusCard kind="api" name="Test API" authMode="oauth2_authorization_code" />,
      { wrapper },
    );

    expect(
      screen.getByText("Upstream IdP rejected the refresh token."),
    ).toBeInTheDocument();
    expect(screen.getByText("invalid_grant")).toBeInTheDocument();
  });

  it("for no_refresh_token, the banner names the local cause", () => {
    mockStatus.mockReturnValue({
      data: statusWithRevocation("no_refresh_token", "iam.example.io"),
      isLoading: false,
    } as unknown as ReturnType<typeof useConnectionOAuthStatus>);

    render(
      <ConnectionOAuthStatusCard kind="api" name="Test API" authMode="oauth2_authorization_code" />,
      { wrapper },
    );

    expect(
      screen.getByText("No refresh token is stored for this connection."),
    ).toBeInTheDocument();
  });
});

// renderDetailHint covers the parenthetical translation in isolation.
// These two tests guard against an honest mistake elsewhere in the
// History panel where someone might add a new locally-decided code
// without translating it.
describe("History row labels distinguish IdP-rejected from locally-decided", () => {
  it("renders the refresh_failed_revoked label for an IdP rejection", () => {
    const hint = renderDetailHint({
      id: "1",
      occurred_at: "2026-05-14T10:00:00Z",
      event_type: "refresh_failed_revoked",
      actor: "system:tool-call",
      detail: { idp_error_code: "invalid_grant" },
    });
    expect(hint).toBe("(invalid_grant)");
  });

  it("renders honest text for refresh_skipped_expired", () => {
    // refresh_skipped_expired events carry no detail payload — the type
    // alone names the verdict. renderDetailHint returns "" in that case
    // (no parenthetical), and EVENT_LABELS supplies the row's leading
    // label "Refresh skipped — IdP-disclosed deadline reached".
    const hint = renderDetailHint({
      id: "1",
      occurred_at: "2026-05-14T10:00:00Z",
      event_type: "refresh_skipped_expired",
      actor: "system:background-refresh",
    });
    expect(hint).toBe("");
  });
});
