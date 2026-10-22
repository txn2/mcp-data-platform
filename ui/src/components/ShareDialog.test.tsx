import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";

// Capture what the dialog sends to the create-share mutations, so the tests
// assert on the request rather than on the rendered button alone.
const { createAsset, createColl, createPrompt, revoke, channelShare, channelList, sharePersonas } = vi.hoisted(() => ({
  createAsset: { mutate: vi.fn(), isPending: false },
  createColl: { mutate: vi.fn(), isPending: false },
  createPrompt: { mutate: vi.fn(), isPending: false },
  revoke: { mutate: vi.fn() },
  channelShare: { mutate: vi.fn(), isPending: false },
  channelList: { data: { channels: [] as unknown[] }, isLoading: false, error: null as unknown },
  sharePersonas: { data: { personas: [] as { name: string; display_name: string }[] } },
}));

vi.mock("@/api/portal/hooks", () => ({
  useShares: () => ({ data: [] }),
  useCollectionShares: () => ({ data: [] }),
  usePromptShares: () => ({ data: [] }),
  useCreateShare: () => createAsset,
  useCreateCollectionShare: () => createColl,
  useCreatePromptShare: () => createPrompt,
  useRevokeShare: () => revoke,
  usePortalChannels: () => channelList,
  useShareToChannel: () => channelShare,
  useSharePersonas: () => sharePersonas,
  useAccessRequests: () => ({ data: { requests: [] } }),
  useDecideAccessRequest: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@/components/UserPicker", () => ({
  UserPicker: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <input aria-label="Recipient" value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

import { ShareDialog } from "./ShareDialog";

function renderDialog() {
  return render(
    <ShareDialog target={{ type: "asset", id: "ast-1" }} open onOpenChange={() => {}} />,
  );
}

/**
 * choose picks an option from one of the dialog's Radix listboxes. jsdom has no
 * PointerEvent, so the trigger's pointerdown handler never runs and the listbox
 * has to be opened from the keyboard (see ui/README.md).
 */
function choose(control: RegExp, option: RegExp) {
  fireEvent.keyDown(screen.getByLabelText(control), { key: "Enter" });
  fireEvent.click(screen.getByRole("option", { name: option }));
}

describe("ShareDialog access modes", () => {
  beforeEach(() => {
    createAsset.mutate.mockClear();
  });

  it("creates a signed-in-users link by default, with no anonymous-access warning", () => {
    renderDialog();

    expect(screen.queryByText(/works without signing in/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /create link/i }));

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ assetId: "ast-1", access_mode: "authenticated" }),
      expect.anything(),
    );
  });

  it("warns that a public link opens without sign-in and sends access_mode public", () => {
    renderDialog();

    choose(/who can open this link/i, /anyone with the link/i);
    expect(screen.getByText(/works without signing in/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /create link/i }));

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ access_mode: "public" }),
      expect.anything(),
    );
  });

  it("offers no lifetime for a signed-in-users link and sends none", () => {
    renderDialog();

    expect(screen.queryByLabelText(/link expiration/i)).not.toBeInTheDocument();
    expect(screen.getByText(/does not expire/i)).toBeInTheDocument();

    // The expiration-notice option has no deadline to announce either.
    fireEvent.click(screen.getByRole("button", { name: /options/i }));
    expect(screen.queryByLabelText(/show expiration notice/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /create link/i }));

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.not.objectContaining({ expires_in: expect.anything() }),
      expect.anything(),
    );
  });

  it("sends the chosen lifetime with a public link", () => {
    renderDialog();

    choose(/who can open this link/i, /anyone with the link/i);
    choose(/link expiration/i, /7 days/i);
    fireEvent.click(screen.getByRole("button", { name: /create link/i }));

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ access_mode: "public", expires_in: "168h" }),
      expect.anything(),
    );
  });

  it("shows the server's refusal instead of a button that does nothing", () => {
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: /create link/i }));

    // The dialog cannot know the server's lifetime rule, so a refusal has to
    // reach the sharer rather than being swallowed.
    const opts = createAsset.mutate.mock.calls[0]?.[1];
    act(() => {
      opts.onError(new Error("expires_in does not apply to a link only signed-in users can open"));
    });

    expect(screen.getByRole("alert")).toHaveTextContent(/only signed-in users can open/i);
  });

  it("states that a recipient share opens only for that person", () => {
    renderDialog();

    expect(
      screen.getByText(/only this person can open the link/i),
    ).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Recipient"), {
      target: { value: "bob@example.com" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^share$/i }));

    // No access_mode is sent: the server defaults a named-recipient share to
    // restricted, which is the behavior this section describes. No expires_in
    // either: a share addressed to a person ends by revocation.
    expect(createAsset.mutate).toHaveBeenCalledWith(
      {
        assetId: "ast-1",
        shared_with_email: "bob@example.com",
        permission: "viewer",
      },
      expect.anything(),
    );
  });

  it("sends the permission chosen for the recipient", () => {
    renderDialog();

    fireEvent.change(screen.getByLabelText("Recipient"), {
      target: { value: "bob@example.com" },
    });
    choose(/permission/i, /editor/i);
    fireEvent.click(screen.getByRole("button", { name: /^share$/i }));

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ permission: "editor" }),
      expect.anything(),
    );
  });
});

describe("ShareDialog recipient notification", () => {
  beforeEach(() => {
    createAsset.mutate.mockClear();
  });

  function nameRecipient(email = "bob@example.com") {
    fireEvent.change(screen.getByLabelText("Recipient"), { target: { value: email } });
  }

  function share() {
    fireEvent.click(screen.getByRole("button", { name: /^share$/i }));
  }

  it("offers no notify control until a recipient is named", () => {
    renderDialog();
    expect(screen.queryByLabelText(/notify by email/i)).not.toBeInTheDocument();

    nameRecipient();
    expect(screen.getByLabelText(/notify by email/i)).toBeChecked();
  });

  it("sends no notify field when notification is left on", () => {
    renderDialog();
    nameRecipient();
    share();

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.not.objectContaining({ notify: expect.anything() }),
      expect.anything(),
    );
  });

  it("sends notify false and hides the message box when notification is off", () => {
    renderDialog();
    nameRecipient();
    fireEvent.click(screen.getByLabelText(/notify by email/i));

    // The note travels only in the email, so it has nowhere to go.
    expect(screen.queryByLabelText(/message/i)).not.toBeInTheDocument();

    share();
    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ notify: false }),
      expect.anything(),
    );
  });

  it("sends a trimmed message with the share", () => {
    renderDialog();
    nameRecipient();
    fireEvent.change(screen.getByLabelText(/message/i), {
      target: { value: "  Here is the Q3 breakdown  " },
    });
    share();

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ message: "Here is the Q3 breakdown" }),
      expect.anything(),
    );
  });

  it("omits an empty message rather than sending a blank field", () => {
    renderDialog();
    nameRecipient();
    fireEvent.change(screen.getByLabelText(/message/i), { target: { value: "   " } });
    share();

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.not.objectContaining({ message: expect.anything() }),
      expect.anything(),
    );
  });

  it("normalizes a pasted display-name address before sending", () => {
    renderDialog();
    nameRecipient("Bob Jones <Bob@Example.COM>");
    share();

    expect(createAsset.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ shared_with_email: "bob@example.com" }),
      expect.anything(),
    );
  });

  it("refuses input that names no address, without calling the API", () => {
    renderDialog();
    nameRecipient("Bob Jones");
    share();

    expect(createAsset.mutate).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent(/single email address/i);
  });
});

describe("ShareDialog channel destination (#2241)", () => {
  beforeEach(() => {
    channelShare.mutate.mockClear();
    channelList.data = {
      channels: [
        {
          name: "team-chat", kind: "mattermost", mode: "immediate", carries: "",
          targets: [{ id: "mm-town", name: "town-square" }],
        },
        { name: "ops-email", kind: "email", mode: "daily", carries: "", targets: [] },
        { name: "slack", kind: "slack", mode: "immediate", carries: "", targets: [], targets_error: "missing_scope" },
        { name: "hook", kind: "webhook", mode: "immediate", carries: "", targets: [] },
      ],
    };
  });

  it("offers the bot's chat channels and the other channels, and says what readers see", () => {
    renderDialog();
    expect(screen.getByText(/could not be read: missing_scope/i)).toBeInTheDocument();
    choose(/^channel$/i, /#town-square \(team-chat\)/i);
    expect(screen.getByText(/everyone in this channel sees the asset's preview image/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^post$/i }));
    expect(channelShare.mutate).toHaveBeenCalledWith(
      expect.objectContaining({
        assetId: "ast-1", channel: "team-chat", target: "mm-town", access_mode: "authenticated", expires_in: undefined,
      }),
      expect.anything(),
    );
  });

  it("sends a public link with its expiration", () => {
    renderDialog();
    choose(/^channel$/i, /ops-email \(email\)/i);
    choose(/who can open the posted link/i, /anyone with the link/i);
    choose(/posted link expiration/i, /30 days/i);
    fireEvent.click(screen.getByRole("button", { name: /^post$/i }));
    expect(channelShare.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ channel: "ops-email", access_mode: "public", expires_in: "720h" }),
      expect.anything(),
    );
  });

  it("states what was posted, and clears the choice so a second post is deliberate", () => {
    renderDialog();
    choose(/^channel$/i, /^hook$/i);
    expect(screen.queryByText(/everyone in this channel sees/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^post$/i }));
    const opts = channelShare.mutate.mock.calls[0]?.[1];
    act(() => {
      opts.onSuccess({ channel: "hook", queued: 1, detail: "accepted" });
      opts.onSettled();
    });
    expect(screen.getByRole("status")).toHaveTextContent("Posted to hook.");
    expect(screen.getByRole("button", { name: /^post$/i })).toBeDisabled();
  });

  it("shows a refusal in the server's words", () => {
    renderDialog();
    choose(/^channel$/i, /^hook$/i);
    fireEvent.click(screen.getByRole("button", { name: /^post$/i }));
    const opts = channelShare.mutate.mock.calls[0]?.[1];
    act(() => {
      opts.onError(new Error("only the asset's owner can post a link"));
      opts.onSettled();
    });
    expect(screen.getByRole("status")).toHaveTextContent("only the asset's owner");
  });

  it("has no channel section when no channel is reachable, and none for a collection", () => {
    channelList.data = { channels: [] };
    renderDialog();
    expect(screen.queryByText(/share to a channel/i)).not.toBeInTheDocument();
    channelList.data = { channels: [{ name: "hook", kind: "webhook", mode: "immediate", carries: "", targets: [] }] };
    render(<ShareDialog target={{ type: "collection", id: "c1" }} open onOpenChange={() => {}} />);
    expect(screen.queryByText(/share to a channel/i)).not.toBeInTheDocument();
  });
});

describe("ShareDialog personas (#2089)", () => {
  beforeEach(() => {
    createAsset.mutate.mockReset();
    sharePersonas.data = { personas: [] };
  });

  it("offers no persona choice when there is no persona to share with", () => {
    renderDialog();
    expect(screen.queryByRole("radiogroup", { name: "Share with" })).not.toBeInTheDocument();
  });

  it("shares with a persona chosen under Personas", () => {
    sharePersonas.data = { personas: [{ name: "finance", display_name: "Finance" }] };
    renderDialog();
    fireEvent.click(screen.getByRole("radio", { name: "Personas" }));
    choose(/^Persona$/, /Finance/);
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    expect(createAsset.mutate).toHaveBeenCalledTimes(1);
    const body = createAsset.mutate.mock.calls[0]![0] as Record<string, unknown>;
    expect(body).toMatchObject({ assetId: "ast-1", shared_with_persona: "finance", permission: "viewer" });
    expect(body).not.toHaveProperty("shared_with_email");
  });
});
