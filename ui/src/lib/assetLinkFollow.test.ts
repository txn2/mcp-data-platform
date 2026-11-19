import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import {
  ASSET_LINK_MESSAGE,
  LINK_SCRIPT_SOURCE,
  assetLinkId,
  contentAssetLinks,
  followAssetLink,
  linkedAssetFromFrame,
  portalAssetHref,
} from "./assetLinks";

// Links between assets (#2432): the frame posts a click on an mcp:asset:<id>
// link, and the page follows it only from the frame it rendered, only to an id
// the displayed asset's content links to, and only on the reader's click.

describe("assetLinkId", () => {
  it("reads the id an mcp:asset link names, without a fragment, query or path", () => {
    expect(assetLinkId("mcp:asset:ast-1")).toBe("ast-1");
    expect(assetLinkId("  mcp:asset:ast-1#part ")).toBe("ast-1");
    expect(assetLinkId("mcp:asset:ast-1?v=2")).toBe("ast-1");
    expect(assetLinkId("mcp:asset:ast-1/x")).toBe("ast-1");
  });

  it("refuses anything else", () => {
    for (const href of [undefined, null, "", "mcp:asset:", "mcp://global/a.csv", "https://x", "mcp:collection:c1", "MCP:ASSET:x"]) {
      expect(assetLinkId(href), String(href)).toBeNull();
    }
    expect(assetLinkId("mcp:asset:" + "a".repeat(201))).toBeNull();
  });

  it("opens the portal page", () => {
    expect(portalAssetHref("a b")).toBe("/portal/assets/a%20b");
  });
});

describe("linkedAssetFromFrame", () => {
  const frame = {} as Window;
  const msg = (data: unknown, source: unknown) => new MessageEvent("message", { data, source: source as Window });
  const links = ["ast-b", "ast-c"];

  it("names a linked id from the frame the page rendered", () => {
    expect(linkedAssetFromFrame(msg({ type: ASSET_LINK_MESSAGE, id: "ast-b" }, frame), frame, links)).toBe("ast-b");
  });

  it("ignores an id the content does not link, however it is posted", () => {
    expect(linkedAssetFromFrame(msg({ type: ASSET_LINK_MESSAGE, id: "ast-z" }, frame), frame, links)).toBeNull();
    expect(linkedAssetFromFrame(msg({ type: ASSET_LINK_MESSAGE, id: 7 }, frame), frame, links)).toBeNull();
    expect(linkedAssetFromFrame(msg({ type: ASSET_LINK_MESSAGE }, frame), frame, links)).toBeNull();
  });

  it("ignores a message from any other window, or before the links are known", () => {
    const data = { type: ASSET_LINK_MESSAGE, id: "ast-b" };
    expect(linkedAssetFromFrame(msg(data, window), frame, links)).toBeNull();
    expect(linkedAssetFromFrame(msg(data, {} as Window), frame, links)).toBeNull();
    expect(linkedAssetFromFrame(msg(data, null), frame, links)).toBeNull();
    expect(linkedAssetFromFrame(msg(data, frame), null, links)).toBeNull();
    expect(linkedAssetFromFrame(msg(data, frame), frame, undefined)).toBeNull();
  });

  it("ignores any other message", () => {
    expect(linkedAssetFromFrame(msg({ type: "mcp:open-link", id: "ast-b" }, frame), frame, links)).toBeNull();
    expect(linkedAssetFromFrame(msg("ast-b", frame), frame, links)).toBeNull();
    expect(linkedAssetFromFrame(msg(null, frame), frame, links)).toBeNull();
  });
});

describe("followAssetLink", () => {
  afterEach(() => Reflect.deleteProperty(navigator, "userActivation"));
  const frame = {} as Window;
  const msg = (id: string) => new MessageEvent("message", { data: { type: ASSET_LINK_MESSAGE, id }, source: frame });

  it("opens a linked asset on the reader's click", () => {
    const open = vi.fn();
    expect(followAssetLink(msg("ast-b"), frame, { links: ["ast-b"], open })).toBe(true);
    expect(open).toHaveBeenCalledWith("ast-b");
  });

  it("opens nothing with no follower, an unlinked id, or no current click", () => {
    const open = vi.fn();
    expect(followAssetLink(msg("ast-b"), frame, null)).toBe(false);
    expect(followAssetLink(msg("ast-x"), frame, { links: ["ast-b"], open })).toBe(false);
    Object.defineProperty(navigator, "userActivation", { value: { isActive: false, hasBeenActive: true }, configurable: true });
    expect(followAssetLink(msg("ast-b"), frame, { links: ["ast-b"], open })).toBe(false);
    expect(open).not.toHaveBeenCalled();
  });
});

describe("contentAssetLinks", () => {
  it("reads every mcp:asset link target in HTML, JSX and Markdown, once each", () => {
    const body =
      `<a href="mcp:asset:ast-b">B</a> <a href='mcp:asset:ast-c#x'>C</a> <a href={"mcp:asset:ast-d"}>D</a>` +
      `\n[again](mcp:asset:ast-b) [md](<mcp:asset:ast-e>)`;
    expect(contentAssetLinks(body).sort()).toEqual(["ast-b", "ast-c", "ast-d", "ast-e"]);
  });

  it("leaves out a reference that loads content rather than links it", () => {
    const body = `<img src="mcp:asset:ast-img"> fetch("mcp:asset:ast-data") ![chart](mcp://global/c.png) mcp:asset:ast-bare`;
    expect(contentAssetLinks(body)).toEqual([]);
  });
});

describe("the frame's handler for links between assets", () => {
  let post: ReturnType<typeof vi.fn>;

  beforeAll(() => {
    window.open = vi.fn(() => null) as unknown as typeof window.open;
    new Function(LINK_SCRIPT_SOURCE)();
  });

  afterEach(() => {
    document.body.innerHTML = "";
    vi.restoreAllMocks();
  });

  function click(el: Element): MouseEvent {
    const ev = new MouseEvent("click", { bubbles: true, cancelable: true, composed: true, button: 0 });
    el.dispatchEvent(ev);
    return ev;
  }

  it("posts a click on an mcp:asset link to the page and keeps the frame where it is", () => {
    post = vi.fn();
    vi.spyOn(window.parent, "postMessage").mockImplementation(post as unknown as typeof window.postMessage);
    document.body.innerHTML = `<a id="l" href="mcp:asset:ast-b#top"><span id="in">Regional detail</span></a>`;
    expect(click(document.getElementById("in")!).defaultPrevented).toBe(true);
    expect(post).toHaveBeenCalledWith({ type: ASSET_LINK_MESSAGE, id: "ast-b" }, "*");
  });

  it("leaves a download anchor to the frame", () => {
    post = vi.fn();
    vi.spyOn(window.parent, "postMessage").mockImplementation(post as unknown as typeof window.postMessage);
    document.body.innerHTML = `<a id="d" href="mcp:asset:ast-b" download>save</a>`;
    expect(click(document.getElementById("d")!).defaultPrevented).toBe(false);
    expect(post).not.toHaveBeenCalled();
  });
});
