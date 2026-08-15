import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type {
  ScriptContract,
  ScriptRun,
  ScriptRunDetail,
} from "@/api/portal/hooks/scripts";
import type { ScriptVersion } from "@/api/admin/types";
import { ScriptDetailPage } from "./ScriptDetailPage";

// The page composes four hooks over real child components, so every assertion
// here is what an owner actually reads on the page.
vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptContract: vi.fn(),
  usePortalScriptVersions: vi.fn(),
  useScriptRuns: vi.fn(),
  useScriptRun: vi.fn(),
  useSetScriptSchedule: vi.fn(),
  useSetScriptScheduleEnabled: vi.fn(),
  // The page size is the module's own constant, not a hook: the run history
  // states it when a result fills it.
  RUN_PAGE_SIZE: 25,
}));

import {
  useScriptContract,
  usePortalScriptVersions,
  useScriptRun,
  useScriptRuns,
  useSetScriptSchedule,
  useSetScriptScheduleEnabled,
} from "@/api/portal/hooks/scripts";

const mockContract = vi.mocked(useScriptContract);
const mockVersions = vi.mocked(usePortalScriptVersions);
const mockRuns = vi.mocked(useScriptRuns);
const mockRun = vi.mocked(useScriptRun);
const mockSaveSchedule = vi.mocked(useSetScriptSchedule);
const mockPauseSchedule = vi.mocked(useSetScriptScheduleEnabled);

const saveSchedule = vi.fn();
const pauseSchedule = vi.fn();

const onBack = vi.fn();
const onNavigate = vi.fn();

function query<T>(data: T, extra: Record<string, unknown> = {}) {
  return { data, isLoading: false, error: null, ...extra } as never;
}

const contract: ScriptContract = {
  id: "script-001",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  description: "Yesterday's sales by region.",
  owner_email: "sarah.chen@example.com",
  scope: "global",
  status: "active",
  enabled: true,
  params: [
    { name: "report_date", type: "date", description: "The business date.", required: true },
  ],
  approval: {
    approved: true,
    version: 2,
    approved_by: "admin@acme.example.com",
    approved_at: "2026-07-15T09:00:00Z",
  },
  schedule: {
    cron_spec: "0 7 * * 1-5",
    timezone: "America/Los_Angeles",
    enabled: true,
    next_run_at: "2026-08-15T14:00:00Z",
  },
};

const version: ScriptVersion = {
  id: "sver-001-v2",
  script_id: "script-001",
  version: 2,
  display_name: "Daily Sales Report",
  description: "Yesterday's sales by region.",
  source: 'rows = platform.query(connection="acme-warehouse", sql="SELECT 1")\n',
  author: "sarah.chen@example.com",
  author_roles: ["analyst"],
  status: "applied",
  approved_by: "admin@acme.example.com",
  approved_at: "2026-07-15T09:00:00Z",
  grants: {
    roles: ["analyst"],
    connections: ["acme-warehouse"],
    capabilities: ["platform.query", "platform.export"],
    destinations: [{ name: "portal", kind: "portal" }],
  },
  created_at: "2026-07-14T09:00:00Z",
};

const runs: ScriptRun[] = [
  {
    id: "run-001",
    status: "succeeded",
    trigger: "schedule",
    version: 2,
    fire_time: "2026-08-14T07:00:00Z",
    finished_at: "2026-08-14T07:00:08Z",
    duration_ms: 8_420,
    output_count: 1,
  },
  {
    id: "run-002",
    status: "failed",
    trigger: "schedule",
    version: 2,
    fire_time: "2026-08-13T07:00:00Z",
    finished_at: "2026-08-13T07:00:03Z",
    duration_ms: 3_110,
    error: "platform.query: relation does not exist",
    output_count: 0,
  },
  {
    id: "run-003",
    status: "skipped_overlap",
    trigger: "schedule",
    version: 2,
    fire_time: "2026-08-12T07:00:00Z",
    duration_ms: 0,
    output_count: 0,
  },
];

const runDetail: ScriptRunDetail = {
  id: "run-001",
  script_id: "script-001",
  version: 2,
  status: "succeeded",
  trigger: "schedule",
  duration_ms: 8_420,
  output_count: 2,
  fire_time: "2026-08-14T07:00:00Z",
  scheduled_for: "2026-08-14T07:00:00Z",
  started_at: "2026-08-14T07:00:00Z",
  finished_at: "2026-08-14T07:00:08Z",
  params: { report_date: "2026-08-13" },
  log: "querying acme-warehouse\nwrote asset version 42\n",
  metrics: { steps: 1_284, duration_ms: 8_420, queries: 1, exports: 1 },
  outputs: [
    {
      name: "daily-sales",
      destination: "portal",
      asset_id: "asset-1",
      asset_version: 42,
      format: "csv",
      row_count: 1_420,
      bytes: 98_304,
    },
    {
      name: "daily-sales",
      destination: "acme-crm-drop",
      bucket: "acme-exports",
      key: "sales/daily.csv",
      format: "csv",
      row_count: 1_420,
      bytes: 98_304,
    },
  ],
  attempt: 1,
  created_at: "2026-08-14T07:00:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  mockContract.mockReturnValue(query({ contract, owned: true }));
  mockVersions.mockReturnValue(query({ data: [version], total: 1 }));
  mockRuns.mockReturnValue(query({ data: runs, total: runs.length }));
  mockRun.mockReturnValue(query(runDetail));
  mockSaveSchedule.mockReturnValue({ mutate: saveSchedule, isPending: false, error: null } as never);
  mockPauseSchedule.mockReturnValue({ mutate: pauseSchedule, isPending: false, error: null } as never);
});

afterEach(cleanup);

function renderPage() {
  render(<ScriptDetailPage scriptId="script-001" onBack={onBack} onNavigate={onNavigate} />);
}

describe("ScriptDetailPage: the contract", () => {
  it("states what will execute, on what cadence, and what it takes", () => {
    renderPage();
    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
    expect(screen.getByText("Approved v2")).toBeInTheDocument();
    // The cadence reads on the contract itself and again on the control below it.
    expect(screen.getAllByText(/0 7 \* \* 1-5 \(America\/Los_Angeles\)/).length).toBeGreaterThan(0);
    expect(screen.getByText("report_date")).toBeInTheDocument();
    expect(screen.getByText("required")).toBeInTheDocument();
  });

  it("carries the gate's own refusal when nothing will run the script", () => {
    mockContract.mockReturnValue(
      query({
        contract: {
          ...contract,
          approval: {
            approved: false,
            refusal: "the script has no approved version, so nothing may execute it",
          },
        },
        owned: true,
      }),
    );
    renderPage();
    expect(screen.getByText("Nothing approved")).toBeInTheDocument();
    expect(screen.getByText(/no approved version, so nothing may execute it/)).toBeInTheDocument();
  });

  it("says a script takes no parameters rather than showing an empty table", () => {
    mockContract.mockReturnValue(query({ contract: { ...contract, params: [] }, owned: true }));
    renderPage();
    expect(screen.getByText(/takes no parameters/)).toBeInTheDocument();
  });

  it("reports a script that could not be loaded", () => {
    mockContract.mockReturnValue(query(undefined, { error: new Error("boom") }));
    renderPage();
    expect(screen.getByText(/could not be loaded/)).toBeInTheDocument();
  });
});

describe("ScriptDetailPage: what an owner may read", () => {
  it("hides the source and the runs from a caller who does not own the script", () => {
    mockContract.mockReturnValue(query({ contract, owned: false }));
    renderPage();
    expect(screen.queryByText("Version history")).not.toBeInTheDocument();
    expect(screen.queryByText("Run history")).not.toBeInTheDocument();
    expect(screen.getByText(/belongs to sarah.chen@example.com/)).toBeInTheDocument();
  });

  it("shows the served version's source and the grant approving it bound", () => {
    renderPage();
    expect(screen.getByText("Version history")).toBeInTheDocument();
    expect(screen.getByText("Executing")).toBeInTheDocument();
    // The served version opens by default, so the code that is running is on
    // screen without a click.
    expect(screen.getByText(/rows = platform\.query/)).toBeInTheDocument();
    expect(screen.getByText("platform.query, platform.export")).toBeInTheDocument();
    expect(screen.getByText("acme-warehouse")).toBeInTheDocument();
  });

  it("says a version nothing approved carries no grant", () => {
    mockVersions.mockReturnValue(
      query({
        data: [{ ...version, approved_by: undefined, approved_at: undefined, status: "draft" }],
        total: 1,
      }),
    );
    mockContract.mockReturnValue(
      query({ contract: { ...contract, approval: { approved: false } }, owned: true }),
    );
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Source" }));
    expect(screen.getByText(/No grant is bound to this version/)).toBeInTheDocument();
  });
});

describe("ScriptDetailPage: the run history", () => {
  it("shows every terminal state a run can end in", () => {
    renderPage();
    expect(screen.getByText("succeeded")).toBeInTheDocument();
    expect(screen.getByText("failed")).toBeInTheDocument();
    expect(screen.getByText("Skipped (overlap)")).toBeInTheDocument();
    // A failure says why in the listing rather than sending every reader into
    // the run to find out.
    expect(screen.getByText(/relation does not exist/)).toBeInTheDocument();
  });

  it("opens one run's log, parameters, and outputs", () => {
    renderPage();
    fireEvent.click(screen.getAllByRole("button", { name: "Open" })[0]!);
    expect(screen.getByText(/wrote asset version 42/)).toBeInTheDocument();
    expect(screen.getByText("report_date=2026-08-13")).toBeInTheDocument();
    expect(screen.getByText("1284 steps · 1 queries · 1 exports")).toBeInTheDocument();
  });

  it("links a portal asset and never a delivered object", () => {
    renderPage();
    fireEvent.click(screen.getAllByRole("button", { name: "Open" })[0]!);

    // The asset is still served by the platform, so it is reachable.
    fireEvent.click(screen.getByRole("button", { name: "daily-sales" }));
    expect(onNavigate).toHaveBeenCalledWith("/assets/asset-1");

    // The delivered copy is named but not offered as a link: those bytes left
    // the platform and nothing here will serve them back.
    expect(screen.getByText(/delivered to acme-exports\/sales\/daily.csv/)).toBeInTheDocument();
  });

  // A history capped at the page size says so: a script that runs every half
  // hour must not read as though its history began this morning.
  it("states the cap when the history fills a page", () => {
    const many = Array.from({ length: 25 }, (_, i) => ({ ...runs[0]!, id: `run-${i}` }));
    mockRuns.mockReturnValue(query({ data: many, total: many.length }));
    renderPage();
    expect(screen.getByText(/Showing the 25 most recent runs/)).toBeInTheDocument();
  });

  it("does not claim a cap on a history that fits", () => {
    renderPage();
    expect(screen.queryByText(/most recent runs/)).not.toBeInTheDocument();
  });

  it("says a script has never run rather than showing an empty table", () => {
    mockRuns.mockReturnValue(query({ data: [], total: 0 }));
    renderPage();
    expect(screen.getByText(/never run/)).toBeInTheDocument();
  });

  it("reports a truncated log as truncated", () => {
    mockRun.mockReturnValue(query({ ...runDetail, log_truncated: true }));
    renderPage();
    fireEvent.click(screen.getAllByRole("button", { name: "Open" })[0]!);
    expect(screen.getByText(/log was truncated at capture/)).toBeInTheDocument();
  });

  it("says a run printed nothing rather than showing an empty log box", () => {
    mockRun.mockReturnValue(query({ ...runDetail, log: undefined }));
    renderPage();
    fireEvent.click(screen.getAllByRole("button", { name: "Open" })[0]!);
    expect(screen.getByText("This run printed nothing.")).toBeInTheDocument();
  });
});

describe("ScriptDetailPage: the schedule", () => {
  it("lets the owner set a cadence and says what it will do", () => {
    renderPage();
    fireEvent.change(screen.getByLabelText("Cadence"), { target: { value: "0 6 * * 1" } });
    fireEvent.change(screen.getByLabelText("Timezone"), { target: { value: "UTC" } });
    fireEvent.click(screen.getByRole("button", { name: "Update schedule" }));

    expect(saveSchedule).toHaveBeenCalledWith({ cron: "0 6 * * 1", timezone: "UTC" });
  });

  it("offers the common cadences as a click rather than a remembered expression", () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Every weekday, 07:00" }));
    expect(screen.getByLabelText("Cadence")).toHaveValue("0 7 * * 1-5");
  });

  // Pausing is its own action: it never re-bases the next fire.
  it("pauses and resumes without touching the cadence", () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    expect(pauseSchedule).toHaveBeenCalledWith(false);
  });

  it("offers to schedule a script that has no cadence yet", () => {
    mockContract.mockReturnValue(query({ contract: { ...contract, schedule: undefined }, owned: true }));
    renderPage();
    expect(screen.getByText(/runs on demand/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Schedule it" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Pause" })).not.toBeInTheDocument();
  });

  // A cadence on an unapproved script is kept and inert, and the page says so
  // rather than implying an approval it cannot grant.
  it("says a cadence will not fire while nothing is approved", () => {
    mockContract.mockReturnValue(
      query({
        contract: { ...contract, approval: { approved: false }, schedule: undefined },
        owned: true,
      }),
    );
    renderPage();
    expect(screen.getByText(/will start firing as soon as a version/)).toBeInTheDocument();
  });

  it("reports a refused cadence instead of appearing to save it", () => {
    mockSaveSchedule.mockReturnValue({
      mutate: saveSchedule,
      isPending: false,
      error: new Error("cron expression is not parseable"),
    } as never);
    renderPage();
    expect(screen.getByText("cron expression is not parseable")).toBeInTheDocument();
  });

  it("does not offer the schedule to a caller who does not own the script", () => {
    mockContract.mockReturnValue(query({ contract, owned: false }));
    renderPage();
    expect(screen.queryByLabelText("Cadence")).not.toBeInTheDocument();
  });
});
