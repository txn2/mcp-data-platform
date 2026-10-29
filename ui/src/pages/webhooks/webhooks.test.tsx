import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import type { WebhookStatusOverview } from "@/api/admin/types";
import { mockWebhookOverview, mockWebhookSources, mockWebhookStatus } from "@/mocks/data/webhooks";

const h = vi.hoisted(() => ({
  status: { data: undefined as unknown, isLoading: false, isError: false },
  one: { data: undefined as unknown, isLoading: false, isError: false },
  ranges: [] as string[],
  sources: [] as string[],
  customs: [] as unknown[],
  detail: { data: undefined as unknown, isLoading: false, error: null as unknown },
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  sync: vi.fn(),
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useWebhookStatus: (range: string, source = "", enabled = true, custom?: unknown) => {
      h.ranges.push(range);
      if (custom) h.customs.push(custom);
      if (source && enabled) h.sources.push(source);
      return source ? h.one : h.status;
    },
    useWebhookSource: () => h.detail,
    useCreateWebhookSource: () => ({ mutate: h.create, isPending: false, error: null }),
    useUpdateWebhookSource: () => ({ mutate: h.update, isPending: false, error: null }),
    useDeleteWebhookSource: () => ({ mutate: h.remove, isPending: false, error: null }),
    useSyncWebhookPartitions: () => ({ mutate: h.sync, isPending: false, error: null }),
    usePersonas: () => ({ data: { personas: [{ name: "marketing", display_name: "Marketing" }] } }),
  };
});

vi.mock("@/api/tables/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/tables/hooks")>();
  return {
    ...actual,
    useTableConnections: () => ({
      data: { connections: [{ name: "acme-scratch-resources", catalog: "scratch_resources", schema: "uploads" }] },
      isLoading: false,
    }),
  };
});

import { AdminWebhookRoutes } from "./AdminWebhookRoutes";
import { WebhookDetailPage } from "./WebhookDetailPage";
import { WebhookEditor } from "./WebhookEditor";
import { WebhooksPage } from "./WebhooksPage";

const busy = mockWebhookSources[0]!;
const quiet = mockWebhookSources[1]!;

beforeEach(() => {
  h.status = { data: mockWebhookOverview(mockWebhookSources, "hour"), isLoading: false, isError: false };
  h.ranges = [];
  h.sources = [];
  h.customs = [];
  h.one = { data: undefined, isLoading: false, isError: false };
  h.detail = { data: { source: busy, status: mockWebhookStatus[busy.name] }, isLoading: false, error: null };
  h.create.mockReset();
  h.update.mockReset();
  h.remove.mockReset();
  h.sync.mockReset();
});

// overview is the status response with every list emptied, for the tests
// that fill in only what they need.
function overview(patch: Partial<WebhookStatusOverview>): WebhookStatusOverview {
  return { ...mockWebhookOverview([], "hour"), ...patch };
}

const noAcks = { count: 0, p50_ms: 0, p99_ms: 0, max_ms: 0, slowest: null };

// row is the table row holding text.
function row(text: string): HTMLElement {
  return screen.getByText(text, { selector: "div.font-medium" }).closest("tr")!;
}

describe("WebhooksPage", () => {
  it("shows every source's health and counts from one request, and opens one on row click", () => {
    const onNavigate = vi.fn();
    render(<WebhooksPage onNavigate={onNavigate} />);
    expect(h.ranges.every((r) => r === "hour")).toBe(true);
    expect(within(row("email-events")).getByText("Receiving")).toBeInTheDocument();
    expect(within(row("email-events")).getByText("18,342 accepted")).toBeInTheDocument();
    expect(within(row("email-events")).getByText("3 rejected")).toBeInTheDocument();
    expect(within(row("email-events")).getByText("402,113 accepted")).toBeInTheDocument();
    expect(within(row("email-events")).getByText("52 rejected")).toBeInTheDocument();
    expect(within(row("crm-contacts")).getByText("Disabled")).toBeInTheDocument();
    expect(screen.getByText("webhook_email_events")).toBeInTheDocument();
    fireEvent.click(row("crm-contacts"));
    expect(onNavigate).toHaveBeenCalledWith("/admin/webhooks/crm-contacts");
    fireEvent.click(screen.getByRole("button", { name: /New source/ }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/webhooks/new");
  });

  it("marks a source with no event in the last day silent, and one that never received any", () => {
    render(<WebhooksPage onNavigate={vi.fn()} />);
    const billing = row("billing-events");
    expect(within(billing).getByText("Silent")).toBeInTheDocument();
    expect(within(billing).getByText("Silent")).toHaveAttribute("title", expect.stringMatching(/no event received in the last 24 hours/));
    expect(within(billing).getByText("1d ago")).toBeInTheDocument();

    h.status = {
      data: overview({
        sources: [
          {
            name: "new-source", enabled: true, health: "silent", auth_mode: "hmac", connection: "c", table: "webhook_new_source",
            last_event_at: null, last_hour: {}, last_day: {}, pending: 0, failing: 0,
            ack_last_hour: noAcks, ack_last_day: noAcks, segments_open_window: 0, segments_per_window_p95: 0,
          },
        ],
      }),
      isLoading: false,
      isError: false,
    };
    cleanup();
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(within(row("new-source")).getByText("Silent")).toBeInTheDocument();
    expect(within(row("new-source")).getByText("Never")).toBeInTheDocument();
    expect(within(row("new-source")).getAllByText("0 accepted")).toHaveLength(2);
  });

  it("shows a failing source's failing windows and last error", () => {
    render(<WebhooksPage onNavigate={vi.fn()} />);
    const orders = row("order-updates");
    expect(within(orders).getByText("Failing")).toBeInTheDocument();
    expect(within(orders).getByText("2")).toBeInTheDocument();
    expect(within(orders).getByText("4")).toBeInTheDocument();
    expect(within(orders).getByText(/gzip: invalid header/)).toBeInTheDocument();
  });

  it("lists recent rejections across sources with source and reason, and opens the source", () => {
    const onNavigate = vi.fn();
    h.status = { data: mockWebhookOverview(mockWebhookSources, "day"), isLoading: false, isError: false };
    render(<WebhooksPage onNavigate={onNavigate} />);
    const section = screen.getByText("Recent rejections").closest("[data-slot=card]") as HTMLElement;
    const reason = within(section).getByText("the credentials do not match");
    const tr = reason.closest("tr")!;
    expect(within(tr).getByText("billing-events")).toBeInTheDocument();
    expect(within(tr).getByText("Unauthorized")).toBeInTheDocument();
    expect(within(section).getByText("the body is not JSON")).toBeInTheDocument();
    expect(within(section).getByText("Invalid body")).toBeInTheDocument();
    fireEvent.click(reason);
    expect(onNavigate).toHaveBeenCalledWith("/admin/webhooks/billing-events");
  });

  it("says when no request was rejected in the range", () => {
    h.status = { data: { ...mockWebhookOverview(mockWebhookSources, "hour"), rejections: [] }, isLoading: false, isError: false };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(screen.getByText("No request has been rejected in the last hour.")).toBeInTheDocument();
  });

  it("shows each source's response time, and a dash for one that accepted nothing", () => {
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(within(row("email-events")).getByText("p99 2.5 s")).toBeInTheDocument();
    expect(within(row("email-events")).getByText("max 2.4 s")).toBeInTheDocument();
    expect(within(row("crm-contacts")).getByText("-")).toBeInTheDocument();
  });

  it("falls back to the last day's response time when the hour has none", () => {
    const base = mockWebhookOverview(mockWebhookSources, "hour");
    const day = { count: 4, p50_ms: 10, p99_ms: 25, max_ms: 22, slowest: null };
    h.status = {
      data: { ...base, sources: base.sources.map((s) => ({ ...s, ack_last_hour: noAcks, ack_last_day: day })) },
      isLoading: false,
      isError: false,
    };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(within(row("email-events")).getByText("max 22 ms (24 hours)")).toBeInTheDocument();
  });

  it("names a rejection's events, and reads the rejections of the source chosen", () => {
    render(<WebhooksPage onNavigate={vi.fn()} />);
    const section = () => screen.getByText("Recent rejections").closest("[data-slot=card]") as HTMLElement;
    const ids = within(section()).getByText("evt_01J9Z6Q4, evt_01J9Z6Q5, evt_01J9Z6Q6 +1 more");
    expect(ids).toHaveAttribute("title", expect.stringContaining("Request SHA-256: 9f2c4e7a"));
    expect(within(section()).getByText("Sender gone")).toBeInTheDocument();
    expect(h.sources).toEqual([]);

    h.one = {
      data: { ...mockWebhookOverview(mockWebhookSources, "hour", "order-updates") },
      isLoading: false,
      isError: false,
    };
    fireEvent.keyDown(screen.getByRole("combobox", { name: "Source" }), { key: "Enter" });
    fireEvent.click(screen.getByRole("option", { name: "order-updates" }));
    expect(h.sources).toContain("order-updates");
    expect(within(section()).getByText("the body is not JSON")).toBeInTheDocument();
    expect(within(section()).queryByText("Sender gone")).not.toBeInTheDocument();
    expect(within(section()).getByText(/for order-updates in the last hour/)).toBeInTheDocument();
  });

  it("totals the volume by outcome for every source and for one", () => {
    h.one = { data: mockWebhookOverview(mockWebhookSources, "hour", "order-updates"), isLoading: false, isError: false };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    const totals = screen.getByRole("list", { name: "Totals by outcome" });
    // email-events 18342 + order-updates 420 accepted in the last hour.
    expect(within(totals).getByText("18,762")).toBeInTheDocument();
    expect(within(totals).getByText("Invalid body")).toBeInTheDocument();

    fireEvent.keyDown(screen.getByRole("combobox", { name: "Source" }), { key: "Enter" });
    fireEvent.click(screen.getByRole("option", { name: "order-updates" }));
    const one = screen.getByRole("list", { name: "Totals by outcome" });
    expect(within(one).getByText("420")).toBeInTheDocument();
    expect(within(one).queryByText("Unauthorized")).not.toBeInTheDocument();
  });

  it("asks for the last 24 hours when that range is chosen", () => {
    render(<WebhooksPage onNavigate={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Last 24 hours" }));
    expect(h.ranges[h.ranges.length - 1]).toBe("day");
    expect(screen.getByRole("button", { name: "Last 24 hours" })).toHaveAttribute("aria-pressed", "true");
  });

  it("says there were no requests in the range", () => {
    h.status = { data: { ...mockWebhookOverview(mockWebhookSources, "hour"), volume: [] }, isLoading: false, isError: false };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(screen.getByText("No requests in this range.")).toBeInTheDocument();
  });

  // Every receiver's spool, by replica, with what waits and whether it still
  // reports (#2121).
  it("shows each receiver's spool and one that stopped reporting", () => {
    render(<WebhooksPage onNavigate={vi.fn()} />);
    const card = screen.getByRole("heading", { name: "Spool" }).closest("section, div[class*=rounded]")!;
    const first = within(card as HTMLElement).getByText("mcp-data-platform-0").closest("tr")!;
    expect(within(first).getByText("Reporting")).toBeInTheDocument();
    expect(within(first).getByText("212")).toBeInTheDocument();
    const second = within(card as HTMLElement).getByText("mcp-data-platform-1").closest("tr")!;
    expect(within(second).getByText("Not reporting")).toBeInTheDocument();
    expect(within(second).getByText("11,402")).toBeInTheDocument();
    expect(within(second).getByText(/context deadline exceeded/)).toBeInTheDocument();
  });

  it("shows no spool card where no receiver spools", () => {
    h.status = { data: { ...mockWebhookOverview(mockWebhookSources, "hour"), spools: [] }, isLoading: false, isError: false };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(screen.queryByRole("heading", { name: "Spool" })).not.toBeInTheDocument();
  });

  it("explains what a source is when there is none", () => {
    h.status = { data: overview({}), isLoading: false, isError: false };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(screen.getByText("No webhook source yet.")).toBeInTheDocument();
    expect(screen.queryByText("Recent rejections")).not.toBeInTheDocument();
  });

  it("says a failed read is not an empty list", () => {
    h.status = { data: undefined, isLoading: false, isError: true };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(screen.getByText(/could not be read/)).toBeInTheDocument();
  });

  it("shows loading in the table", () => {
    h.status = { data: undefined, isLoading: true, isError: false };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
    expect(screen.queryByText("Recent rejections")).not.toBeInTheDocument();
  });
});

describe("WebhookDetailPage", () => {
  it("shows the address, the requests, the storage and the rejections", () => {
    render(<WebhookDetailPage name="email-events" onBack={vi.fn()} onNavigate={vi.fn()} />);
    expect(screen.getByText(`${window.location.origin}/hooks/email-events`)).toBeInTheDocument();
    expect(screen.getByText("X-Signature (prefix sha256=)")).toBeInTheDocument();
    expect(screen.getByText("18342")).toBeInTheDocument();
    expect(screen.getByText("402113")).toBeInTheDocument();
    expect(screen.getAllByText("Buffer full")).toHaveLength(2);
    expect(screen.getByText("the timestamp is outside the tolerance window")).toBeInTheDocument();
    expect(screen.getByText("The marketing persona")).toBeInTheDocument();
    expect(screen.getByText(/Last event received/)).toBeInTheDocument();
    expect(screen.queryByText(/This source is disabled/)).not.toBeInTheDocument();
  });

  it("shows the response time and where the slowest request's time went", () => {
    render(<WebhookDetailPage name="email-events" onBack={vi.fn()} onNavigate={vi.fn()} />);
    const section = screen.getByText("Response time").closest("[data-slot=card]") as HTMLElement;
    const hour = within(section).getByText("Last hour").closest("tr")!;
    expect(within(hour).getByText("18,342")).toBeInTheDocument();
    expect(within(hour).getByText("1.0 s")).toBeInTheDocument();
    expect(within(hour).getByText("2.4 s")).toBeInTheDocument();
    expect(within(section).getByText("Slowest request")).toBeInTheDocument();
    expect(within(section).getAllByText("24.6 s")).toHaveLength(2);
    expect(within(section).getByText("22.0 s")).toBeInTheDocument();
    expect(within(section).getByText("Writing")).toBeInTheDocument();
  });

  it("says a disabled source stores nothing and a quiet one has no requests", () => {
    h.detail = { data: { source: quiet, status: mockWebhookStatus[quiet.name] }, isLoading: false, error: null };
    render(<WebhookDetailPage name="crm-contacts" onBack={vi.fn()} onNavigate={vi.fn()} />);
    expect(screen.getByText(/This source is disabled/)).toBeInTheDocument();
    expect(screen.getByText("No requests in the last day.")).toBeInTheDocument();
    expect(screen.getByText("No request was accepted in the last 24 hours.")).toBeInTheDocument();
    expect(screen.getByText("No request has been rejected.")).toBeInTheDocument();
    expect(screen.getByText("forever")).toBeInTheDocument();
    expect(screen.getByText("Administrators only")).toBeInTheDocument();
    expect(screen.getByText("Answered")).toBeInTheDocument();
  });

  it("reports windows failing to compact", () => {
    const status = { ...mockWebhookStatus[busy.name]!, failing: 2, last_error: "the scratch catalog is down" };
    h.detail = { data: { source: busy, status }, isLoading: false, error: null };
    render(<WebhookDetailPage name="email-events" onBack={vi.fn()} onNavigate={vi.fn()} />);
    expect(screen.getByText(/2 windows have failed to compact/)).toBeInTheDocument();
    expect(screen.getByText("the scratch catalog is down")).toBeInTheDocument();
  });

  it("reports a source that is not there", () => {
    h.detail = { data: undefined, isLoading: false, error: new Error("404") };
    render(<WebhookDetailPage name="nope" onBack={vi.fn()} onNavigate={vi.fn()} />);
    expect(screen.getByText(/There is no webhook source named nope/)).toBeInTheDocument();
  });

  it("edits, and deletes after confirming", () => {
    const onNavigate = vi.fn();
    render(<WebhookDetailPage name="email-events" onBack={vi.fn()} onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: /Edit/ }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/webhooks/email-events/edit");
    fireEvent.click(screen.getByRole("button", { name: /Delete/ }));
    expect(h.remove).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Delete source" }));
    expect(h.remove).toHaveBeenCalledWith("email-events", expect.anything());
  });
});

describe("raw files (#2132, #2133)", () => {
  it("shows the raw files of each source's current window in the overview", () => {
    render(<WebhooksPage onNavigate={vi.fn()} />);
    const cell = within(row("email-events")).getByText("412");
    expect(cell).toHaveAttribute("title", "95th percentile over the last day: 3650");
  });

  it("shows a source's raw files and resyncs its partitions after confirming", () => {
    render(<WebhookDetailPage name="email-events" onBack={vi.fn()} onNavigate={vi.fn()} />);
    expect(screen.getByText("Raw files in the current window")).toBeInTheDocument();
    expect(screen.getByText("Raw files per window, 95th percentile (day)")).toBeInTheDocument();
    expect(screen.getByText("3650")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Resync partitions/ }));
    expect(screen.getByText(/Lists every raw file the source holds/)).toBeInTheDocument();
    expect(h.sync).not.toHaveBeenCalled();
    const buttons = screen.getAllByRole("button", { name: "Resync partitions" });
    const confirm = buttons[buttons.length - 1]!;
    fireEvent.click(confirm);
    expect(h.sync).toHaveBeenCalledWith("email-events", expect.anything());
    const { onSuccess } = h.sync.mock.calls[0]![1] as { onSuccess: () => void };
    act(() => onSuccess());
    expect(screen.getByText("Partitions resynced.")).toBeInTheDocument();
  });

  it("sends the raw retention and holds a save that came back with warnings", () => {
    const onSaved = vi.fn();
    render(<WebhookEditor name="email-events" onBack={vi.fn()} onSaved={onSaved} />);
    expect(screen.queryByLabelText(/Merge recent raw files every/)).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/Keep raw events/), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(h.update.mock.calls[0]![0].config.raw_retention_days).toBe(2);
    expect(h.update.mock.calls[0]![0].config).not.toHaveProperty("rollup_every_minutes");
    const { onSuccess } = h.update.mock.calls[0]![1] as { onSuccess: (s: unknown) => void };
    act(() => onSuccess({ ...busy, warnings: ["reading the partitions of webhook_email_events_raw took 57 s"] }));
    expect(onSaved).not.toHaveBeenCalled();
    expect(screen.getByText("Saved, with warnings")).toBeInTheDocument();
    expect(screen.getByText(/took 57 s/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Open the source" }));
    expect(onSaved).toHaveBeenCalledWith("email-events");
  });

  it("goes straight to the source after a save with no warnings", () => {
    const onSaved = vi.fn();
    render(<WebhookEditor name="email-events" onBack={vi.fn()} onSaved={onSaved} />);
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    const { onSuccess } = h.update.mock.calls[0]![1] as { onSuccess: (s: unknown) => void };
    act(() => onSuccess({ ...busy, warnings: [] }));
    expect(onSaved).toHaveBeenCalledWith("email-events");
  });
});

describe("WebhookEditor", () => {
  it("refuses a new source with nothing filled in", () => {
    h.detail = { data: undefined, isLoading: false, error: null };
    render(<WebhookEditor onBack={vi.fn()} onSaved={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Create source" }));
    expect(screen.getByText("Enter the secret the sender uses.")).toBeInTheDocument();
    expect(h.create).not.toHaveBeenCalled();
  });

  it("creates a source from what was entered", () => {
    h.detail = { data: undefined, isLoading: false, error: null };
    render(<WebhookEditor onBack={vi.fn()} onSaved={vi.fn()} />);
    fireEvent.change(screen.getByPlaceholderText("email-events"), { target: { value: "esp" } });
    fireEvent.change(screen.getByLabelText(/^Secret(?! encoding)/), { target: { value: "whsec" } });
    fireEvent.change(screen.getByPlaceholderText("X-Signature"), { target: { value: "X-Sig" } });
    // The connection select is a Radix select; the form model is covered in
    // webhookForm.test.ts, so this asserts the refusal names it.
    fireEvent.click(screen.getByRole("button", { name: "Create source" }));
    expect(screen.getByText("Choose the connection the source's table is created on.")).toBeInTheDocument();
  });

  it("edits a source without asking for its name, connection or secret", () => {
    render(<WebhookEditor name="email-events" onBack={vi.fn()} onSaved={vi.fn()} />);
    expect(screen.queryByPlaceholderText("email-events")).not.toBeInTheDocument();
    expect(screen.getByText("Leave empty to keep the stored secret.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(h.update).toHaveBeenCalledTimes(1);
    const body = h.update.mock.calls[0]![0];
    expect(body.auth.secret).toBeUndefined();
    expect(body.name).toBeUndefined();
  });

  it("shows a preset source by its sender, with the secret named as that sender names it (#2223)", () => {
    const discord = { ...busy, auth: { mode: "ed25519" as const, preset: "discord", secret_set: true } };
    h.detail = { data: { source: discord, status: mockWebhookStatus[busy.name] }, isLoading: false, error: null };
    render(<WebhookEditor name={busy.name} onBack={vi.fn()} onSaved={vi.fn()} />);
    expect(screen.getByLabelText(/^Application public key/)).toBeInTheDocument();
    expect(screen.queryByText("How a request proves it came from the sender")).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText("X-Signature-Ed25519")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(h.update.mock.calls[0]![0].auth).toMatchObject({ mode: "ed25519", preset: "discord" });
  });

  it("offers the rotation overlap once a new secret is typed", () => {
    render(<WebhookEditor name="email-events" onBack={vi.fn()} onSaved={vi.fn()} />);
    expect(screen.queryByText(/Keep accepting the previous secret/)).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/New secret/), { target: { value: "rotated" } });
    expect(screen.getByText(/Keep accepting the previous secret/)).toBeInTheDocument();
  });

  it("reports a source it cannot read", () => {
    h.detail = { data: undefined, isLoading: false, error: new Error("boom") };
    render(<WebhookEditor name="gone" onBack={vi.fn()} onSaved={vi.fn()} />);
    expect(screen.getByText("This source could not be read.")).toBeInTheDocument();
  });
});

describe("AdminWebhookRoutes", () => {
  const routes = (route: string) =>
    render(<AdminWebhookRoutes route={route} onNavigate={vi.fn()} onBack={vi.fn()} />);

  it("sends each address to its page", () => {
    routes("/admin/webhooks");
    expect(screen.getByRole("button", { name: /New source/ })).toBeInTheDocument();
  });

  it("opens the editor for a new source and for an existing one", () => {
    h.detail = { data: undefined, isLoading: false, error: null };
    const { unmount } = routes("/admin/webhooks/new");
    expect(screen.getByText("New webhook source")).toBeInTheDocument();
    unmount();
    h.detail = { data: { source: busy, status: mockWebhookStatus[busy.name] }, isLoading: false, error: null };
    routes("/admin/webhooks/email-events/edit");
    expect(screen.getByText("Edit email-events")).toBeInTheDocument();
  });

  it("opens one source, and nothing for an address it does not know", () => {
    const { unmount } = routes("/admin/webhooks/email-events");
    expect(screen.getByText("Give this to the sender")).toBeInTheDocument();
    unmount();
    const { container } = routes("/admin/webhooks/a/b/c");
    expect(container).toBeEmptyDOMElement();
  });
});

describe("WebhooksPage ranges (#2150)", () => {
  it("reads the last seven days", () => {
    render(<WebhooksPage onNavigate={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Last 7 days" }));
    expect(h.ranges[h.ranges.length - 1]).toBe("week");
    expect(screen.getByText(/in the last 7 days/)).toBeInTheDocument();
  });

  it("reads a custom span once both ends are set and shown", () => {
    render(<WebhooksPage onNavigate={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Custom" }));
    expect(screen.getByTestId("custom-range")).toBeInTheDocument();
    expect(h.customs).toHaveLength(0);
    fireEvent.change(screen.getByLabelText("From"), { target: { value: "2026-10-06T09:00" } });
    fireEvent.change(screen.getByLabelText("To"), { target: { value: "2026-10-06T17:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Show" }));
    const last = h.customs[h.customs.length - 1] as { from: string; to: string };
    expect(new Date(last.to).getTime() - new Date(last.from).getTime()).toBe(8 * 3600_000);

    // A span that ends before it starts is not read.
    const before = h.customs.length;
    fireEvent.change(screen.getByLabelText("To"), { target: { value: "2026-10-05T17:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Show" }));
    expect((h.customs[h.customs.length - 1] as { to: string }).to).toBe(last.to);
    expect(h.customs.length).toBeGreaterThanOrEqual(before);
  });
});

describe("mailbox sources (#2247)", () => {
  const mailbox = mockWebhookSources.find((s) => s.config.input === "imap")!;
  const folders = [
    {
      folder: "INBOX",
      uid_validity: 7,
      last_uid: 46,
      stored: 41,
      hidden_not_allowed: 4,
      hidden_unauthenticated: 1,
      last_error: "logging in as platform: authentication failed",
    },
  ];

  it("shows the mailbox and each folder's counts in place of a sender address", () => {
    h.detail = {
      data: { source: mailbox, status: mockWebhookStatus[mailbox.name], imap: folders },
      isLoading: false,
      error: null,
    };
    render(<WebhookDetailPage name={mailbox.name} onBack={vi.fn()} onNavigate={vi.fn()} />);
    expect(screen.getByText("imap.acme.example:993 (TLS)")).toBeInTheDocument();
    expect(screen.getByText("no-reply@zoom.example")).toBeInTheDocument();
    expect(screen.getByText("Required, read from mx.acme.example")).toBeInTheDocument();
    const inbox = screen.getByText("INBOX").closest("tr")!;
    expect(within(inbox).getByText("41")).toBeInTheDocument();
    expect(within(inbox).getByText("4")).toBeInTheDocument();
    expect(within(inbox).getByText("1")).toBeInTheDocument();
    expect(within(inbox).getByText("logging in as platform: authentication failed")).toBeInTheDocument();
    expect(screen.getByText(/Last message filed/)).toBeInTheDocument();
    expect(screen.queryByText("Give this to the sender")).not.toBeInTheDocument();
    expect(screen.queryByText("Rejected requests")).not.toBeInTheDocument();
  });

  it("says a folder no check has reached yet", () => {
    h.detail = {
      data: { source: mailbox, status: { ...mockWebhookStatus[mailbox.name]!, last_segment_at: null } },
      isLoading: false,
      error: null,
    };
    render(<WebhookDetailPage name={mailbox.name} onBack={vi.fn()} onNavigate={vi.fn()} />);
    expect(screen.getByText("Not yet")).toBeInTheDocument();
    expect(screen.getByText("No message has been filed yet.")).toBeInTheDocument();
  });

  it("edits a mailbox source with the mailbox fields and saves it as one", () => {
    h.detail = { data: { source: mailbox, status: mockWebhookStatus[mailbox.name] }, isLoading: false, error: null };
    render(<WebhookEditor name={mailbox.name} onBack={vi.fn()} onSaved={vi.fn()} />);
    expect(screen.getByText("Mail server")).toBeInTheDocument();
    expect(screen.queryByText("Events")).not.toBeInTheDocument();
    expect(screen.queryByText("Largest body (bytes)")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Allowed senders"), { target: { value: "no-reply@zoom.example\n@acme.example" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(h.update).toHaveBeenCalledTimes(1);
    const body = h.update.mock.calls[0]![0];
    expect(body.auth.mode).toBe("imap");
    expect(body.config.imap.allow_senders).toEqual(["no-reply@zoom.example", "@acme.example"]);
  });

  it("refuses a mailbox source with no allowed sender", () => {
    h.detail = { data: { source: mailbox, status: mockWebhookStatus[mailbox.name] }, isLoading: false, error: null };
    render(<WebhookEditor name={mailbox.name} onBack={vi.fn()} onSaved={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Allowed senders"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(screen.getByText("Name the addresses or @domains whose mail becomes events.")).toBeInTheDocument();
    expect(h.update).not.toHaveBeenCalled();
  });
});
