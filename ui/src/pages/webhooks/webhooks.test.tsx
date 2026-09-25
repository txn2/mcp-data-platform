import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { mockWebhookSources, mockWebhookStatus } from "@/mocks/data/webhooks";

const h = vi.hoisted(() => ({
  list: { data: undefined as unknown, isLoading: false, isError: false },
  detail: { data: undefined as unknown, isLoading: false, error: null as unknown },
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useWebhookSources: () => h.list,
    useWebhookSource: () => h.detail,
    useCreateWebhookSource: () => ({ mutate: h.create, isPending: false, error: null }),
    useUpdateWebhookSource: () => ({ mutate: h.update, isPending: false, error: null }),
    useDeleteWebhookSource: () => ({ mutate: h.remove, isPending: false, error: null }),
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
  h.list = { data: { sources: mockWebhookSources }, isLoading: false, isError: false };
  h.detail = { data: { source: busy, status: mockWebhookStatus[busy.name] }, isLoading: false, error: null };
  h.create.mockReset();
  h.update.mockReset();
  h.remove.mockReset();
});

describe("WebhooksPage", () => {
  it("lists every source and opens one on row click", () => {
    const onNavigate = vi.fn();
    render(<WebhooksPage onNavigate={onNavigate} />);
    expect(screen.getByText("webhook_email_events")).toBeInTheDocument();
    expect(screen.getByText("Disabled")).toBeInTheDocument();
    expect(screen.getByText("Token in a header")).toBeInTheDocument();
    fireEvent.click(screen.getByText("crm-contacts"));
    expect(onNavigate).toHaveBeenCalledWith("/admin/webhooks/crm-contacts");
    fireEvent.click(screen.getByRole("button", { name: /New source/ }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/webhooks/new");
  });

  it("explains what a source is when there is none", () => {
    h.list = { data: { sources: [] }, isLoading: false, isError: false };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(screen.getByText("No webhook source yet.")).toBeInTheDocument();
  });

  it("says a failed read is not an empty list", () => {
    h.list = { data: undefined, isLoading: false, isError: true };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(screen.getByText(/could not be read/)).toBeInTheDocument();
  });

  it("shows loading in the table", () => {
    h.list = { data: undefined, isLoading: true, isError: false };
    render(<WebhooksPage onNavigate={vi.fn()} />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
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

  it("says a disabled source stores nothing and a quiet one has no requests", () => {
    h.detail = { data: { source: quiet, status: mockWebhookStatus[quiet.name] }, isLoading: false, error: null };
    render(<WebhookDetailPage name="crm-contacts" onBack={vi.fn()} onNavigate={vi.fn()} />);
    expect(screen.getByText(/This source is disabled/)).toBeInTheDocument();
    expect(screen.getByText("No requests in the last day.")).toBeInTheDocument();
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
    fireEvent.change(screen.getByLabelText(/^Secret/), { target: { value: "whsec" } });
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
