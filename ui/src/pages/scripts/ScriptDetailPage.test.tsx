import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
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
  // The schedule editor's hooks. The editor has its own tests; here they only
  // have to answer, so the page composes with the one section that mutates.
  useScriptSchedule: vi.fn(),
  useSetScriptSchedule: vi.fn(),
  useSetScriptSchedulePaused: vi.fn(),
  // The source editor's and the run panel's hooks. Each has its own tests;
  // here they only have to answer, so the page composes with every section.
  useSaveScriptSource: vi.fn(),
  useValidateScriptSource: vi.fn(),
  useDryRunScript: vi.fn(),
  useScriptConnections: vi.fn(),
  useRunScript: vi.fn(),
  // The state card's hooks (#1537). The card has its own tests; here they only
  // have to answer, so the page composes with the section that carries state.
  useScriptState: vi.fn(),
  useSetScriptState: vi.fn(),
  useClearScriptState: vi.fn(),
  // The page size is the module's own constant, not a hook: the run history
  // states it when a result fills it.
  RUN_PAGE_SIZE: 25,
}));

// The owner transfer's hook (#1404). The control has its own tests; here it
// only has to answer, so the page composes with the administrator's section.
vi.mock("@/api/portal/hooks/scriptOwner", () => ({
  useTransferScriptOwner: vi.fn(),
}));

// Removing the script (#1575) has its own tests; here the hook only has to
// answer, so the page composes with the section that offers the delete.
vi.mock("@/api/portal/hooks/scriptDelete", () => ({
  useDeleteScript: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

// Everything this script has produced (#1569) has its own tests; here it only
// has to answer, so the page renders without a query client. The fixture is one
// asset, which is what the section assertion below reads.
vi.mock("@/api/portal/hooks/producers", () => ({
  useScriptProduced: () => ({
    data: {
      data: [
        {
          target_kind: "asset",
          target_id: "ast-001",
          name: "Q4 Revenue Dashboard",
          created: true,
          first_write_at: "2026-07-01T09:00:00Z",
          last_write_at: "2026-08-20T09:00:00Z",
          write_count: 41,
          last_version: 8,
        },
      ],
      total: 1,
    },
    isLoading: false,
    error: null,
  }),
}));

// The transfer control offers the known-users directory as type-ahead. It has
// its own tests; here it only has to answer, so the page renders without a
// query client.
vi.mock("@/api/portal/hooks", () => ({
  useDirectoryUsers: () => ({ data: undefined }),
}));

// The transfer section is an administrator's, so the page reads the auth
// store. Drive is_admin per test.
vi.mock("@/stores/auth", () => ({
  useAuthStore: (selector: (s: { isAdmin: () => boolean }) => unknown) =>
    selector({ isAdmin: () => admin }),
}));

let admin = false;

import {
  useDryRunScript,
  useScriptContract,
  usePortalScriptVersions,
  useRunScript,
  useScriptConnections,
  useScriptRun,
  useScriptRuns,
  useSaveScriptSource,
  useScriptSchedule,
  useScriptState,
  useSetScriptSchedule,
  useSetScriptSchedulePaused,
  useSetScriptState,
  useClearScriptState,
  useValidateScriptSource,
} from "@/api/portal/hooks/scripts";
import { useTransferScriptOwner } from "@/api/portal/hooks/scriptOwner";

const mockContract = vi.mocked(useScriptContract);
const mockTransfer = vi.mocked(useTransferScriptOwner);
const mockVersions = vi.mocked(usePortalScriptVersions);
const mockRuns = vi.mocked(useScriptRuns);
const mockRun = vi.mocked(useScriptRun);
const mockSchedule = vi.mocked(useScriptSchedule);
const mockSaveSchedule = vi.mocked(useSetScriptSchedule);
const mockPauseSchedule = vi.mocked(useSetScriptSchedulePaused);
const mockSaveSource = vi.mocked(useSaveScriptSource);
const mockValidateSource = vi.mocked(useValidateScriptSource);
const mockDryRun = vi.mocked(useDryRunScript);
const mockConnections = vi.mocked(useScriptConnections);
const mockRunScript = vi.mocked(useRunScript);
const mockState = vi.mocked(useScriptState);
const mockSetState = vi.mocked(useSetScriptState);
const mockClearState = vi.mocked(useClearScriptState);

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
  status: "active",
  enabled: true,
  params: [
    { name: "report_date", type: "date", description: "The business date.", required: true },
  ],
  version: 2,
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
  mockSchedule.mockReturnValue(
    query({
      id: "sched-001",
      script_id: "script-001",
      cron_spec: "0 7 * * 1-5",
      timezone: "America/Los_Angeles",
      params: { report_date: "${fire_date}" },
      enabled: true,
      next_run_at: "2026-08-16T14:00:00Z",
    }),
  );
  mockSaveSchedule.mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
  mockPauseSchedule.mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
  mockSaveSource.mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
  mockValidateSource.mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
  mockDryRun.mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
  mockRunScript.mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
  mockTransfer.mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
  mockConnections.mockReturnValue(query(undefined));
  mockState.mockReturnValue(query({ state: { synced_through: "2026-08-13" }, revision: 41, run_id: "run-001" }));
  mockSetState.mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
  mockClearState.mockReturnValue({ mutate: vi.fn(), isPending: false } as never);
});

afterEach(cleanup);

function renderPage() {
  render(<ScriptDetailPage scriptId="script-001" onBack={onBack} onNavigate={onNavigate} />);
}

describe("ScriptDetailPage: the details", () => {
  it("states what will execute, on what schedule, and what it takes", () => {
    renderPage();
    expect(screen.getByText("Daily Sales Report")).toBeInTheDocument();
    // The badge is the run gate's answer: the latest saved version runs.
    expect(screen.getByText("Runs v2")).toBeInTheDocument();
    expect(screen.getByText("v2, the latest saved version")).toBeInTheDocument();
    // The cadence in words, as every other surface states it (#1407): the
    // expression is read and written in the schedule editor below.
    expect(
      screen.getByText("Every weekday at 7:00 AM, America/Los_Angeles"),
    ).toBeInTheDocument();
    // The parameters are read in the same section as the facts (#1406), not in
    // a card of their own below them. Scoped to that table: the schedule
    // editor below also names the parameter, because that is the box its
    // binding goes in.
    const details = within(screen.getByRole("heading", { name: "Details" }).closest("div[data-slot=card]")!);
    expect(details.getByText("report_date")).toBeInTheDocument();
    expect(details.getByText("required")).toBeInTheDocument();
  });

  // The section used to be titled "Contract", which is the platform's word for
  // it and not a word anybody debugging a script would reach for (#1406).
  it("calls the summary facts what they are", () => {
    renderPage();
    expect(screen.getByRole("heading", { name: "Details" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Contract" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Parameters" })).not.toBeInTheDocument();
  });

  // The order is the order somebody debugging a script reads in: what it is,
  // when it fires, what it says about itself, the code, and what the code has
  // been doing. Source and run history are adjacent on purpose — an error in
  // the history is answered by the text above it (#1406).
  it("orders the sections for the person debugging the script", () => {
    renderPage();
    const sections = screen
      .getAllByRole("heading", { level: 3 })
      .map((h) => h.textContent);
    expect(sections).toEqual([
      "Details",
      "Schedule",
      "About",
      "Source",
      "Run history",
      "Files written (1)",
      "State",
      // Removing the script is last, because it is the last thing anybody does
      // to one (#1575).
      "Delete",
    ]);
  });

  // Running the script lives with the code it executes (#1406). There is no
  // "Run now" section any more, and no second parameter form to fill.
  it("runs the script from the section that holds its code", () => {
    renderPage();
    const source = within(screen.getByRole("heading", { name: "Source" }).closest("div[data-slot=card]")!);
    expect(source.getByRole("button", { name: "Run" })).toBeInTheDocument();
    expect(source.getByRole("button", { name: "Dry run" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Run now" })).not.toBeInTheDocument();
  });

  it("carries the gate's own refusal when nothing will run the script", () => {
    mockContract.mockReturnValue(
      query({
        contract: { ...contract, enabled: false, refusal: "the script is disabled" },
        owned: true,
      }),
    );
    renderPage();
    expect(screen.getByText("Not running")).toBeInTheDocument();
    expect(screen.getByText("the script is disabled")).toBeInTheDocument();
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

  // The description used to be the page header's subtitle, which is a one-line
  // slot, so a page of markdown arrived as one run-on line and authors wrote
  // one-line captions to fit it (#1369). It is a document now, and it renders
  // in the section the owner also writes it in.
  it("renders the description as a document rather than as a header caption", () => {
    mockContract.mockReturnValue(
      query({
        contract: { ...contract, description: "## What it produces\n\nOne CSV." },
        owned: true,
      }),
    );
    renderPage();

    expect(screen.getByRole("heading", { level: 2, name: "What it produces" })).toBeInTheDocument();
    expect(screen.queryByText(/## What it produces/)).not.toBeInTheDocument();
  });
});

describe("ScriptDetailPage: what an owner may read", () => {
  it("hides the source and the runs from a caller who does not own the script", () => {
    mockContract.mockReturnValue(query({ contract, owned: false }));
    renderPage();
    expect(screen.queryByText("Version history")).not.toBeInTheDocument();
    expect(screen.queryByText("Run history")).not.toBeInTheDocument();
    // The state is the runs' input and belongs to the same reader (#1537).
    expect(screen.queryByRole("heading", { name: "State" })).not.toBeInTheDocument();
    // And so is removing it (#1575): the control carries the same reach the
    // route enforces, so a reader who is offered it is one the route admits.
    expect(screen.queryByRole("button", { name: "Delete script" })).not.toBeInTheDocument();
  });

  // Removing a script is the owner's and an administrator's, which is the rule
  // the tool's delete already applies (#1575). It was the one verb in a
  // script's life that sent a person out of the portal and into an agent
  // session.
  it("offers the delete to a reader the script belongs to", () => {
    renderPage();

    expect(screen.getByRole("button", { name: "Delete script" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 3, name: "Delete" })).toBeInTheDocument();
  });

  // The state a script carries between runs is read with its runs (#1537): the
  // card is the owner's, and the details say what the source does with it.
  it("states what the script does with its state, and offers the state to its owner", () => {
    mockContract.mockReturnValue(
      query({
        contract: { ...contract, state: { reads_state: true, saves_state: true, revision: 41 } },
        owned: true,
      }),
    );
    renderPage();
    expect(screen.getByText("carried between runs, revision 41")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "State" })).toBeInTheDocument();
    expect(mockState).toHaveBeenCalledWith("script-001", true);
  });

  it("says a script keeps no state when its source neither reads nor saves any", () => {
    mockContract.mockReturnValue(
      query({
        contract: { ...contract, state: { reads_state: false, saves_state: false, revision: 0 } },
        owned: true,
      }),
    );
    renderPage();
    expect(screen.getByText("keeps none")).toBeInTheDocument();
  });

  // Moving a script to somebody else is the one control on this page that is
  // not the owner's own (#1404).
  it("offers the owner transfer to an administrator and to nobody else", () => {
    renderPage();
    expect(screen.queryByRole("button", { name: "Transfer ownership" })).not.toBeInTheDocument();

    admin = true;
    cleanup();
    renderPage();

    expect(screen.getByRole("button", { name: "Transfer ownership" })).toBeInTheDocument();
    expect(screen.getByText(/only person who sees it/)).toBeInTheDocument();
    // It comes after everything the owner reads and does (#1406), and before
    // the delete, which is last on the page for every reader (#1575).
    expect(
      screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent),
    ).toEqual([
      "Details",
      "Schedule",
      "About",
      "Source",
      "Run history",
      "Files written (1)",
      "State",
      "Owner",
      "Delete",
    ]);
    admin = false;
  });

  // The version history is folded into Source behind a reveal (#1406): the
  // editor already holds the version that runs, so what the history adds is
  // the versions before it.
  it("opens a version's source and the roles a run of it presents", () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: /Version history/ }));
    fireEvent.click(screen.getByText("v2"));

    expect(screen.getByText(/rows = platform\.query/)).toBeInTheDocument();
    // The authority line: a run presents the roles its author held at the save.
    expect(screen.getByText("analyst")).toBeInTheDocument();
    expect(screen.getByText(/the roles its author held at the save/)).toBeInTheDocument();
  });

  it("says a version whose author held no roles could call nothing", () => {
    mockVersions.mockReturnValue(
      query({ data: [{ ...version, author_roles: [] }], total: 1 }),
    );
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: /Version history/ }));
    fireEvent.click(screen.getByText("v2"));

    expect(screen.getByText("no roles")).toBeInTheDocument();
    expect(screen.getByText(/deny-all persona and could call nothing/)).toBeInTheDocument();
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
    fireEvent.click(screen.getAllByRole("row", { name: /succeeded/ })[0]!);
    expect(screen.getByText(/wrote asset version 42/)).toBeInTheDocument();
    expect(screen.getByText("report_date=2026-08-13")).toBeInTheDocument();
    expect(screen.getByText("1284 steps · 1 queries · 1 exports")).toBeInTheDocument();
  });

  // #1405: the cross-script Runs listing links to one run, and that address
  // lands on the run it named rather than on a history the reader has to find
  // it in again.
  it("opens the run the address names, without a click", () => {
    render(
      <ScriptDetailPage
        scriptId="script-001"
        openRunId="run-001"
        onBack={onBack}
        onNavigate={onNavigate}
      />,
    );
    expect(screen.getByText(/wrote asset version 42/)).toBeInTheDocument();
  });

  // The address is read every time it names a different run, not only when
  // this page first mounts: following one run link and then another leaves the
  // history mounted, and a run that stayed closed because the section already
  // existed reads exactly like one that never opened.
  it("opens the run the address names when the address changes", () => {
    mockRun.mockImplementation((_scriptId, runId) =>
      query({ ...runDetail, id: String(runId), log: `log of ${String(runId)}` }),
    );
    const { rerender } = render(
      <ScriptDetailPage
        scriptId="script-001"
        openRunId="run-002"
        onBack={onBack}
        onNavigate={onNavigate}
      />,
    );
    expect(screen.getByText(/log of run-002/)).toBeInTheDocument();

    rerender(
      <ScriptDetailPage
        scriptId="script-001"
        openRunId="run-001"
        onBack={onBack}
        onNavigate={onNavigate}
      />,
    );
    expect(screen.getByText(/log of run-001/)).toBeInTheDocument();
    expect(screen.queryByText(/log of run-002/)).not.toBeInTheDocument();
  });

  it("links a portal asset and never a delivered object", () => {
    renderPage();
    fireEvent.click(screen.getAllByRole("row", { name: /succeeded/ })[0]!);

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
    fireEvent.click(screen.getAllByRole("row", { name: /succeeded/ })[0]!);
    expect(screen.getByText(/log was truncated at capture/)).toBeInTheDocument();
  });

  it("says a run printed nothing rather than showing an empty log box", () => {
    mockRun.mockReturnValue(query({ ...runDetail, log: undefined }));
    renderPage();
    fireEvent.click(screen.getAllByRole("row", { name: /succeeded/ })[0]!);
    expect(screen.getByText("This run printed nothing.")).toBeInTheDocument();
  });

  // #1362: the table laid out six columns and needed horizontal room it does
  // not have. Trigger and version repeat down the whole column, so they are
  // folded into the row they qualify rather than each holding a column open.
  it("carries the run in three columns, with the repeating fields folded in", () => {
    renderPage();
    const runTable = screen.getByRole("columnheader", { name: "Run" }).closest("table")!;
    expect(
      within(runTable).getAllByRole("columnheader").map((h) => h.textContent),
    ).toEqual(["Run", "Duration", "Produced"]);
    expect(within(runTable).getAllByText("schedule · v2").length).toBe(runs.length);
  });

  // A bare count needs its noun to mean anything, and the noun is what a reader
  // scanning this column is looking for.
  it("names what a run produced rather than printing a bare count", () => {
    renderPage();
    expect(screen.getByText("1 output")).toBeInTheDocument();
    expect(screen.getAllByText("nothing").length).toBe(2);
  });

  // A table cell does not wrap by default, so a Starlark traceback sat on one
  // line and the page scrolled sideways to read the message somebody opened
  // the history for (#1406).
  it("wraps a failure message rather than holding the page open sideways", () => {
    renderPage();
    const failure = screen.getByText(/relation does not exist/);
    expect(failure.className).toContain("whitespace-normal");
    expect(failure.className).not.toContain("whitespace-nowrap");

    fireEvent.click(screen.getAllByRole("row", { name: /succeeded/ })[0]!);
    const detail = screen.getByText("Requested by").closest("td")!;
    expect(detail.className).toContain("whitespace-normal");
  });

  // The expanded detail and the failure line span the table however wide it is;
  // a stale span would leave either sitting in the first column.
  it("spans the whole table with the failure line and the expanded run", () => {
    renderPage();
    const failure = screen.getByText(/relation does not exist/);
    expect(failure.getAttribute("colspan")).toBe("3");
    fireEvent.click(screen.getAllByRole("row", { name: /succeeded/ })[0]!);
    expect(
      screen.getByText("Requested by").closest("td")?.getAttribute("colspan"),
    ).toBe("3");
  });
});
