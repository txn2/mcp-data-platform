import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, act, within, waitFor } from "@testing-library/react";
import type { Share } from "@/api/portal/types";

// What the dialog sends, captured at the hooks, so the tests assert on the
// request rather than on the rendered button alone.
const m = vi.hoisted(() => {
  const mutation = () => ({ mutate: vi.fn(), mutateAsync: vi.fn(() => Promise.resolve({})), isPending: false });
  return {
    shares: [] as unknown[],
    fileShares: [] as unknown[],
    createAsset: mutation(),
    createColl: mutation(),
    createPrompt: mutation(),
    createFile: mutation(),
    revoke: mutation(),
    revokeFile: mutation(),
    setPermission: mutation(),
    setFilePermission: mutation(),
    setLink: mutation(),
    removeLink: mutation(),
    channelShare: mutation(),
    fileChannel: mutation(),
    channels: [] as unknown[],
    personas: [] as { name: string; display_name: string; member_count?: number }[],
    directory: [] as { email: string; first_name?: string; last_name?: string; confirmed: boolean }[],
  };
});

vi.mock("@/api/portal/hooks", () => ({
  useAsset: () => ({ data: undefined }),
  useCollection: () => ({ data: undefined }),
  useShares: (id: string) => ({ data: id ? m.shares : [], isLoading: false }),
  useCollectionShares: (id: string) => ({ data: id ? m.shares : [], isLoading: false }),
  usePromptShares: (id: string) => ({ data: id ? m.shares : [], isLoading: false }),
  useCreateShare: () => m.createAsset,
  useCreateCollectionShare: () => m.createColl,
  useCreatePromptShare: () => m.createPrompt,
  useRevokeShare: () => m.revoke,
  useSetSharePermission: () => m.setPermission,
  useSetGeneralLink: () => m.setLink,
  useRemoveGeneralLink: () => m.removeLink,
  usePortalChannels: () => ({ data: { channels: m.channels } }),
  useShareToChannel: () => m.channelShare,
  useSharePersonas: () => ({ data: { personas: m.personas } }),
  useDirectoryUsers: (q: string) => ({
    isSuccess: true,
    data: { users: m.directory.filter((u) => `${u.first_name ?? ""} ${u.last_name ?? ""} ${u.email}`.toLowerCase().includes(q.toLowerCase())) },
  }),
  useAccessRequests: () => ({ data: { requests: [] } }),
  useDecideAccessRequest: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@/api/resources/hooks", () => ({ useResource: () => ({ data: undefined }) }));

vi.mock("@/api/resources/shares", () => ({
  useResourceShares: (id: string) => ({ data: id ? m.fileShares : [], isLoading: false }),
  useCreateResourceShare: () => m.createFile,
  useRevokeResourceShare: () => m.revokeFile,
  useSetResourceSharePermission: () => m.setFilePermission,
  usePostResourceToChannel: () => m.fileChannel,
}));

import { ShareDialog, type ShareTarget } from "./ShareDialog";
import { useAuthStore } from "@/stores/auth";

const OWNER = "owner@example.com";

function share(over: Partial<Share>): Share {
  return {
    id: "s", asset_id: "ast-1", token: "tok", created_by: OWNER, permission: "viewer", access_mode: "restricted",
    revoked: false, access_count: 0, created_at: "2026-10-01T12:00:00Z", ...over,
  };
}

function renderDialog(target: Partial<ShareTarget> = {}) {
  return render(
    <ShareDialog target={{ type: "asset", id: "ast-1", name: "Q3 revenue", ownerEmail: OWNER, ...target }} open onOpenChange={() => {}} />,
  );
}

/** type puts text in the search box and lets its debounce settle. */
async function type(text: string, box = screen.getAllByRole("combobox")[0]!) {
  vi.useFakeTimers();
  try {
    fireEvent.change(box, { target: { value: text } });
    await act(async () => {
      vi.advanceTimersByTime(300);
    });
  } finally {
    vi.useRealTimers();
  }
}

/** pick chooses the result whose text matches name. */
function pick(name: RegExp) {
  fireEvent.mouseDown(screen.getByRole("option", { name }));
}

/** openMenu opens a Radix menu from the keyboard: jsdom has no PointerEvent. */
function openMenu(trigger: HTMLElement) {
  fireEvent.keyDown(trigger, { key: "Enter" });
}

/** choose picks an option of a Radix select, opened from the keyboard. */
function choose(control: RegExp, option: RegExp) {
  fireEvent.keyDown(screen.getByLabelText(control), { key: "Enter" });
  fireEvent.click(screen.getByRole("option", { name: option }));
}

beforeEach(() => {
  for (const k of Object.keys(m) as (keyof typeof m)[]) {
    const v = m[k] as { mutateAsync?: ReturnType<typeof vi.fn>; mutate?: ReturnType<typeof vi.fn> };
    v.mutateAsync?.mockClear();
    v.mutate?.mockClear();
  }
  m.shares = [];
  m.fileShares = [];
  m.channels = [];
  m.personas = [{ name: "finance", display_name: "Finance", member_count: 12 }];
  m.directory = [
    { email: OWNER, first_name: "Olive", last_name: "Owner", confirmed: true },
    { email: "fiona@example.com", first_name: "Fiona", last_name: "Field", confirmed: true },
    { email: "sam@example.com", first_name: "Sam", last_name: "Stone", confirmed: true },
  ];
  useAuthStore.setState({ user: { user_id: "u1", email: OWNER, roles: [] } as never });
});

describe("the Share view (#2291)", () => {
  it("opens on who has access, with one obvious way out", () => {
    m.channels = [{ name: "team", kind: "slack", mode: "", carries: "", targets: [{ id: "C1", name: "general" }] }];
    renderDialog();
    expect(screen.getByRole("heading", { name: "Share “Q3 revenue”" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Add people or groups" })).toHaveFocus();
    expect(screen.getByText("Olive Owner (you)")).toBeInTheDocument();
    expect(screen.getByText("Owner")).toBeInTheDocument();
    expect(screen.getByText("Not shared with anyone yet.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "General access: Only people added" })).toBeInTheDocument();
    for (const name of ["Copy link", "Post to a channel", "Done"]) expect(screen.getByRole("button", { name })).toBeInTheDocument();
    for (const name of [/create link/i, /^share$/i, /^post$/i]) expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
  });

  it("lists groups and people for two letters, and picking one starts adding", async () => {
    renderDialog();
    await type("fi");
    const list = screen.getByRole("listbox");
    expect(within(list).getByText("Groups")).toBeInTheDocument();
    expect(within(list).getByText("12 people")).toBeInTheDocument();
    expect(within(list).getByText("People")).toBeInTheDocument();
    expect(within(list).getByText("fiona@example.com")).toBeInTheDocument();
    pick(/Fiona Field/);
    expect(screen.getAllByTestId("share-chip")).toHaveLength(1);
    expect(screen.getByLabelText("Send an email")).toBeChecked();
    expect(screen.getByPlaceholderText("Add a message (optional)")).toBeInTheDocument();
    expect(screen.getByText("500 characters left")).toBeInTheDocument();
  });

  it("invites an address with no account as a guest who can only view", async () => {
    renderDialog();
    await type("dana@client.example.org");
    pick(/dana@client\.example\.org/);
    expect(within(screen.getByTestId("share-chip")).getByText("dana@client.example.org")).toBeInTheDocument();
    expect(screen.getByTestId("guest-note")).toHaveTextContent(
      "dana@client.example.org doesn’t have an account. They’ll get an email with a link to view it. Guests can view but not edit.",
    );
    fireEvent.keyDown(screen.getByLabelText("What they can do"), { key: "Enter" });
    expect(screen.getByRole("option", { name: /Can edit/ })).toHaveAttribute("data-disabled");
    fireEvent.keyDown(screen.getByRole("option", { name: /Can view/ }), { key: "Escape" });
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    await waitFor(() =>
      expect(m.createAsset.mutateAsync).toHaveBeenCalledWith({ assetId: "ast-1", shared_with_email: "dana@client.example.org", permission: "viewer" }),
    );
  });

  it("shares with three at once and says so", async () => {
    renderDialog();
    await type("fiona");
    pick(/Fiona Field/);
    const more = screen.getAllByRole("combobox")[0]!;
    await type("sam", more);
    pick(/Sam Stone/);
    await type("finance", more);
    pick(/Finance/);
    expect(screen.getAllByTestId("share-chip")).toHaveLength(3);
    fireEvent.change(screen.getByPlaceholderText("Add a message (optional)"), { target: { value: "  for Friday  " } });
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    await waitFor(() => expect(m.createAsset.mutateAsync).toHaveBeenCalledTimes(3));
    expect(m.createAsset.mutateAsync).toHaveBeenCalledWith({ assetId: "ast-1", shared_with_persona: "finance", permission: "viewer", message: "for Friday" });
    expect(await screen.findByText("Shared with Fiona Field and 2 others")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Add people or groups" })).toBeInTheDocument();
  });

  it("keeps a refused recipient with the server's words, and shares the rest", async () => {
    m.createAsset.mutateAsync.mockImplementation(((body: { shared_with_email?: string }) =>
      body.shared_with_email === "sam@example.com" ? Promise.reject(new Error("no persona of that name")) : Promise.resolve({})) as never);
    renderDialog();
    await type("fiona");
    pick(/Fiona Field/);
    await type("sam", screen.getAllByRole("combobox")[0]!);
    pick(/Sam Stone/);
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    expect(await screen.findByTestId("share-refusal")).toHaveTextContent("Sam Stone: no persona of that name");
    expect(screen.getAllByTestId("share-chip")).toHaveLength(1);
    m.createAsset.mutateAsync.mockImplementation((() => Promise.resolve({})) as never);
  });

  it("sends no email and no message when Send an email is off", async () => {
    renderDialog();
    await type("fiona");
    pick(/Fiona Field/);
    fireEvent.change(screen.getByPlaceholderText("Add a message (optional)"), { target: { value: "kept" } });
    fireEvent.click(screen.getByLabelText("Send an email"));
    expect(screen.queryByPlaceholderText("Add a message (optional)")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    await waitFor(() =>
      expect(m.createAsset.mutateAsync).toHaveBeenCalledWith({ assetId: "ast-1", shared_with_email: "fiona@example.com", permission: "viewer", notify: false }),
    );
  });
});

describe("changing who has access (#2291)", () => {
  const fiona = share({ id: "g1", shared_with_email: "fiona@example.com", permission: "viewer" });

  it("changes a grant's permission at once", () => {
    m.shares = [fiona];
    renderDialog();
    openMenu(screen.getByRole("button", { name: "What Fiona Field can do: Can view" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Can edit" }));
    expect(m.setPermission.mutateAsync).toHaveBeenCalledWith({ shareId: "g1", permission: "editor" });
  });

  it("removes access, and Undo puts it back without a second email", async () => {
    m.shares = [share({ ...fiona, permission: "editor" })];
    renderDialog();
    openMenu(screen.getByRole("button", { name: /What Fiona Field can do/ }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Remove access" }));
    expect(m.revoke.mutateAsync).toHaveBeenCalledWith("g1");
    fireEvent.click(await screen.findByRole("button", { name: "Undo" }));
    expect(m.createAsset.mutateAsync).toHaveBeenCalledWith({ assetId: "ast-1", shared_with_email: "fiona@example.com", permission: "editor", notify: false });
  });

  it("shows a guest's row with what a guest can do", () => {
    m.shares = [share({ id: "g2", shared_with_email: "dana@client.example.org" })];
    renderDialog();
    expect(screen.getByText("Guest")).toBeInTheDocument();
    expect(screen.getByText("No account. Opens it from a link sent by email.")).toBeInTheDocument();
    openMenu(screen.getByRole("button", { name: /What dana@client\.example\.org can do/ }));
    expect(screen.getByRole("menuitem", { name: /Can edit/ })).toHaveAttribute("data-disabled");
    expect(screen.getByText("Guests can only view.")).toBeInTheDocument();
  });

  it("names a group by its people, and keeps an old grant's countdown", () => {
    const soon = new Date(Date.now() + 3 * 24 * 3600 * 1000 + 3600 * 1000).toISOString();
    m.shares = [share({ id: "g3", shared_with_persona: "finance", expires_at: soon })];
    renderDialog();
    expect(screen.getByText("Group · 12 people · Access ends in 3 days")).toBeInTheDocument();
  });
});

describe("General access (#2291)", () => {
  function chooseMode(from: string, to: string) {
    openMenu(screen.getByRole("button", { name: `General access: ${from}` }));
    fireEvent.click(screen.getByRole("menuitem", { name: new RegExp(to) }));
  }

  it("makes the one link anyone can open, with the default lifetime", () => {
    renderDialog();
    chooseMode("Only people added", "Anyone with the link");
    expect(m.setLink.mutateAsync).toHaveBeenCalledWith({ target: { type: "asset", id: "ast-1" }, access_mode: "public", expires_in: "24h" });
  });

  it("changes the same link to signed-in people, and removes it for Only people added", () => {
    m.shares = [share({ id: "l1", access_mode: "public", expires_at: "2099-01-01T12:00:00Z", notice_text: "Board only" })];
    const { unmount } = renderDialog();
    expect(screen.getByTestId("general-sentence")).toHaveTextContent("No sign-in needed. Anyone it’s forwarded to can open it too.");
    expect(screen.getByRole("button", { name: "Copy link" }).className).toContain("bg-primary");
    expect(screen.getByText("Jan 1, 2099")).toBeInTheDocument();
    chooseMode("Anyone with the link", "Anyone who can sign in");
    expect(m.setLink.mutateAsync).toHaveBeenCalledWith({ target: { type: "asset", id: "ast-1" }, access_mode: "authenticated" });
    unmount();

    m.shares = [share({ id: "l1", access_mode: "authenticated" })];
    renderDialog();
    chooseMode("Anyone who can sign in", "Only people added");
    expect(m.removeLink.mutateAsync).toHaveBeenCalledWith({ type: "asset", id: "ast-1" });
  });

  it("edits the banner the shared page shows", () => {
    m.shares = [share({ id: "l1", access_mode: "authenticated", notice_text: "Proprietary" })];
    renderDialog();
    expect(screen.getByTestId("banner-text")).toHaveTextContent("“Proprietary”");
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Banner on the shared page"), { target: { value: "Board only " } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(m.setLink.mutateAsync).toHaveBeenCalledWith({
      target: { type: "asset", id: "ast-1" }, access_mode: "authenticated", notice_text: "Board only", hide_expiration: false,
    });
  });

  it("lists links made before an item had one, apart from the general one", () => {
    m.shares = [
      share({ id: "old", access_mode: "authenticated", created_at: "2026-09-01T12:00:00Z", access_count: 4 }),
      share({ id: "new", access_mode: "authenticated", created_at: "2026-10-01T12:00:00Z" }),
    ];
    renderDialog();
    expect(screen.getByTestId("other-link")).toHaveTextContent("Other link · Anyone who can sign in · opened 4 times");
    fireEvent.click(within(screen.getByTestId("other-link")).getByRole("button", { name: "Remove" }));
    expect(m.revoke.mutateAsync).toHaveBeenCalledWith("old");
  });
});

describe("posting to a channel (#2291)", () => {
  beforeEach(() => {
    m.channels = [{ name: "team", kind: "slack", mode: "", carries: "", targets: [{ id: "C1", name: "general" }] }];
  });

  it("posts from its own view and returns to the Share view", async () => {
    m.channelShare.mutate.mockImplementation(((_b: unknown, h: { onSuccess: (r: unknown) => void }) => h.onSuccess({ queued: 1 })) as never);
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Post to a channel" }));
    expect(screen.getByRole("heading", { name: "Post to a channel" })).toBeInTheDocument();
    choose(/^Channel$/, /#general · Slack/);
    fireEvent.change(screen.getByPlaceholderText("Say something about it"), { target: { value: "Numbers are in" } });
    expect(screen.getByTestId("post-preview")).toHaveTextContent("Numbers are in");
    expect(screen.getByText(/Everyone in this channel sees the preview image/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Post" }));
    expect(m.channelShare.mutate).toHaveBeenCalledWith(
      { assetId: "ast-1", channel: "team", target: "C1", message: "Numbers are in", access_mode: "authenticated", expires_in: undefined },
      expect.anything(),
    );
    expect(await screen.findByText("Posted to #general")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Add people or groups" })).toBeInTheDocument();
    m.channelShare.mutate.mockReset();
  });

  it("lists a post and stops its link after the confirm", () => {
    m.shares = [share({ id: "p1", access_mode: "authenticated", origin: "channel_post", posted_to: "#general (team)", access_count: 1 })];
    renderDialog();
    const row = screen.getByTestId("posted-row");
    expect(row).toHaveTextContent("#general");
    expect(row).toHaveTextContent("Slack · posted Oct 1 · opened once");
    fireEvent.click(within(row).getByRole("button", { name: "Remove" }));
    expect(row).toHaveTextContent("The link in #general stops working.");
    fireEvent.click(within(screen.getByTestId("posted-row")).getByRole("button", { name: "Remove" }));
    expect(m.revoke.mutateAsync).toHaveBeenCalledWith("p1");
  });

  it("keeps the form and says why when the post is refused", async () => {
    m.channelShare.mutate.mockImplementation(((_b: unknown, h: { onError: (e: unknown) => void }) => h.onError(new Error("the bot left the channel"))) as never);
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Post to a channel" }));
    choose(/^Channel$/, /#general · Slack/);
    fireEvent.click(screen.getByRole("button", { name: "Post" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("the bot left the channel");
    expect(screen.getByRole("heading", { name: "Post to a channel" })).toBeInTheDocument();
    m.channelShare.mutate.mockReset();
  });
});

describe("what each kind of item offers (#2291)", () => {
  beforeEach(() => {
    m.channels = [{ name: "team", kind: "slack", mode: "", carries: "", targets: [{ id: "C1", name: "general" }] }];
  });

  it("a collection has general access and no channel", () => {
    renderDialog({ type: "collection", id: "c1" });
    expect(screen.getByRole("button", { name: /General access/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Post to a channel" })).not.toBeInTheDocument();
  });

  it("a prompt has neither", () => {
    renderDialog({ type: "prompt", id: "p1" });
    expect(screen.queryByRole("button", { name: /General access/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Post to a channel" })).not.toBeInTheDocument();
  });

  it("a file has no general access, and is posted for the people who can open it", () => {
    m.fileShares = [share({ id: "f1", resource_id: "r1", shared_with_email: "fiona@example.com" })];
    renderDialog({ type: "resource", id: "r1" });
    expect(screen.queryByRole("button", { name: /General access/ })).not.toBeInTheDocument();
    openMenu(screen.getByRole("button", { name: /What Fiona Field can do/ }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Can edit" }));
    expect(m.setFilePermission.mutateAsync).toHaveBeenCalledWith({ shareId: "f1", permission: "editor" });
    fireEvent.click(screen.getByRole("button", { name: "Post to a channel" }));
    fireEvent.keyDown(screen.getByLabelText("Who can open it"), { key: "Enter" });
    expect(screen.getByRole("option", { name: "Only people with access" })).toBeInTheDocument();
  });
});
