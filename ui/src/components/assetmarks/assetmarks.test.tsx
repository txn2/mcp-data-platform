import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import type { Asset, AssetLinks, AssetMarkEvent } from "@/api/portal/types";
import { useAuthStore } from "@/stores/auth";

// The asset page's status (#2502), Verified (#2503) and links (#2432) pieces.
// The hooks are stubbed so each test states what the server answered and reads
// what the page sends.

const calls: { name: string; args: unknown }[] = [];
const state: { links?: AssetLinks; history: AssetMarkEvent[] } = { history: [] };

function mutation(name: string) {
  return () => ({
    mutate: (args: unknown, opts?: { onSuccess?: () => void }) => {
      calls.push({ name, args });
      opts?.onSuccess?.();
    },
    reset: () => {},
    isPending: false,
    error: null,
  });
}

vi.mock("@/api/portal/hooks/assetMarks", () => ({
  useSetAssetStatus: mutation("setStatus"),
  useClearAssetStatus: mutation("clearStatus"),
  useVerifyAsset: mutation("verify"),
  useUnverifyAsset: mutation("unverify"),
  useAssetMarkHistory: () => ({ data: state.history }),
  useAssetLinks: () => ({ data: state.links }),
  endOfDayUTC: (d: string) => (/^\d{4}-\d{2}-\d{2}$/.test(d) ? `${d}T23:59:59Z` : null),
}));

import { AssetMarkBadges } from "./AssetMarkBadges";
import { AssetMarkBanner } from "./AssetMarkBanner";
import { AssetMarkControls } from "./AssetMarkControls";
import { AssetMarksSidebar, describeMarkEvent } from "./AssetMarksSidebar";
import { VerifyControls, mayRemoveVerification } from "./VerifyControls";
import { AssetLinkProvider } from "./AssetLinkProvider";
import { useFrameOpener, ASSET_LINK_MESSAGE } from "@/lib/assetLinks";
import { useRef, useEffect } from "react";

type Marked = Pick<Asset, "id" | "name" | "status" | "verification" | "current_version">;

const base: Marked = { id: "a1", name: "Monthly churn", current_version: 6 };
const verified = {
  verified_by: "maria@example.com",
  verified_at: "2026-10-30T12:00:00Z",
  verified_version: 4,
  note: "Checked against the finance close.",
  expires_at: "2027-01-31T23:59:59Z",
  changed_since_verified: true,
};

function signIn(portal: Record<string, unknown> = {}, isAdmin = false, email = "jane@example.com") {
  useAuthStore.setState({ user: { user_id: email, email, roles: [], is_admin: isAdmin, portal } as never });
}

// A Radix menu opens on pointerdown with the primary button, not on click.
function openMenu(trigger: HTMLElement) {
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false, pointerType: "mouse" });
}

beforeEach(() => {
  calls.length = 0;
  state.links = undefined;
  state.history = [];
  signIn();
});

describe("badges", () => {
  it("shows nothing for an asset with no status and no badge", () => {
    const { container } = render(<AssetMarkBadges asset={{}} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the status, Verified and Changed since verified, and leaves the marker off a card", () => {
    render(<AssetMarkBadges asset={{ status: { value: "published", set_by: "a", set_at: "" }, verification: verified }} />);
    expect(screen.getByTestId("asset-status-badge")).toHaveTextContent("Published");
    expect(screen.getByTestId("asset-verified-badge")).toHaveTextContent("Verified");
    expect(screen.getByTestId("asset-changed-since-verified")).toBeInTheDocument();
  });

  it("leaves Changed since verified to the viewer at card size", () => {
    render(<AssetMarkBadges asset={{ verification: verified }} small />);
    expect(screen.queryByTestId("asset-changed-since-verified")).toBeNull();
  });
});

describe("banner", () => {
  it("shows a Deprecated asset's note and who marked it to every reader", () => {
    render(
      <AssetMarkBanner
        asset={{ current_version: 3, status: { value: "deprecated", note: "Replaced by the Q4 revenue report", set_by: "jane@example.com", set_at: "2026-11-12T00:00:00Z" } }}
      />,
    );
    const banner = screen.getByTestId("asset-deprecated-banner");
    expect(banner).toHaveTextContent("Deprecated");
    expect(banner).toHaveTextContent("Replaced by the Q4 revenue report");
    expect(banner).toHaveTextContent("Marked by jane@example.com");
  });

  it("names the verified version on a changed asset and opens it", () => {
    const view = vi.fn();
    render(<AssetMarkBanner asset={{ current_version: 6, verification: verified }} onViewVersion={view} />);
    expect(screen.getByTestId("asset-changed-since-verified-note")).toHaveTextContent(
      "Verified version 4 by maria@example.com. You are viewing version 6.",
    );
    fireEvent.click(screen.getByRole("button", { name: "View version 4" }));
    expect(view).toHaveBeenCalledWith(4);
  });

  it("shows nothing for a draft or an unchanged verified asset", () => {
    const { container } = render(
      <AssetMarkBanner asset={{ current_version: 1, status: { value: "draft", set_by: "a", set_at: "" }, verification: { ...verified, changed_since_verified: false } }} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});

describe("controls", () => {
  it("gives the owner the Status menu and no Verify without the permission", () => {
    render(<AssetMarkControls asset={base} canEdit />);
    expect(screen.getByTestId("asset-status-menu")).toBeInTheDocument();
    expect(screen.queryByTestId("asset-verify")).toBeNull();
  });

  it("gives a viewer-share recipient neither control", () => {
    render(<AssetMarkControls asset={{ ...base, status: { value: "published", set_by: "a", set_at: "" } }} canEdit={false} />);
    expect(screen.queryByTestId("asset-status-menu")).toBeNull();
    expect(screen.queryByTestId("asset-verify")).toBeNull();
    expect(screen.queryByTestId("asset-verified-menu")).toBeNull();
  });

  it("gives a holder of Can verify assets the Verify button, and an administrator too", () => {
    signIn({ verify_assets: true });
    const { unmount } = render(<AssetMarkControls asset={base} canEdit={false} />);
    expect(screen.getByTestId("asset-verify")).toBeInTheDocument();
    unmount();
    signIn({}, true);
    render(<AssetMarkControls asset={base} canEdit={false} />);
    expect(screen.getByTestId("asset-verify")).toBeInTheDocument();
  });

  it("sets a status from the menu, and asks for a note before Deprecated", () => {
    render(<AssetMarkControls asset={base} canEdit />);
    openMenu(screen.getByTestId("asset-status-menu"));
    expect(screen.getByText(/Status changes no access/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitem", { name: /Published/ }));
    expect(calls).toEqual([{ name: "setStatus", args: { id: "a1", status: "published" } }]);

    openMenu(screen.getByTestId("asset-status-menu"));
    fireEvent.click(screen.getByRole("menuitem", { name: /Deprecated/ }));
    const dialog = screen.getByRole("dialog", { name: "Mark as Deprecated" });
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "Replaced by the Q4 revenue report" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Mark as Deprecated" }));
    expect(calls[1]).toEqual({ name: "setStatus", args: { id: "a1", status: "deprecated", note: "Replaced by the Q4 revenue report" } });
  });

  it("clears a status, and offers Clear only when there is one", () => {
    const { unmount } = render(<AssetMarkControls asset={base} canEdit />);
    openMenu(screen.getByTestId("asset-status-menu"));
    expect(screen.getByRole("menuitem", { name: "Clear status" })).toHaveAttribute("aria-disabled", "true");
    unmount();
    render(<AssetMarkControls asset={{ ...base, status: { value: "draft", set_by: "a", set_at: "" } }} canEdit />);
    openMenu(screen.getByTestId("asset-status-menu"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Clear status" }));
    expect(calls).toEqual([{ name: "clearStatus", args: "a1" }]);
  });
});

describe("Verify", () => {
  it("verifies with a note and the end of the chosen day", () => {
    render(<VerifyControls asset={base} canVerify email="jane@example.com" />);
    fireEvent.click(screen.getByTestId("asset-verify"));
    const dialog = screen.getByRole("dialog", { name: 'Verify "Monthly churn"' });
    expect(dialog).toHaveTextContent("Readers see that you checked version 6.");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "Checked against the finance close." } });
    fireEvent.change(within(dialog).getByLabelText("Expiry date"), { target: { value: "2027-01-31" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Verify" }));
    expect(calls).toEqual([
      { name: "verify", args: { id: "a1", note: "Checked against the finance close.", expires_at: "2027-01-31T23:59:59Z" } },
    ]);
  });

  it("asks for a date when On is chosen without one", () => {
    render(<VerifyControls asset={base} canVerify />);
    fireEvent.click(screen.getByTestId("asset-verify"));
    const dialog = screen.getByRole("dialog");
    fireEvent.click(within(dialog).getAllByRole("radio")[1]!);
    fireEvent.click(within(dialog).getByRole("button", { name: "Verify" }));
    expect(calls).toEqual([]);
    expect(dialog).toHaveTextContent("Choose the date it expires, or Never.");
  });

  it("offers a holder Verify version N, Change note or expiry, and Remove on a verified asset", () => {
    render(<VerifyControls asset={{ ...base, verification: verified }} canVerify />);
    openMenu(screen.getByTestId("asset-verified-menu"));
    expect(screen.getByRole("menuitem", { name: "Verify version 6" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Change note or expiry" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitem", { name: "Remove verification" }));
    expect(calls).toEqual([{ name: "unverify", args: "a1" }]);
  });

  it("lets the person who verified remove it without the permission, and nobody else", () => {
    const { unmount } = render(<VerifyControls asset={{ ...base, verification: verified }} canVerify={false} email="Maria@example.com" />);
    expect(screen.getByTestId("asset-verified-menu")).toBeInTheDocument();
    unmount();
    const { container } = render(<VerifyControls asset={{ ...base, verification: verified }} canVerify={false} email="sam@example.com" />);
    expect(container).toBeEmptyDOMElement();
    expect(mayRemoveVerification(false, undefined, "a@example.com")).toBe(false);
    expect(mayRemoveVerification(true, undefined, undefined)).toBe(true);
  });
});

describe("details panel", () => {
  it("shows who verified, the version, the note and the expiry", () => {
    render(<AssetMarksSidebar asset={{ id: "a1", verification: verified }} />);
    const detail = screen.getByTestId("asset-verified-detail");
    expect(detail).toHaveTextContent("Verified by maria@example.com");
    expect(detail).toHaveTextContent("version 4");
    expect(detail).toHaveTextContent('"Checked against the finance close."');
    expect(detail).toHaveTextContent("Expires");
  });

  it("lists the assets linking here that the reader can open, and hides the section with none", () => {
    state.links = { links: [], linked_from: [] };
    const { unmount } = render(<AssetMarksSidebar asset={{ id: "a1" }} />);
    expect(screen.queryByTestId("asset-linked-from")).toBeNull();
    unmount();
    state.links = { links: [], linked_from: [{ id: "g1", name: "Finance reports guide", content_type: "text/markdown" }] };
    const nav = vi.fn();
    render(<AssetMarksSidebar asset={{ id: "a1" }} assetPath={(id) => `/assets/${id}`} onNavigate={nav} />);
    const section = screen.getByTestId("asset-linked-from");
    expect(section).toHaveTextContent("Linked from");
    fireEvent.click(within(section).getByRole("button", { name: "Finance reports guide" }));
    expect(nav).toHaveBeenCalledWith("/assets/g1");
  });

  it("describes each status and verification change", () => {
    const e = (x: Partial<AssetMarkEvent>): AssetMarkEvent => ({ kind: "status", actor: "a", at: "", ...x });
    expect(describeMarkEvent(e({ to: "draft" }))).toBe("Status set to Draft");
    expect(describeMarkEvent(e({ from: "draft", to: "published" }))).toBe("Status Draft → Published");
    expect(describeMarkEvent(e({ from: "deprecated" }))).toBe("Status cleared (was Deprecated)");
    expect(describeMarkEvent(e({ kind: "verification", to: "verified" }))).toBe("Verified");
    expect(describeMarkEvent(e({ kind: "verification", from: "verified" }))).toBe("Verification removed");
  });

  it("lists the history when there is one", () => {
    state.history = [{ kind: "status", from: "draft", to: "published", actor: "jane@example.com", at: "2026-11-12T00:00:00Z" }];
    render(<AssetMarksSidebar asset={{ id: "a1" }} />);
    expect(screen.getByTestId("asset-status-history")).toHaveTextContent("1 change");
  });
});

/** A frame stand-in that listens the way the HTML and JSX renderers do. */
function FrameHost({ frameWindow }: { frameWindow: Window }) {
  const ref = useRef<HTMLIFrameElement>(null);
  useEffect(() => {
    Object.defineProperty(ref.current!, "contentWindow", { value: frameWindow });
  }, [frameWindow]);
  useFrameOpener(ref);
  return <iframe ref={ref} title="asset" />;
}

describe("links from the asset's frame", () => {
  it("opens a linked asset through the portal's navigation, and nothing else", () => {
    state.links = { links: ["ast-b"], linked_from: [] };
    const frameWindow = {} as Window;
    const nav = vi.fn();
    render(
      <AssetLinkProvider assetId="a1" onNavigate={nav}>
        <FrameHost frameWindow={frameWindow} />
      </AssetLinkProvider>,
    );
    const post = (data: unknown, source: unknown) =>
      window.dispatchEvent(new MessageEvent("message", { data, source: source as Window }));
    post({ type: ASSET_LINK_MESSAGE, id: "ast-z" }, frameWindow);
    post({ type: ASSET_LINK_MESSAGE, id: "ast-b" }, window);
    expect(nav).not.toHaveBeenCalled();
    post({ type: ASSET_LINK_MESSAGE, id: "ast-b" }, frameWindow);
    expect(nav).toHaveBeenCalledWith("/assets/ast-b");
  });
});
