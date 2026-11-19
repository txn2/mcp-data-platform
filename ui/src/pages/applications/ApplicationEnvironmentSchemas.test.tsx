import { describe, it, expect, vi, afterEach } from "vitest";
import { screen, fireEvent, within } from "@testing-library/react";
import { renderWithQuery, serveApplications, signIn } from "@/test/applicationHarness";
import { ApplicationDetailPage } from "./ApplicationDetailPage";

// Each environment its own schema (#2709) through the page and the same MSW
// handlers the dev mock server serves: shop runs dev in shop_dev and prod in
// shop, with site parts calling each by name and one calling nothing;
// warehouse, still naming its schema wh, is shown the prepare banner. The
// handlers keep state for the whole file, so the writes come last.

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function renderPage(app = "app-shop") {
  renderWithQuery(<ApplicationDetailPage app={app} onNavigate={vi.fn()} onBack={vi.fn()} />);
}

describe("an application whose environments each run in a schema of their own", () => {
  it("shows each environment's schema and sites, and what each site part calls", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    const dev = await screen.findByTestId("environment-schema-dev");
    expect(dev).toHaveTextContent("shop_dev");
    expect(dev).toHaveTextContent("on acme-shop · migration 3");
    expect(screen.getByTestId("environment-sites-dev")).toHaveTextContent("shop-web / dev");
    expect(screen.getByTestId("environment-sites-prod")).toHaveTextContent("shop-web / prod");
    expect(screen.getByTestId("site-part-calls-shop-web/dev")).toHaveTextContent("shop / dev · release 3 · shop_dev");
    expect(screen.getByTestId("site-part-calls-shop-web/prod")).toHaveTextContent("shop / prod · release 3 · shop");
    expect(screen.getByTestId("site-part-calls-shop-web/staging")).toHaveTextContent(
      "Nothing. shop has no environment named staging, so its /endpoints answer 404.",
    );
    expect(screen.queryByRole("button", { name: "Add environment staging" })).toBeNull();
    expect(screen.queryByTestId("prepare-banner")).toBeNull();
  });

  it("lets an administrator add the environment a site part calls, prefilled", async () => {
    serveApplications();
    signIn("admin@example.com", true);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add environment staging" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByLabelText("Name")).toHaveValue("staging");
    expect(within(dialog).getByLabelText("Schema")).toHaveAttribute("placeholder", "shop_staging");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  });

  it("holds a site environment of no environment's name out of Add parts, with the way to add it", async () => {
    serveApplications();
    signIn("admin@example.com", true);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Add parts" }));
    fireEvent.click(screen.getByRole("tab", { name: /^Sites/ }));
    const ok = await screen.findByTestId("candidate-site-storefront/dev");
    expect(ok).toHaveTextContent("calls shop / dev");
    expect(within(ok).getByRole("checkbox")).toBeEnabled();
    const preview = screen.getByTestId("candidate-site-shop-web/preview");
    expect(within(preview).getByRole("checkbox")).toBeDisabled();
    expect(preview).toHaveTextContent("no environment named preview");
    expect(screen.getByTestId("site-unmatched-shop-web/preview")).toHaveTextContent(
      "shop-web / preview can be added once shop has an environment named preview.",
    );
    fireEvent.click(screen.getByRole("button", { name: "Add environment preview" }));
    expect(screen.getByRole("heading", { name: "Add environment" })).toBeInTheDocument();
    expect(screen.getByLabelText("Name")).toHaveValue("preview");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  });

  it("shows why an environment a site part calls cannot be removed", async () => {
    const sent = serveApplications();
    signIn("admin@example.com", true);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Remove dev" }));
    expect(screen.getByText(/The schema shop_dev is kept until an administrator drops it\./)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(
      await screen.findByText(
        "dev can't be removed: site part shop-web/dev calls it. Remove that site part first. Its schema shop_dev is kept until an administrator drops it",
      ),
    ).toBeInTheDocument();
    expect(sent.some((r) => r.method === "DELETE" && r.path === "/api/v1/portal/applications/app-shop/environments/dev")).toBe(true);
  });

  it("lists what a promote does after a release lands in dev", async () => {
    serveApplications();
    signIn("admin@example.com", true);
    renderPage();
    await screen.findByTestId("environment-dev");
    fireEvent.click(screen.getByRole("button", { name: "Release" }));
    const releases = screen.getAllByRole("button", { name: "Release" });
    fireEvent.click(releases[releases.length - 1]!);
    await screen.findByLabelText("dev runs release 4");
    fireEvent.click(screen.getByRole("button", { name: "Promote dev to prod" }));
    const items = within(screen.getByTestId("promote-list")).getAllByRole("listitem").map((li) => li.textContent);
    expect(items).toEqual(["runs 1 changed script in prod", "applies migration 4 to shop", "moves shop-web / prod (coupled) to release 31"]);
    fireEvent.click(within(screen.getByTestId("promote-dialog")).getByRole("button", { name: "Promote" }));
    expect(await screen.findByLabelText("prod runs release 4")).toBeInTheDocument();
  });
});

describe("an application still naming its schema", () => {
  it("shows the prepare banner, the change and its replay, and saves it", async () => {
    const sent = serveApplications();
    signIn("admin@example.com", true);
    renderPage("app-warehouse");
    const banner = await screen.findByTestId("prepare-banner");
    expect(banner).toHaveTextContent(
      "warehouse runs in prod only. 2 scripts, 1 library and 1 pending migration name the schema wh or its roles. A second environment would run them against prod's schema, so prepare them first.",
    );
    expect(screen.getByRole("button", { name: "Add environment" })).toBeDisabled();
    fireEvent.click(within(banner).getByRole("button", { name: "Review the change" }));
    const dialog = await screen.findByTestId("prepare-dialog");
    expect(within(dialog).getByRole("heading", { name: "Prepare warehouse for environments" })).toBeInTheDocument();
    await within(dialog).findByTestId("prepare-files");
    expect(dialog).toHaveTextContent("4 files change. Each loses the schema and role names it carries; nothing else in it changes.");
    const script = within(dialog).getByTestId("prepare-file-script-wh-01");
    expect(script).toHaveTextContent("Receive stock · version 17 → 18");
    expect(script).toHaveTextContent('- rows = platform.query("acme-shop", "SELECT * FROM wh.receipt", {})');
    expect(script).toHaveTextContent('+ rows = platform.query("acme-shop", "SELECT * FROM receipt", {})');
    const migration = within(dialog).getByTestId("prepare-file-migration-2");
    expect(migration).toHaveTextContent("migration 2 (pending)");
    expect(migration).toHaveTextContent("-GRANT SELECT ON wh.refund_reason TO wh_write;");
    expect(within(dialog).getByTestId("prepare-replay")).toHaveTextContent("Replayed 5 runs of the changed scripts: every answer is the same.");
    expect(dialog).toHaveTextContent("Grants come from the platform: each environment's run role gets what the migrations create in its schema.");
    fireEvent.click(within(dialog).getByRole("button", { name: "Save 4 new versions" }));
    expect(await within(dialog).findByText("Saved. Release the new versions to put them live, then add the environment.")).toBeInTheDocument();
    expect(within(dialog).getByTestId("prepare-file-script-wh-01")).toHaveTextContent("Receive stock · version 17 → 18");
    expect(sent.some((r) => r.method === "POST" && r.path === "/api/v1/portal/applications/app-warehouse/prepare")).toBe(true);
    fireEvent.click(within(dialog).getByRole("button", { name: "Done" }));
    expect(await screen.findByRole("button", { name: "Add environment" })).toBeEnabled();
    expect(screen.queryByTestId("prepare-banner")).toBeNull();
  });
});
