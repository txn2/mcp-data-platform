import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { useSite, type Site } from "@/api/portal/hooks/sites";
import { renderWithQuery, signIn } from "@/test/applicationHarness";
import { SiteDetailPage } from "./SiteDetailPage";
import { ShareDialog } from "./SiteShare";
import { serveSites } from "./siteTestServer";

// #2587: Share and Add person open one dialog offering Can view and Can
// manage. Can manage stays in view, unavailable and saying why, for someone who
// manages without owning, a persona and an external person.

afterEach(() => vi.unstubAllGlobals());
beforeEach(() => resetSitesMock());

async function pick(dialog: HTMLElement, text: string, option: RegExp) {
  const box = within(dialog).getByRole("combobox", { name: "Name, email or persona" });
  fireEvent.change(box, { target: { value: text } });
  await waitFor(() => expect(within(dialog).getByRole("option", { name: option })).toBeInTheDocument());
  fireEvent.keyDown(box, { key: "Enter" });
}

async function shareFromStage() {
  renderWithQuery(<SiteDetailPage site="marketing" tab="environments" onNavigate={vi.fn()} />);
  const stage = await screen.findByTestId("env-stage");
  fireEvent.click(within(stage).getByRole("button", { name: "Share" }));
  return screen.findByRole("dialog", { name: "Share marketing" });
}

function SharedAs({ permissions }: { permissions: Site["permissions"] }) {
  const { data } = useSite("marketing");
  if (!data) return null;
  return <ShareDialog site={{ ...data, permissions }} env="stage" onClose={vi.fn()} />;
}

describe("the Share dialog (#2587)", () => {
  it("lets the owner give Can manage, which covers every environment", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    const dialog = await shareFromStage();
    await pick(dialog, "client@client.example", /client@client\.example/);
    fireEvent.click(within(dialog).getByRole("radio", { name: "Can manage" }));
    expect(within(dialog).queryByRole("checkbox", { name: "stage" })).toBeNull();
    expect(within(dialog).getByTestId("manage-covers")).toHaveTextContent("covers every environment");
    fireEvent.click(within(dialog).getByRole("button", { name: "Share" }));
    await waitFor(() =>
      expect(sent.find((r) => r.method === "PUT" && r.path === "/api/v1/portal/sites/marketing/access")?.body).toEqual({
        email: "client@client.example",
        level: "manage",
        notify: true,
      }),
    );
  });

  it("shows Can manage to someone who manages without owning, unavailable, saying why", async () => {
    serveSites();
    signIn("david.park@example.com", false);
    renderWithQuery(<SharedAs permissions={{ manage: true, own: false, delete: false }} />);
    const dialog = await screen.findByRole("dialog", { name: "Share marketing" });
    expect(within(dialog).getByRole("radio", { name: "Can manage" })).toBeDisabled();
    expect(within(dialog).getByRole("radio", { name: "Can view" })).toBeChecked();
    expect(within(dialog).getByTestId("manage-why")).toHaveTextContent("Only the site’s owner or an administrator can add someone who manages it.");
  });

  it("offers a persona Can view only", async () => {
    serveSites();
    signIn("admin@example.com", true);
    const dialog = await shareFromStage();
    await pick(dialog, "management", /management-team/);
    expect(within(dialog).getByRole("radio", { name: "Can manage" })).toBeDisabled();
    expect(within(dialog).getByTestId("manage-why")).toHaveTextContent("A persona can be given Can view only.");
  });

  it("refuses Can manage to an external person and says why", async () => {
    serveSites();
    signIn("admin@example.com", true);
    const dialog = await shareFromStage();
    await pick(dialog, "dana.reyes", /dana\.reyes@client-a\.example\.com/);
    await waitFor(() => expect(within(dialog).getByRole("radio", { name: "Can manage" })).toBeDisabled());
    expect(within(dialog).getByTestId("manage-why")).toHaveTextContent("dana.reyes@client-a.example.com is external. Only internal people can manage a site.");
  });

  it("opens the same dialog from Add person on the Access tab, on Can manage", async () => {
    serveSites();
    signIn("admin@example.com", true);
    renderWithQuery(<SiteDetailPage site="marketing" tab="access" onNavigate={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Add person" }));
    const dialog = await screen.findByRole("dialog", { name: "Share marketing" });
    expect(within(dialog).getByRole("radio", { name: "Can manage" })).toBeChecked();
  });
});
