import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
import type { PendingReview, Script, ScriptVersion, VersionReview } from "@/api/admin/types";
import { AdminScriptsPage } from "./AdminScriptsPage";

// The page is assembled from six hooks; the components under them are real, so
// every assertion here exercises what a reviewer actually sees.
vi.mock("@/api/admin/hooks", () => ({
  useScriptReviews: vi.fn(),
  useAdminScripts: vi.fn(),
  useScriptVersions: vi.fn(),
  useScriptVersionReview: vi.fn(),
  useApproveScriptVersion: vi.fn(),
  useRejectScriptVersion: vi.fn(),
}));

import {
  useScriptReviews,
  useAdminScripts,
  useScriptVersions,
  useScriptVersionReview,
  useApproveScriptVersion,
  useRejectScriptVersion,
} from "@/api/admin/hooks";

const mockReviews = vi.mocked(useScriptReviews);
const mockScripts = vi.mocked(useAdminScripts);
const mockVersions = vi.mocked(useScriptVersions);
const mockReview = vi.mocked(useScriptVersionReview);
const mockApprove = vi.mocked(useApproveScriptVersion);
const mockReject = vi.mocked(useRejectScriptVersion);

const approveMutate = vi.fn();
const rejectMutate = vi.fn();
const navigate = vi.fn();

const pendingRow: PendingReview = {
  script_id: "script-001",
  script_name: "daily-sales-report",
  display_name: "Daily Sales Report",
  description: "Yesterday's sales by region.",
  owner_email: "sarah.chen@example.com",
  scope: "global",
  version: 3,
  version_id: "sver-001-v3",
  version_status: "draft",
  author: "sarah.chen@example.com",
  author_roles: ["analyst"],
  first_approval: false,
  created_at: new Date(Date.now() - 3 * 86_400_000).toISOString(),
};

const script: Script = {
  id: "script-001",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  description: "Yesterday's sales by region.",
  scope: "global",
  owner_email: "sarah.chen@example.com",
  status: "active",
  enabled: true,
  version: 2,
  approved_version_id: "sver-001-v2",
  updated_at: new Date().toISOString(),
};

const draftVersion: ScriptVersion = {
  id: "sver-001-v3",
  script_id: "script-001",
  version: 3,
  display_name: "Daily Sales Report",
  description: "",
  source: 'rows = platform.query(connection="acme-finance", sql="SELECT 1")\n',
  author: "sarah.chen@example.com",
  author_roles: ["analyst"],
  status: "draft",
  grants: { roles: [], connections: [], capabilities: [], destinations: [] },
  created_at: new Date().toISOString(),
};

function reviewPayload(overrides: Partial<VersionReview> = {}): VersionReview {
  return {
    version: draftVersion,
    referenced: {
      capabilities: ["platform.query", "platform.export"],
      connections: ["acme-finance", "acme-warehouse"],
      destinations: ["portal"],
      dynamic_connections: false,
      dynamic_destinations: false,
    },
    approved: {
      version: 2,
      version_id: "sver-001-v2",
      grants: {
        roles: ["analyst"],
        connections: ["acme-warehouse"],
        capabilities: ["platform.query"],
        destinations: [],
      },
      approved_by: "admin@acme.example.com",
      approved_at: new Date().toISOString(),
      source_diff: "--- v2 (approved)\n+++ v3 (under review)\n@@ -1 +1,2 @@\n+platform.export(name=\"x\")\n",
    },
    ...overrides,
  };
}

// query fakes the react-query result shape the page reads.
function query<T>(data: T, extra: Record<string, unknown> = {}) {
  return { data, isLoading: false, error: null, ...extra } as never;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockReviews.mockReturnValue(query({ data: [pendingRow], total: 1 }));
  mockScripts.mockReturnValue(query({ data: [script], total: 1 }));
  mockVersions.mockReturnValue(query({ data: [draftVersion], total: 1 }));
  mockReview.mockReturnValue(query(reviewPayload()));
  mockApprove.mockReturnValue({ mutate: approveMutate, isPending: false } as never);
  mockReject.mockReturnValue({ mutate: rejectMutate, isPending: false } as never);
});

afterEach(cleanup);

describe("AdminScriptsPage: the queue", () => {
  it("lists a pending version as a change to a running script", () => {
    render(<AdminScriptsPage onNavigate={navigate} />);
    // The name appears in the queue and again in the listing below it.
    expect(screen.getAllByText("Daily Sales Report").length).toBeGreaterThan(0);
    expect(screen.getByText("v3")).toBeInTheDocument();
    expect(screen.getByText("Change to a running script")).toBeInTheDocument();
    expect(screen.getByText(/waiting 3 days/)).toBeInTheDocument();
  });

  it("marks a script nobody has approved as a first approval", () => {
    mockReviews.mockReturnValue(
      query({ data: [{ ...pendingRow, first_approval: true }], total: 1 }),
    );
    render(<AdminScriptsPage onNavigate={navigate} />);
    expect(screen.getByText("First approval")).toBeInTheDocument();
  });

  it("says plainly when nothing is waiting", () => {
    mockReviews.mockReturnValue(query({ data: [], total: 0 }));
    render(<AdminScriptsPage onNavigate={navigate} />);
    expect(screen.getByText(/Nothing is waiting for approval/)).toBeInTheDocument();
  });

  it("reports a queue that could not be loaded instead of showing it as empty", () => {
    mockReviews.mockReturnValue(query(undefined, { error: new Error("boom") }));
    render(<AdminScriptsPage onNavigate={navigate} />);
    expect(screen.getByText(/review queue could not be loaded/)).toBeInTheDocument();
  });

  it("says when no scripts exist at all", () => {
    mockScripts.mockReturnValue(query({ data: [], total: 0 }));
    render(<AdminScriptsPage onNavigate={navigate} />);
    expect(screen.getByText(/No scripts have been authored yet/)).toBeInTheDocument();
  });
});

describe("AdminScriptsPage: the review", () => {
  function openReview() {
    render(<AdminScriptsPage onNavigate={navigate} />);
    fireEvent.click(screen.getByRole("button", { name: /^Review Daily Sales Report/ }));
    return screen.getByRole("dialog");
  }

  it("shows the code diff against the version running today", () => {
    const dialog = openReview();
    expect(within(dialog).getByText(/Code changes since v2/)).toBeInTheDocument();
    expect(within(dialog).getByText(/\+platform\.export/)).toBeInTheDocument();
  });

  it("marks the grant as widening when the code reaches somewhere new", () => {
    const dialog = openReview();
    expect(within(dialog).getByText("Widens authority")).toBeInTheDocument();
    // platform.export and acme-finance are both new against the approved grant.
    expect(within(dialog).getByText("+ platform.export")).toBeInTheDocument();
    expect(within(dialog).getByText("+ acme-finance")).toBeInTheDocument();
  });

  it("states the authority being bound and offers no way to change it", () => {
    const dialog = openReview();
    expect(within(dialog).getByText(/Authority this version would run with/)).toBeInTheDocument();
    expect(
      within(dialog).getByText(/Approving cannot change it/),
    ).toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/Add role/)).not.toBeInTheDocument();
  });

  it("approves with the grant the reviewer left selected", () => {
    const dialog = openReview();
    // Drop the export capability before approving.
    fireEvent.click(within(dialog).getByRole("button", { name: /platform\.export/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: /Approve/ }));

    expect(approveMutate).toHaveBeenCalledTimes(1);
    const [vars] = approveMutate.mock.calls[0] as [
      { scriptID: string; version: number; grant: { capabilities: string[] } },
    ];
    expect(vars.scriptID).toBe("script-001");
    expect(vars.version).toBe(3);
    expect(vars.grant.capabilities).toEqual(["platform.query"]);
  });

  it("refuses to approve a destination that does not say where it writes", () => {
    mockReview.mockReturnValue(
      query(
        reviewPayload({
          referenced: {
            capabilities: ["platform.query", "platform.export"],
            connections: ["acme-finance", "acme-warehouse"],
            destinations: ["acme-drop", "portal"],
            dynamic_connections: false,
            dynamic_destinations: false,
          },
        }),
      ),
    );
    const dialog = openReview();
    expect(within(dialog).getByText("Needs an address")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: /Approve/ })).toBeDisabled();
    expect(within(dialog).getByText(/Say where acme-drop writes/)).toBeInTheDocument();

    // Filling in the address is what clears the refusal, and the approval
    // carries what the reviewer typed.
    fireEvent.change(within(dialog).getByLabelText("acme-drop connection"), {
      target: { value: "acme-s3" },
    });
    fireEvent.change(within(dialog).getByLabelText("acme-drop bucket"), {
      target: { value: "acme-exports" },
    });
    fireEvent.change(within(dialog).getByLabelText("acme-drop prefix"), {
      target: { value: "weekly" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: /Approve/ }));

    const [vars] = approveMutate.mock.calls[0] as [
      { grant: { destinations: { name: string; bucket?: string; prefix?: string }[] } },
    ];
    expect(vars.grant.destinations).toEqual([
      { name: "acme-drop", kind: "s3", connection: "acme-s3", bucket: "acme-exports", prefix: "weekly" },
      { name: "portal", kind: "portal" },
    ]);
  });

  it("keeps a part-edited grant when the review is refetched underneath it", () => {
    const { rerender } = render(<AdminScriptsPage onNavigate={navigate} />);
    fireEvent.click(screen.getByRole("button", { name: /^Review Daily Sales Report/ }));
    const dialog = screen.getByRole("dialog");

    fireEvent.click(within(dialog).getByRole("button", { name: /platform\.export/ }));
    // A background refetch hands back an equal payload with a new identity.
    mockReview.mockReturnValue(query(reviewPayload()));
    rerender(<AdminScriptsPage onNavigate={navigate} />);

    fireEvent.click(within(dialog).getByRole("button", { name: /Approve/ }));
    const [vars] = approveMutate.mock.calls[0] as [{ grant: { capabilities: string[] } }];
    expect(vars.grant.capabilities).toEqual(["platform.query"]);
  });

  it("attributes an approval failure to the attempt that produced it", () => {
    approveMutate.mockImplementation((_vars, opts: { onError: (e: unknown) => void }) => {
      opts.onError(new Error("the grant does not cover connections this version queries"));
    });
    const dialog = openReview();
    fireEvent.click(within(dialog).getByRole("button", { name: /Approve/ }));
    expect(
      within(dialog).getByText(/does not cover connections/),
    ).toBeInTheDocument();
  });

  it("rejects a pending draft", () => {
    const dialog = openReview();
    fireEvent.click(within(dialog).getByRole("button", { name: /Reject/ }));
    expect(rejectMutate).toHaveBeenCalledWith(
      { scriptID: "script-001", version: 3 },
      expect.anything(),
    );
  });

  it("offers no rejection for a version that is not a pending draft", () => {
    mockReview.mockReturnValue(
      query(reviewPayload({ version: { ...draftVersion, status: "applied" } })),
    );
    const dialog = openReview();
    expect(within(dialog).queryByRole("button", { name: /Reject/ })).not.toBeInTheDocument();
    expect(within(dialog).getByText(/nothing to reject here/)).toBeInTheDocument();
  });

  it("refuses to offer an approval the store would reject", () => {
    for (const [status, reason] of [
      ["rejected", /was rejected and cannot be approved/],
      ["superseded", /was superseded when a later one was approved/],
    ] as const) {
      cleanup();
      mockReview.mockReturnValue(
        query(reviewPayload({ version: { ...draftVersion, status } })),
      );
      const dialog = openReview();
      expect(within(dialog).getByRole("button", { name: /Approve/ })).toBeDisabled();
      expect(within(dialog).queryByRole("button", { name: /Reject/ })).not.toBeInTheDocument();
      expect(within(dialog).getByText(reason)).toBeInTheDocument();
    }
  });

  it("warns that a computed connection cannot be checked by reading the code", () => {
    mockReview.mockReturnValue(
      query(
        reviewPayload({
          referenced: {
            capabilities: ["platform.query"],
            connections: [],
            destinations: [],
            dynamic_connections: true,
            dynamic_destinations: false,
          },
        }),
      ),
    );
    const dialog = openReview();
    expect(within(dialog).getByText(/computes at least one connection name/)).toBeInTheDocument();
  });

  it("shows the whole source when there is no approved version to diff against", () => {
    mockReview.mockReturnValue(query(reviewPayload({ approved: undefined })));
    const dialog = openReview();
    expect(within(dialog).getByText("Nothing of this script runs yet")).toBeInTheDocument();
    expect(within(dialog).getByText(/platform\.query\(connection="acme-finance"/)).toBeInTheDocument();
  });

  it("reports a version that could not be loaded", () => {
    mockReview.mockReturnValue(query(undefined, { error: new Error("boom") }));
    const dialog = openReview();
    expect(within(dialog).getByText(/could not be loaded/)).toBeInTheDocument();
  });

  it("shows what validation found", () => {
    mockReview.mockReturnValue(
      query(
        reviewPayload({
          findings: [
            { severity: "warning", line: 8, message: "print output is truncated at 64KB.", hint: "Use an export." },
          ],
        }),
      ),
    );
    const dialog = openReview();
    expect(within(dialog).getByText(/print output is truncated/)).toBeInTheDocument();
    expect(within(dialog).getByText(/line 8:/)).toBeInTheDocument();
  });
});

describe("AdminScriptsPage: opening a script", () => {
  it("opens the script itself, on the same page its owner opens", () => {
    render(<AdminScriptsPage onNavigate={navigate} />);

    fireEvent.click(screen.getByRole("row", { name: /Daily Sales Report/ }));

    // The listing lists; the detail page is where an administrator runs, edits,
    // schedules and decides (#1367). A second surface here would have been a
    // second answer to what an administrator can do with a script.
    expect(navigate).toHaveBeenCalledWith("/admin/scripts/script-001");
  });

  it("names the audience, which is what decides whether a version is reviewed here", () => {
    mockScripts.mockReturnValue(
      query({ data: [script, { ...script, id: "script-002", name: "mine", display_name: "My Own Report", scope: "personal" }], total: 2 }),
    );
    render(<AdminScriptsPage onNavigate={navigate} />);

    expect(screen.getByRole("row", { name: /Daily Sales Report/ })).toHaveTextContent("everyone");
    expect(screen.getByRole("row", { name: /My Own Report/ })).toHaveTextContent("its owner");
  });
});
