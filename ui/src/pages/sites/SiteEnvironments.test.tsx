import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { getResponse } from "msw";
import { handlers } from "@/mocks/handlers";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { renderWithQuery, signIn, type SentRequest } from "@/test/applicationHarness";
import { SiteDetailPage } from "./SiteDetailPage";
import { envNameProblem } from "./envName";

// The Environments and Access tabs of #2604, #2617 and #2619: dev is the
// working copy, the environments are in the site's stored order with dev first
// and prod last, every address on the pages domain asks for a sign-in and only
// a domain made public answers anyone, sharing names environments, a promotion
// takes the release of the environment before, and publishing lands in dev.

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

    expect(screen.getAllByTestId(/^env-(dev|stage|prod)$/).map((e) => e.dataset.testid)).toEqual(["env-dev", "env-stage", "env-prod"]);
    const dev = await screen.findByTestId("env-dev");
    expect(dev).toHaveTextContent("Working copy");
    expect(dev).toHaveTextContent("Private");
    expect(dev).toHaveTextContent("marketing--dev.pages.example.net");
    expect(screen.getByTestId("fixed-dev")).toHaveTextContent("Always first");
    await waitFor(() => expect(dev).toHaveTextContent("web-team"));
    expect(within(dev).getByRole("button", { name: "Publish" })).toBeInTheDocument();
    expect(within(dev).queryByRole("button", { name: "Move" })).toBeNull();

    const stage = screen.getByTestId("env-stage");
    expect(stage).toHaveTextContent("marketing--stage.pages.example.net");
    expect(stage).toHaveTextContent("Everyone internal");
    expect(stage).toHaveTextContent("pat.rivera@client.example");

    const prod = screen.getByTestId("env-prod");
    expect(prod).toHaveTextContent("Public domain");
    expect(prod).toHaveTextContent("Anyone at example.com, with no sign-in.");
    expect(prod).toHaveTextContent("Everywhere else, after a sign-in");
    expect(prod).toHaveTextContent("marketing.pages.example.net");
    expect(prod).toHaveTextContent("Main address");
    expect(screen.getByTestId("fixed-prod")).toHaveTextContent("Always last");
    expect(within(prod).getByRole("button", { name: "Share" })).toBeInTheDocument();
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
    const dialog = await screen.findByRole("dialog", { name: "Share marketing" });
    expect(within(dialog).getByRole("checkbox", { name: "stage" })).toBeChecked();
    expect(within(dialog).getByRole("checkbox", { name: "prod" })).not.toBeChecked();

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
    const dialog = await screen.findByRole("dialog", { name: "Share marketing" });
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

  it("has no public setting on an environment: it points at the Domains tab", async () => {
    serve();
    signIn("admin@example.com", true);
    open();
    const stage = await screen.findByTestId("env-stage");
    fireEvent.click(within(stage).getByRole("button", { name: "Settings" }));
    const settings = await screen.findByRole("dialog", { name: "stage settings" });
    expect(within(settings).queryByRole("radio", { name: "Public" })).toBeNull();
    expect(settings).toHaveTextContent("add a domain and make that domain public on the Domains tab");
  });

  it("promotes prod from the environment before it, naming its release, with nothing to choose", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open();
    const prod = await screen.findByTestId("env-prod");
    fireEvent.click(within(prod).getByRole("button", { name: "Promote" }));
    const dialog = await screen.findByRole("dialog", { name: "Promote to prod" });
    expect(within(dialog).getByTestId("promote-source")).toHaveTextContent("stage, release 11");
    expect(dialog).toHaveTextContent("prod takes releases only from stage, the environment before it.");
    expect(within(dialog).queryByRole("combobox")).toBeNull();
    fireEvent.click(within(dialog).getByRole("button", { name: "Promote to prod" }));
    await waitFor(() => expect(sent.find((r) => r.path === "/api/v1/portal/sites/marketing/promote")?.body).toEqual({ from: "stage", to: "prod", release: 11, reason: "" }));
  });

  it("moves an environment after another and says which takes from which", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open();
    fireEvent.click(await screen.findByRole("button", { name: "New environment" }));
    let dialog = await screen.findByRole("dialog", { name: "New environment" });
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "qa" } });
    expect(within(dialog).getByRole("radio", { name: "After dev" })).toBeChecked();
    fireEvent.click(within(dialog).getByRole("radio", { name: "After stage" }));
    expect(within(dialog).queryByRole("radio", { name: "After prod" })).toBeNull();
    fireEvent.click(within(dialog).getByRole("button", { name: "Create environment" }));
    await waitFor(() => expect(sent.find((r) => r.method === "PUT" && r.path === "/api/v1/portal/sites/marketing/environments/qa")?.body).toEqual({ requires_approval: false, after: "stage" }));

    const qa = await screen.findByTestId("env-qa");
    expect(screen.getAllByTestId(/^env-(dev|stage|qa|prod)$/).map((e) => e.dataset.testid)).toEqual(["env-dev", "env-stage", "env-qa", "env-prod"]);
    fireEvent.click(within(qa).getByRole("button", { name: "Move" }));
    dialog = await screen.findByRole("dialog", { name: "Move qa" });
    expect(within(dialog).getByRole("radio", { name: "After stage" }).closest("label")).toHaveTextContent("Now");
    fireEvent.click(within(dialog).getByRole("radio", { name: "After dev" }));
    expect(within(dialog).getByTestId("move-after")).toHaveTextContent("qa takes releases from dev, stage takes releases from qa, prod takes releases from stage.");
    fireEvent.click(within(dialog).getByRole("button", { name: "Move qa" }));
    await waitFor(() => expect(sent.filter((r) => r.path === "/api/v1/portal/sites/marketing/environments/qa").slice(-1)[0]?.body).toEqual({ after: "dev" }));
  });

  it("moves the main address and says what each address serves afterwards", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open();
    fireEvent.click(await screen.findByRole("button", { name: "Main address" }));
    const dialog = await screen.findByRole("dialog", { name: "Main address" });
    fireEvent.click(within(dialog).getByRole("radio", { name: "stage" }));
    expect(dialog).toHaveTextContent("marketing.pages.example.net serves stage.");
    expect(dialog).toHaveTextContent("marketing--prod.pages.example.net serves prod.");
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent.find((r) => r.path === "/api/v1/portal/sites/marketing/main-environment")?.body).toEqual({ environment: "stage" }));
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

  it("creates an environment after the one chosen, with no public setting", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open();
    fireEvent.click(await screen.findByRole("button", { name: "New environment" }));
    const dialog = await screen.findByRole("dialog", { name: "New environment" });
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "review" } });
    expect(dialog).toHaveTextContent("marketing--review.pages.example.net");
    expect(within(dialog).queryByRole("radio", { name: "Public" })).toBeNull();
    fireEvent.click(within(dialog).getByRole("button", { name: "Create environment" }));
    await waitFor(() =>
      expect(sent.find((r) => r.method === "PUT" && r.path === "/api/v1/portal/sites/marketing/environments/review")?.body).toEqual({ requires_approval: false, after: "dev" }),
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
      created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-01T00:00:00Z", address: "", addresses: {}, pages_addresses: {}, domains: [], permissions: { manage: false, own: false, delete: false },
    };
    const served = vi.mocked(fetch);
    const real = served.getMockImplementation()!;
    served.mockImplementation(async (input, init) => (String(input).endsWith("/sites/x-site") ? new Response(JSON.stringify(empty), { status: 200 }) : real(input, init)));
    signIn("viewer@example.com", false);
    renderWithQuery(<SiteDetailPage site="x-site" tab="environments" onNavigate={vi.fn()} />);
    expect(await screen.findByText("No environment to show")).toBeInTheDocument();
  });
});

// #2607: a card lists the address people use first, a domain before the pages
// address, and the rest muted after it. #2611: a two-character site's
// environments other than the main one have no pages address, and the page
// says why in the server's words.
describe("Environment addresses (#2607, #2611)", () => {
  const links = (card: HTMLElement) => within(card).getAllByRole("link").map((a) => [a.textContent, a.className.includes("text-muted-foreground") ? "muted" : "primary"]);

  it("lists a public domain first, then the private domains and the pages address, muted, each with its access", async () => {
    serve();
    signIn("admin@example.com", true);
    open();
    const prod = await screen.findByTestId("env-prod");
    expect(links(prod)).toEqual([
      ["example.com", "primary"],
      ["www.example.com", "muted"],
      ["marketing.pages.example.net", "muted"],
    ]);
    expect(prod).toHaveTextContent(/example\.comDomainPublic.*www\.example\.comDomainPrivate.*marketing\.pages\.example\.netMain addressPrivate/);
    expect(links(screen.getByTestId("env-dev"))).toEqual([["marketing--dev.pages.example.net", "primary"]]);
    expect(screen.queryByTestId("address-note")).toBeNull();
  });

  it("says why a two-character site's environments have no pages address", async () => {
    serve();
    signIn("admin@example.com", true);
    renderWithQuery(<SiteDetailPage site="ab" tab="environments" onNavigate={vi.fn()} />);
    const note = await screen.findByTestId("address-note");
    expect(note).toHaveTextContent("The environments of ab have no pages address");
    expect(note).toHaveTextContent(
      'A two-character site name puts "--" in the 3rd and 4th characters of an address like ab--prod.pages.example.net. DNS reserves that form, so certificate authorities refuse it.',
    );
    expect(links(screen.getByTestId("env-dev"))).toEqual([["ab.pages.example.net", "primary"]]);
    expect(screen.getByTestId("env-staging")).toHaveTextContent("No address");
    expect(links(screen.getByTestId("env-prod"))).toEqual([["ab.example.com", "primary"]]);
    expect(screen.queryByText(/ab--/, { selector: "a" })).toBeNull();
    expect(screen.queryByText("Every environment also answers at", { exact: false })).toBeNull();
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
    expect(screen.getByText(/example\.com is a public domain and opens for anyone/)).toBeInTheDocument();
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

describe("Site domains (#2619)", () => {
  it("shows each domain's access and opens one only after the owner confirms it", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open("domains");
    const row = (await screen.findByText("www.example.com")).closest("tr")!;
    expect(row).toHaveTextContent("Private");
    fireEvent.click(within(row).getByRole("button", { name: "Make public" }));
    const confirm = await screen.findByRole("dialog", { name: "Make www.example.com public?" });
    const go = within(confirm).getByRole("button", { name: "Make public" });
    expect(go).toBeDisabled();
    fireEvent.change(within(confirm).getByLabelText("Type www.example.com to confirm"), { target: { value: "www.example.com" } });
    fireEvent.click(go);
    await waitFor(() => expect(sent.find((r) => r.method === "PUT" && r.path === "/api/v1/portal/sites/marketing/domains/www.example.com")?.body).toEqual({ public: true }));

    const open1 = (await screen.findByText("example.com", { selector: "a" })).closest("tr")!;
    fireEvent.click(within(open1).getByRole("button", { name: "Make private" }));
    const close = await screen.findByRole("dialog", { name: "Make example.com private?" });
    fireEvent.click(within(close).getByRole("button", { name: "Make private" }));
    await waitFor(() => expect(sent.find((r) => r.method === "PUT" && r.path === "/api/v1/portal/sites/marketing/domains/example.com")?.body).toEqual({ public: false }));
  });

  it("adds a domain private by default, and public only after the owner confirms", async () => {
    const sent = serve();
    signIn("admin@example.com", true);
    open("domains");
    fireEvent.click(await screen.findByRole("button", { name: "Add domain" }));
    const dialog = await screen.findByRole("dialog", { name: "Add domain" });
    fireEvent.change(within(dialog).getByLabelText("Domain"), { target: { value: "shop.example.com" } });
    expect(within(dialog).getByRole("radio", { name: "Private" })).toBeChecked();
    fireEvent.click(within(dialog).getByRole("radio", { name: "Public" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Add domain" }));
    const confirm = await screen.findByRole("dialog", { name: "Make shop.example.com public?" });
    fireEvent.change(within(confirm).getByLabelText("Type shop.example.com to confirm"), { target: { value: "shop.example.com" } });
    fireEvent.click(within(confirm).getByRole("button", { name: "Add public domain" }));
    await waitFor(() =>
      expect(sent.find((r) => r.method === "POST" && r.path === "/api/v1/portal/sites/marketing/domains")?.body).toEqual({ hostname: "shop.example.com", environment: "prod", public: true }),
    );
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
