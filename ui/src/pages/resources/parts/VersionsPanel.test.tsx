import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { VersionsPanel } from "./VersionsPanel";
import type { Resource, ResourceVersionListResponse } from "@/api/resources/types";

// A revision the platform wrote on the uploader's behalf -- a registration that
// had to correct a CSV before it could read it -- is otherwise indistinguishable
// in this panel from one its owner uploaded (#1450). What is asserted here is
// that the reason travels: it is on the row when the revision carries one, and
// absent when the revision is an upload, where the uploader is the answer.

const RESOURCE: Resource = {
  id: "res-008",
  scope: "persona",
  scope_id: "inventory-analyst",
  category: "reference",
  filename: "seasonal-factors.csv",
  display_name: "Seasonal Factors",
  description: "d",
  mime_type: "text/csv",
  size_bytes: 12_288,
  s3_key: "resources/persona/inventory-analyst/reference/seasonal-factors.csv",
  uri: "s3://acme-platform/resources/persona/inventory-analyst/reference/seasonal-factors.csv",
  tags: [],
  uploader_sub: "rachel-analyst",
  uploader_email: "rachel.thompson@example.com",
  created_at: "2026-08-03T10:00:00Z",
  updated_at: "2026-08-17T10:00:00Z",
};

const SUMMARY = "rewrote the CRLF line endings as newlines and put 3 rows back onto one line";

const TRAIL: ResourceVersionListResponse = {
  current: 2,
  max_versions: 10,
  versions: [
    {
      resource_id: "res-008",
      version: 2,
      mime_type: "text/csv",
      size_bytes: 12_288,
      s3_key: "resources/persona/inventory-analyst/res-008/v/rev2/seasonal-factors.csv",
      uploader_sub: "rachel-analyst",
      uploader_email: "rachel.thompson@example.com",
      change_summary: SUMMARY,
      created_at: "2026-08-17T10:00:00Z",
    },
    {
      resource_id: "res-008",
      version: 1,
      mime_type: "text/csv",
      size_bytes: 12_320,
      s3_key: "resources/persona/inventory-analyst/reference/seasonal-factors.csv",
      uploader_sub: "rachel-analyst",
      uploader_email: "rachel.thompson@example.com",
      created_at: "2026-08-03T10:00:00Z",
    },
  ],
};

// stubVersions answers the one read the panel makes. Anything else is a failure
// rather than a silent empty body.
function stubVersions(body: ResourceVersionListResponse) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/versions")) {
        return Promise.resolve(new Response(JSON.stringify(body), { status: 200 }));
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    }),
  );
}

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <VersionsPanel resource={RESOURCE} canModify />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  stubVersions(TRAIL);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("the resource version trail", () => {
  it("says why a revision written on the uploader's behalf changed the file", async () => {
    renderPanel();
    await waitFor(() => {
      expect(screen.getByTestId("resource-version-2")).toBeInTheDocument();
    });
    expect(screen.getByTestId("resource-version-summary-2")).toHaveTextContent(SUMMARY);
  });

  it("says nothing beside a revision its uploader picked themselves", async () => {
    renderPanel();
    await waitFor(() => {
      expect(screen.getByTestId("resource-version-1")).toBeInTheDocument();
    });
    expect(screen.queryByTestId("resource-version-summary-1")).not.toBeInTheDocument();
  });
});
