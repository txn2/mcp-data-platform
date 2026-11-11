import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { useSite } from "@/api/portal/hooks/sites";
import { renderWithQuery, signIn } from "@/test/applicationHarness";
import { SiteDetailPage } from "./SiteDetailPage";
import { EnvironmentSettingsDialog } from "./SiteEnvironmentDialogs";
import { SiteReleasePage } from "./SiteReleasePage";
import { serveSites } from "./siteTestServer";

// #2602: each environment's card says how many releases it keeps; the owner
// or an administrator changes it in the environment's settings, warned how
// many releases it stops keeping; someone who manages sees it read-only. The
// Releases tab says what keeps each release and marks the removed ones, which
// a rollback no longer offers.

afterEach(() => vi.unstubAllGlobals());
beforeEach(() => resetSitesMock());

function environments() {
  renderWithQuery(<SiteDetailPage site="marketing" tab="environments" onNavigate={vi.fn()} />);
}

describe("releases kept (#2602)", () => {
  it("says on each card how many releases it keeps", async () => {
    serveSites();
    signIn("admin@example.com", true);
    environments();
    expect(await screen.findByTestId("kept-dev")).toHaveTextContent("Last 3 · 3 kept");
    expect(screen.getByTestId("kept-dev")).toHaveTextContent("Set for dev");
    expect(screen.getByTestId("kept-stage")).toHaveTextContent("Last 50 · 1 kept");
    expect(screen.getByTestId("kept-stage")).toHaveTextContent("Deployment default");
    expect(screen.getByTestId("kept-prod")).toHaveTextContent("All · 2 kept");
    expect(screen.getByTestId("kept-prod")).toHaveTextContent("Every release prod has served");
  });

  it("lets the owner lower prod's limit, warning what it stops keeping", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    environments();
    const prod = await screen.findByTestId("env-prod");
    fireEvent.click(within(prod).getByRole("button", { name: "Settings" }));
    const dialog = await screen.findByRole("dialog", { name: "prod settings" });
    const kept = within(dialog).getByTestId("releases-kept");
    expect(within(kept).getByRole("radio", { name: "All" })).toBeChecked();
    fireEvent.change(within(kept).getByRole("spinbutton", { name: "Releases to keep" }), { target: { value: "1" } });
    expect(within(dialog).getByTestId("keep-warning")).toHaveTextContent("1 release prod served will no longer be kept");
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent.find((r) => r.method === "PUT" && r.path.endsWith("/environments/prod"))?.body).toMatchObject({ keep_releases: 1 }));
  });

  it("sends default when the owner goes back to the deployment's default", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    environments();
    const dev = await screen.findByTestId("env-dev");
    fireEvent.click(within(dev).getByRole("button", { name: "Settings" }));
    const dialog = await screen.findByRole("dialog", { name: "dev settings" });
    fireEvent.click(within(dialog).getByRole("radio", { name: "Deployment default" }));
    expect(within(dialog).getByText("The last 50 releases dev has served.")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent.find((r) => r.method === "PUT" && r.path.endsWith("/environments/dev"))?.body).toMatchObject({ keep_releases: "default" }));
  });

  it("shows someone who manages without owning the limit, read-only, and never sends it", async () => {
    const sent = serveSites();
    signIn("david.park@example.com", false);
    function AsManager() {
      const { data } = useSite("marketing");
      if (!data) return null;
      const env = data.environments.find((e) => e.name === "prod")!;
      return <EnvironmentSettingsDialog site={{ ...data, permissions: { manage: true, own: false, delete: false } }} env={env} onClose={vi.fn()} />;
    }
    renderWithQuery(<AsManager />);
    const dialog = await screen.findByRole("dialog", { name: "prod settings" });
    expect(within(dialog).getByRole("radio", { name: "All" })).toBeDisabled();
    expect(within(dialog).getByTestId("keep-locked")).toHaveTextContent("Only the site’s owner or an administrator changes this");
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent.some((r) => r.method === "PUT")).toBe(true));
    expect(sent.find((r) => r.method === "PUT")?.body).not.toHaveProperty("keep_releases");
  });

  it("rolls back only to a kept release", async () => {
    serveSites();
    signIn("admin@example.com", true);
    environments();
    const dev = await screen.findByTestId("env-dev");
    fireEvent.click(within(dev).getByRole("button", { name: "Roll back" }));
    const dialog = await screen.findByRole("dialog", { name: "Roll back dev" });
    await waitFor(() => expect(within(dialog).getByText("Release 11")).toBeInTheDocument());
    expect(within(dialog).getByText("Release 10")).toBeInTheDocument();
    expect(within(dialog).queryByText("Release 8")).toBeNull();
    expect(within(dialog).getByTestId("rollback-kept")).toHaveTextContent("dev keeps the last 3 releases it served");
  });

  it("lists what keeps each release, and marks a removed one with no link", async () => {
    serveSites();
    signIn("admin@example.com", true);
    renderWithQuery(<SiteDetailPage site="marketing" tab="releases" onNavigate={vi.fn()} />);
    const nine = await screen.findByTestId("release-9");
    expect(nine).toHaveTextContent("Application shop");
    expect(screen.getByTestId("release-11")).toHaveTextContent("stage");
    const eight = screen.getByTestId("release-8");
    expect(within(eight).getByTestId("release-removed")).toHaveTextContent("Removed");
    expect(within(eight).queryByRole("button", { name: "8" })).toBeNull();
    expect(screen.getByTestId("release-12")).toHaveTextContent("edit pricing/index.html");
    expect(within(screen.getByTestId("release-5")).getAllByText("None").length).toBe(2);
  });

  it("opens a removed release as history", async () => {
    serveSites();
    signIn("admin@example.com", true);
    renderWithQuery(<SiteReleasePage site="marketing" number={8} onNavigate={vi.fn()} />);
    expect(await screen.findByTestId("release-removed")).toHaveTextContent("Release 8 was removed");
  });
});
