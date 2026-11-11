import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { getResponse } from "msw";
import { handlers } from "@/mocks/handlers";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { renderWithQuery, signIn, type SentRequest } from "@/test/applicationHarness";
import { SiteDetailPage } from "./SiteDetailPage";
import { envNameProblem } from "./envName";

// The Environments and Access tabs of #2604: dev is the working copy, every
// environment says who can open it, sharing names environments, making one
// public names what opens, and publishing lands in dev only.

afterEach(() => {
  vi.unstubAllGlobals();
});

beforeEach(() => {
  resetSitesMock();
});

function serve(): SentRequest[] {
  const sent: SentRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, window.location.origin);
      const body = typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
      sent.push({ method: init?.method ?? "GET", path: url.pathname + url.search, body });
      const res = await getResponse(handlers, new Request(url, init));
      return res ?? new Response(JSON.stringify({ detail: "no handler" }), { status: 404 });
    }),
  );
  return sent;
}

function open(tab: "environments" | "access" | "domains" = "environments") {
  renderWithQuery(<SiteDetailPage site="marketing" tab={tab} onNavigate={vi.fn()} />);
}

describe("Site environments (#2604)", () => {
  it("explains how the site works and shows each environment's access and who can open it", async () => {
    serve();
    signIn("admin@example.com", true);
    open();

    expect(await screen.findByText("How this site works")).toBeInTheDocument();
    expect(screen.getByText("Changes land in dev")).toBeInTheDocument();

    const dev = await screen.findByTestId("env-dev");
    expect(dev).toHaveTextContent("Working copy");
    expect(dev).toHaveTextContent("Private");
    expect(dev).toHaveTextContent("marketing.pages.example.net");
    expect(dev).toHaveTextContent("Main address");
    await waitFor(() => expect(dev).toHaveTextContent("web-team"));
    expect(within(dev).getByRole("button", { name: "Publish" })).toBeInTheDocument();

    const stage = screen.getByTestId("env-stage");
    expect(stage).toHaveTextContent("marketing--stage.pages.example.net");
    expect(stage).toHaveTextContent("Everyone internal");
    expect(stage).toHaveTextContent("pat.rivera@client.example");

    const prod = screen.getByTestId("env-prod");
    expect(prod).toHaveTextContent("Public");
    expect(prod).toHaveTextContent("Anyone. The platform asks no one to sign in.");
    expect(within(prod).queryByRole("button", { name: "Share" })).toBeNull();
    expect(within(prod).getByRole("button", { name: "Promote" })).toBeInTheDocument();
  });

  it("hides the explainer and offers it back", async () => {
    serve();
    signIn("admin@example.com", true);
    open();
    fireEvent.click(await screen.findByRole("button", { name: "Hide" }));
    expect(screen.queryByText("Changes land in dev")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "How this site works" }));
    expect(screen.getByText("Changes land in dev")).toBeInTheDocument();
  });

  it("shares a private environment with a person, naming that environment", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open();
    const stage = await screen.findByTestId("env-stage");
    fireEvent.click(within(stage).getByRole("button", { name: "Share" }));
    const dialog = await screen.findByRole("dialog", { name: "Share stage" });
    expect(within(dialog).getByRole("checkbox", { name: "stage" })).toBeChecked();
    expect(within(dialog).queryByRole("checkbox", { name: "prod" })).toBeNull();

    const box = within(dialog).getByRole("combobox", { name: "Name, email or persona" });
    fireEvent.change(box, { target: { value: "client@client.example" } });
    await waitFor(() => expect(within(dialog).getByRole("option", { name: /client@client\.example/ })).toBeInTheDocument());
    fireEvent.keyDown(box, { key: "Enter" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Share" }));

    await waitFor(() =>
      expect(sent.find((r) => r.method === "PUT" && r.path === "/api/v1/portal/sites/marketing/access")?.body).toEqual({
        email: "client@client.example",
        level: "view",
        environments: ["stage"],
        notify: true,
      }),
    );
  });

  it("shares with any persona on the deployment, from the site's access list", async () => {
    const sent = serve();
    signIn("owner@example.com", false);
    open();
    const stage = await screen.findByTestId("env-stage");
    fireEvent.click(within(stage).getByRole("button", { name: "Share" }));
    const dialog = await screen.findByRole("dialog", { name: "Share stage" });
    const box = within(dialog).getByRole("combobox", { name: "Name, email or persona" });
    fireEvent.change(box, { target: { value: "management" } });
    await waitFor(() => expect(within(dialog).getByRole("option", { name: /management-team/ })).toBeInTheDocument());
    fireEvent.keyDown(box, { key: "Enter" });
    expect(dialog).toHaveTextContent("Whoever holds this persona when they visit");
    fireEvent.click(within(dialog).getByRole("button", { name: "Share" }));
    await waitFor(() =>
      expect(sent.find((r) => r.method === "PUT" && r.path === "/api/v1/portal/sites/marketing/access")?.body).toEqual({
        persona: "management-team",
        level: "view",
        environments: ["stage"],
      }),
    );
    expect(sent.some((r) => r.path.startsWith("/api/v1/portal/share-personas"))).toBe(false);
  });

  it("confirms what becomes public before making an environment public", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open();
    const stage = await screen.findByTestId("env-stage");
    fireEvent.click(within(stage).getByRole("button", { name: "Settings" }));
    const settings = await screen.findByRole("dialog", { name: "stage settings" });
    fireEvent.click(within(settings).getByRole("radio", { name: "Public" }));

    const confirm = await screen.findByRole("dialog", { name: "Make stage public?" });
    expect(confirm).toHaveTextContent("marketing--stage.pages.example.net");
    expect(confirm).toHaveTextContent("go.example.com");
    const go = within(confirm).getByRole("button", { name: "Make stage public" });
    expect(go).toBeDisabled();
    fireEvent.change(within(confirm).getByLabelText("Type stage to confirm"), { target: { value: "stage" } });
    fireEvent.click(go);
    await waitFor(() => expect(sent.find((r) => r.method === "PUT" && r.path === "/api/v1/portal/sites/marketing/environments/stage")?.body).toEqual({ public: true }));
  });

  it("moves the main address and says what each address serves afterwards", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open();
    fireEvent.click(await screen.findByRole("button", { name: "Main address" }));
    const dialog = await screen.findByRole("dialog", { name: "Main address" });
    fireEvent.click(within(dialog).getByRole("radio", { name: "prod" }));
    expect(dialog).toHaveTextContent("marketing.pages.example.net serves prod (public).");
    expect(dialog).toHaveTextContent("marketing--dev.pages.example.net serves dev (private).");
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent.find((r) => r.path === "/api/v1/portal/sites/marketing/main-environment")?.body).toEqual({ environment: "prod" }));
  });

  it("publishes to dev with no environment to choose", async () => {
    serve();
    signIn("admin@example.com", true);
    open();
    const dev = await screen.findByTestId("env-dev");
    fireEvent.click(within(dev).getByRole("button", { name: "Publish" }));
    const dialog = await screen.findByRole("dialog", { name: "Publish to dev" });
    expect(within(dialog).queryByRole("combobox", { name: "Environment" })).toBeNull();
    expect(dialog).toHaveTextContent("This changes dev only");
  });

  it("creates a private environment and refuses an address that would not fit", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open();
    fireEvent.click(await screen.findByRole("button", { name: "New environment" }));
    const dialog = await screen.findByRole("dialog", { name: "New environment" });
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "review" } });
    expect(dialog).toHaveTextContent("marketing--review.pages.example.net");
    expect(within(dialog).getByRole("radio", { name: "Private" })).toBeChecked();
    fireEvent.click(within(dialog).getByRole("button", { name: "Create environment" }));
    await waitFor(() =>
      expect(sent.find((r) => r.method === "PUT" && r.path === "/api/v1/portal/sites/marketing/environments/review")?.body).toEqual({ public: false, requires_approval: false }),
    );
  });
});

describe("Site environments for someone who views (#2604)", () => {
  it("shows the environments open to them and nothing to change", async () => {
    serve();
    signIn("viewer@example.com", false);
    renderWithQuery(<SiteDetailPage site="docs" tab="environments" onNavigate={vi.fn()} />);
    const prod = await screen.findByTestId("env-prod");
    expect(prod).toHaveTextContent("docs.example.com");
    expect(screen.getByText(/environments of docs shared with you/)).toBeInTheDocument();
    expect(screen.queryByText("How this site works")).toBeNull();
    for (const name of ["Promote", "Settings", "Share", "Publish", "Roll back", "Main address", "New environment", "Add domain"]) {
      expect(screen.queryByRole("button", { name })).toBeNull();
    }
  });

  it("says so when no environment is open to them", async () => {
    serve();
    const empty = {
      id: "site_x", name: "x-site", description: "", owner_email: "o@example.com", main_environment: "", environments: [], created_by: "o@example.com",
      created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-01T00:00:00Z", address: "", addresses: {}, domains: [], permissions: { manage: false, own: false, delete: false },
    };
    const served = vi.mocked(fetch);
    const real = served.getMockImplementation()!;
    served.mockImplementation(async (input, init) => (String(input).endsWith("/sites/x-site") ? new Response(JSON.stringify(empty), { status: 200 }) : real(input, init)));
    signIn("viewer@example.com", false);
    renderWithQuery(<SiteDetailPage site="x-site" tab="environments" onNavigate={vi.fn()} />);
    expect(await screen.findByText("No environment to show")).toBeInTheDocument();
  });
});

describe("Site access (#2604)", () => {
  it("lists who manages and who each environment is shared with, people and personas", async () => {
    serve();
    signIn("admin@example.com", true);
    open("access");
    expect(await screen.findByText("Who manages")).toBeInTheDocument();
    expect(await screen.findByText("david.park@example.com")).toBeInTheDocument();
    const persona = (await screen.findByText("web-team")).closest("tr")!;
    expect(persona).toHaveTextContent("Persona");
    expect(persona).toHaveTextContent("dev");
    const internal = screen.getByText("Everyone internal").closest("tr")!;
    expect(internal).toHaveTextContent("stage");
    expect(screen.getByText(/prod is public, so it opens for anyone/)).toBeInTheDocument();
  });

  it("removes a persona share by its persona", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open("access");
    const row = (await screen.findByText("web-team")).closest("tr")!;
    fireEvent.click(within(row).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(sent.find((r) => r.method === "DELETE")?.path).toBe("/api/v1/portal/sites/marketing/access?persona=web-team"));
  });
});

describe("envNameProblem", () => {
  it("accepts a name whose address fits and refuses one that does not", () => {
    expect(envNameProblem("intranet", "prod")).toBe("");
    expect(envNameProblem("intranet", "Prod")).toMatch(/lowercase/);
    expect(envNameProblem("intranet", "a--b")).toMatch(/single hyphens/);
    const long = "employee-benefits-open-enrollment-2026-information-hub";
    expect(envNameProblem(long, "review-copy")).toMatch(/at most 63/);
  });
});
