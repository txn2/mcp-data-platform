import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { HandlerDetail } from "@/api/portal/hooks/scriptHandlers";

vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptEventSummary: vi.fn(),
}));

vi.mock("@/api/portal/hooks/scriptHandlers", () => ({
  useSaveHandler: vi.fn(),
  useSetHandlerEnabled: vi.fn(),
}));

import { useScriptEventSummary } from "@/api/portal/hooks/scripts";
import { useSaveHandler, useSetHandlerEnabled } from "@/api/portal/hooks/scriptHandlers";
import { AddWebhook, WebhookHandler } from "./WebhookHandler";

const save = vi.fn();
const toggle = vi.fn();

const handler: HandlerDetail = {
  handler: "wh-001",
  kind: "webhook",
  type: "webhook",
  name: "on-sale",
  event_source: "form-contact",
  enabled: true,
};

function summary(skipped = 0) {
  vi.mocked(useScriptEventSummary).mockReturnValue({
    data: {
      pending: 2,
      skipped_since: skipped,
      skipped_window_days: 7,
      max_queued: 100,
      last_event_run: "2026-10-26T09:30:00Z",
      last_skipped_at: "2026-10-25T18:02:00Z",
    },
  } as never);
}

function mutation(mutate: ReturnType<typeof vi.fn>, error?: string) {
  return { mutate, isPending: false, error: error ? new Error(error) : null } as never;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useSaveHandler).mockReturnValue(mutation(save));
  vi.mocked(useSetHandlerEnabled).mockReturnValue(mutation(toggle));
  summary();
});

afterEach(cleanup);

// #2332, #2434: one of a script's webhook handlers, and how its event runs
// stand.
describe("WebhookHandler", () => {
  it("says what each event of its source does, and turns the handler it shows off", () => {
    render(<WebhookHandler scriptId="s1" handler={handler} />);
    expect(screen.getByText("form-contact")).toBeInTheDocument();
    expect(screen.getByText(/runs this script once, with the event as run.event/)).toBeInTheDocument();
    expect(useSetHandlerEnabled).toHaveBeenCalledWith("wh-001");
    fireEvent.click(screen.getByRole("button", { name: "Turn off" }));
    expect(toggle).toHaveBeenCalledWith(false);
  });

  it("turns a handler that is off back on", () => {
    render(<WebhookHandler scriptId="s1" handler={{ ...handler, enabled: false }} />);
    fireEvent.click(screen.getByRole("button", { name: "Turn on" }));
    expect(toggle).toHaveBeenCalledWith(true);
  });

  it("shows the waiting and skipped event runs", () => {
    summary(3);
    render(<WebhookHandler scriptId="s1" handler={handler} />);
    expect(useScriptEventSummary).toHaveBeenCalledWith("s1", true);
    expect(screen.getByText("2 event runs, at most 100 for each source")).toBeInTheDocument();
    expect(screen.getByText(/3 in the last 7 days/)).toBeInTheDocument();
    expect(screen.getByText(/each is a run marked Skipped \(backlog\)/)).toBeInTheDocument();
  });

  it("says when nothing was skipped, and shows a refusal", () => {
    vi.mocked(useSetHandlerEnabled).mockReturnValue(mutation(toggle, 'only an administrator may run a script for source "shop"'));
    render(<WebhookHandler scriptId="s1" handler={handler} />);
    expect(screen.getByText("None in the last 7 days")).toBeInTheDocument();
    expect(screen.getByText(/only an administrator/)).toBeInTheDocument();
  });

  it("says a script no event has run yet", () => {
    vi.mocked(useScriptEventSummary).mockReturnValue({
      data: { pending: 0, skipped_since: 0, skipped_window_days: 7, max_queued: 100 },
    } as never);
    render(<WebhookHandler scriptId="s1" handler={handler} />);
    expect(screen.getByText("None yet")).toBeInTheDocument();
  });
});

// Adding a webhook handler: the source is picked from the ones the caller may
// name, never typed.
describe("AddWebhook", () => {
  it("offers the sources the script does not already run for, and adds one", () => {
    const onDone = vi.fn();
    render(
      <AddWebhook scriptId="s1" sources={["shop-orders", "form-contact", "github-pushes"]} held={["shop-orders"]} onDone={onDone} />,
    );
    const select = screen.getByLabelText("Webhook source") as HTMLSelectElement;
    expect(Array.from(select.options).map((o) => o.value)).toEqual(["form-contact", "github-pushes"]);
    expect(select.value).toBe("form-contact");
    fireEvent.change(select, { target: { value: "github-pushes" } });
    fireEvent.click(screen.getByRole("button", { name: "Add webhook" }));
    expect(useSaveHandler).toHaveBeenCalledWith("s1");
    expect(save).toHaveBeenCalledWith({ kind: "webhook", event_source: "github-pushes" }, { onSuccess: onDone });
  });

  it("cancels without adding", () => {
    const onDone = vi.fn();
    render(<AddWebhook scriptId="s1" sources={["form-contact"]} held={[]} onDone={onDone} />);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onDone).toHaveBeenCalled();
    expect(save).not.toHaveBeenCalled();
  });

  it("says who may name a source when the caller may name none", () => {
    render(<AddWebhook scriptId="s1" sources={[]} held={[]} onDone={vi.fn()} />);
    expect(screen.getByText(/There is no webhook source you may run this script for/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Webhook source")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add webhook" })).toBeDisabled();
  });

  it("says the script already runs for every source the caller may name", () => {
    render(<AddWebhook scriptId="s1" sources={["form-contact"]} held={["form-contact"]} onDone={vi.fn()} />);
    expect(screen.getByText("This script already runs for every source you may name.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add webhook" })).toBeDisabled();
  });

  it("shows the server's refusal", () => {
    vi.mocked(useSaveHandler).mockReturnValue(mutation(save, 'only an administrator may run a script for source "shop"'));
    render(<AddWebhook scriptId="s1" sources={["shop"]} held={[]} onDone={vi.fn()} />);
    expect(screen.getByText(/only an administrator may run a script/)).toBeInTheDocument();
  });
});
