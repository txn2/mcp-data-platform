import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { renderWithQuery, signIn } from "@/test/applicationHarness";
import { SiteDetailPage } from "./SiteDetailPage";
import { serveSites } from "./siteTestServer";

// #2591: a site serves Resources folders at addresses on every environment.
// Adding one publishes what is in it, so the dialog says where it becomes
// public, and the server refuses a folder the person cannot read.

afterEach(() => vi.unstubAllGlobals());
beforeEach(() => resetSitesMock());

function media() {
  renderWithQuery(<SiteDetailPage site="marketing" tab="media" onNavigate={vi.fn()} />);
}

describe("media folders (#2591)", () => {
  it("lists the folders a site serves and where they are public", async () => {
    serveSites();
    signIn("admin@example.com", true);
    media();
    const table = await screen.findByTestId("media-table");
    expect(table).toHaveTextContent("/media/");
    expect(table).toHaveTextContent("sites/marketing/downloads");
    expect(screen.getByTestId("media-public")).toHaveTextContent("prod is public");
  });

  it("removes a folder from the site and leaves the rest", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    media();
    const table = await screen.findByTestId("media-table");
    fireEvent.click(within(table).getAllByRole("button", { name: "Remove" })[1]!);
    await waitFor(() =>
      expect(sent.find((r) => r.method === "PATCH")?.body).toEqual({ media: [{ prefix: "/media/", scope: "global", scope_id: "", folder: "sites/marketing/media" }] }),
    );
  });

  it("adds a folder, saying what becomes public", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    media();
    fireEvent.click(await screen.findByRole("button", { name: "Add media folder" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a media folder to marketing" });
    expect(within(dialog).getByTestId("media-goes-public")).toHaveTextContent("Files in this folder become public on prod");
    fireEvent.change(within(dialog).getByLabelText("Address"), { target: { value: "/brand/" } });
    fireEvent.change(within(dialog).getByLabelText("Folder"), { target: { value: "sites/marketing/brand" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Add media folder" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const body = sent.find((r) => r.method === "PATCH")?.body as { media: { prefix: string; folder: string }[] };
    expect(body.media).toHaveLength(3);
    expect(body.media[2]).toMatchObject({ prefix: "/brand/", folder: "sites/marketing/brand" });
  });

  it("shows the refusal of a folder the person cannot read", async () => {
    serveSites();
    signIn("admin@example.com", true);
    media();
    fireEvent.click(await screen.findByRole("button", { name: "Add media folder" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a media folder to marketing" });
    fireEvent.change(within(dialog).getByLabelText("Folder"), { target: { value: "reports/2026" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Add media folder" }));
    expect(await within(dialog).findByTestId("media-refused")).toHaveTextContent("there is no folder reports/2026");
  });

  it("is a tab for the people who manage the site only", async () => {
    serveSites();
    signIn("admin@example.com", true);
    renderWithQuery(<SiteDetailPage site="docs" tab="media" onNavigate={vi.fn()} />);
    await screen.findByRole("tab", { name: "Environments" });
    expect(screen.queryByRole("tab", { name: "Media" })).toBeNull();
    expect(screen.queryByTestId("media-table")).toBeNull();
  });
});
