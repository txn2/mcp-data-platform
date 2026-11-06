import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import {
  activationCurrent,
  externalLinkProps,
  frameMessageURL,
  LINK_SCRIPT_SOURCE,
  linkToOpen,
  OPEN_LINK_MESSAGE,
  openFromFrame,
  withAssetLinks,
} from "./assetLinks";

describe("linkToOpen", () => {
  it("opens http, https and mailto addresses, normalized by the URL parser", () => {
    expect(linkToOpen("https://example.com/a?b=1#c")).toBe("https://example.com/a?b=1#c");
    expect(linkToOpen("http://example.com")).toBe("http://example.com/");
    expect(linkToOpen("HTTPS://Example.COM/x")).toBe("https://example.com/x");
    expect(linkToOpen("mailto:someone@example.com?subject=Hi")).toBe("mailto:someone@example.com?subject=Hi");
  });

  it("refuses every other scheme, however it is written", () => {
    for (const raw of [
      "javascript:alert(1)",
      " javascript:alert(1)",
      "JavaScript:alert(1)",
      "java\tscript:alert(1)",
      "java\nscript:alert(1)",
      "\u0000javascript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      "blob:https://example.com/uuid",
      "file:///etc/passwd",
      "vbscript:msgbox(1)",
      "tel:+15551234",
      "ftp://example.com",
      "about:blank",
      "mcp:asset:ast_1",
    ]) {
      expect(linkToOpen(raw), raw).toBeNull();
    }
  });

  it("refuses relative, protocol-relative and malformed addresses, and anything that is not a string", () => {
    for (const raw of ["//evil.example.com", "/portal/assets", "evil.example.com", "#top", "", "https://", "http://[::1", 42, null, undefined, { url: "https://example.com" }]) {
      expect(linkToOpen(raw), String(raw)).toBeNull();
    }
    expect(linkToOpen("https://example.com/" + "a".repeat(9000))).toBeNull();
  });
});

describe("frameMessageURL", () => {
  it("reads a link message and a video message, and nothing else", () => {
    expect(frameMessageURL({ type: OPEN_LINK_MESSAGE, url: "https://example.com" })).toBe("https://example.com/");
    expect(frameMessageURL({ type: "mcp:open-video", video: "vimeo:76979871" })).toBe("https://vimeo.com/76979871");
    expect(frameMessageURL({ type: OPEN_LINK_MESSAGE, url: "javascript:alert(1)" })).toBeNull();
    expect(frameMessageURL({ type: "mcp:open-video", url: "https://example.com" })).toBeNull();
    expect(frameMessageURL({ type: "other", url: "https://example.com" })).toBeNull();
    expect(frameMessageURL("https://example.com")).toBeNull();
    expect(frameMessageURL(null)).toBeNull();
  });
});

describe("openFromFrame", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    Reflect.deleteProperty(navigator, "userActivation");
  });

  const frame = {} as Window;
  const msg = (data: unknown, source: unknown) => new MessageEvent("message", { data, source: source as Window });

  it("opens an allowed link from the frame it rendered in a new tab with noopener and noreferrer", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    for (const url of ["https://example.com/", "http://example.com/", "mailto:a@example.com"]) {
      expect(openFromFrame(msg({ type: OPEN_LINK_MESSAGE, url }, frame), frame)).toBe(true);
      expect(open).toHaveBeenLastCalledWith(url, "_blank", "noopener,noreferrer");
    }
  });

  it("ignores a message from any other window, and from no window", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    const data = { type: OPEN_LINK_MESSAGE, url: "https://example.com" };
    expect(openFromFrame(msg(data, window), frame)).toBe(false);
    expect(openFromFrame(msg(data, {} as Window), frame)).toBe(false);
    expect(openFromFrame(msg(data, null), frame)).toBe(false);
    expect(openFromFrame(msg(data, frame), null)).toBe(false);
    expect(open).not.toHaveBeenCalled();
  });

  it("ignores a refused scheme from the right frame", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    for (const url of ["javascript:alert(1)", "data:text/html,x", "blob:https://x/y", "file:///etc/passwd", "//evil.example.com"]) {
      expect(openFromFrame(msg({ type: OPEN_LINK_MESSAGE, url }, frame), frame)).toBe(false);
    }
    expect(open).not.toHaveBeenCalled();
  });

  it("opens nothing when the reader's gesture is not current", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    Object.defineProperty(navigator, "userActivation", { value: { isActive: false, hasBeenActive: true }, configurable: true });
    expect(activationCurrent()).toBe(false);
    expect(openFromFrame(msg({ type: OPEN_LINK_MESSAGE, url: "https://example.com" }, frame), frame)).toBe(false);
    Object.defineProperty(navigator, "userActivation", { value: { isActive: true, hasBeenActive: true }, configurable: true });
    expect(openFromFrame(msg({ type: OPEN_LINK_MESSAGE, url: "https://example.com" }, frame), frame)).toBe(true);
    expect(open).toHaveBeenCalledTimes(1);
  });
});

describe("withAssetLinks", () => {
  it("puts the handler first in <head>, ahead of the document's own scripts", () => {
    const out = withAssetLinks(`<!doctype html><html><head><script>mine()</script></head><body></body></html>`);
    expect(out.indexOf(OPEN_LINK_MESSAGE)).toBeGreaterThan(out.indexOf("<head>"));
    expect(out.indexOf(OPEN_LINK_MESSAGE)).toBeLessThan(out.indexOf("mine()"));
  });

  it("finds a place in a document without a head, without html, and in a fragment", () => {
    expect(withAssetLinks(`<html lang="en"><body>x</body></html>`)).toMatch(/^<html lang="en"><head><script>/);
    expect(withAssetLinks(`<!DOCTYPE html><p>x</p>`)).toMatch(/^<!DOCTYPE html><script>/);
    expect(withAssetLinks(`<p>x</p>`)).toMatch(/^<script>.*<\/script><p>x<\/p>$/s);
  });
});

describe("the frame's link handler", () => {
  let post: ReturnType<typeof vi.fn>;
  let originalOpen: ReturnType<typeof vi.fn>;

  beforeAll(() => {
    originalOpen = vi.fn(() => null);
    window.open = originalOpen as unknown as typeof window.open;
    new Function(LINK_SCRIPT_SOURCE)();
  });

  afterEach(() => {
    document.body.innerHTML = "";
    vi.restoreAllMocks();
    originalOpen.mockClear();
  });

  function spyPost() {
    post = vi.fn();
    vi.spyOn(window.parent, "postMessage").mockImplementation(post as unknown as typeof window.postMessage);
  }

  function click(el: Element, init: MouseEventInit = {}): MouseEvent {
    const ev = new MouseEvent("click", { bubbles: true, cancelable: true, composed: true, button: 0, ...init });
    el.dispatchEvent(ev);
    return ev;
  }

  it("posts a click on an http, https or mailto link and keeps the frame where it is", () => {
    spyPost();
    document.body.innerHTML = `<a id="w" href="https://example.com/x">site</a><a id="m" href="mailto:a@example.com">mail</a><a id="t" href="http://example.com" target="_blank"><span id="inner">t</span></a>`;
    expect(click(document.getElementById("w")!).defaultPrevented).toBe(true);
    expect(click(document.getElementById("m")!).defaultPrevented).toBe(true);
    expect(click(document.getElementById("inner")!).defaultPrevented).toBe(true);
    expect(post.mock.calls.map((c) => c[0])).toEqual([
      { type: OPEN_LINK_MESSAGE, url: "https://example.com/x" },
      { type: OPEN_LINK_MESSAGE, url: "mailto:a@example.com" },
      { type: OPEN_LINK_MESSAGE, url: "http://example.com/" },
    ]);
    expect(post.mock.calls[0]?.[1]).toBe("*");
  });

  it("moves the frame's own fragment for an in-page anchor rather than resolving it against the page around it", () => {
    spyPost();
    document.body.innerHTML = `<a id="h" href="#section">jump</a><h2 id="section">s</h2>`;
    expect(click(document.getElementById("h")!).defaultPrevented).toBe(true);
    expect(window.location.hash).toBe("#section");
    expect(post).not.toHaveBeenCalled();
    window.location.hash = "";
  });

  it("leaves download links, other schemes and cancelled clicks to the frame", () => {
    spyPost();
    document.body.innerHTML =
      `<a id="d" href="https://example.com/f.csv" download>get</a>` +
      `<a id="j" href=" javascript:alert(1)">js</a><a id="da" href="data:text/html,x">data</a><a id="e" href="">empty</a>` +
      `<a id="c" href="https://example.com">cancelled</a><a id="n">no href</a>`;
    document.getElementById("c")!.addEventListener("click", (e) => e.preventDefault());
    for (const id of ["d", "j", "da", "e", "n"]) {
      expect(click(document.getElementById(id)!).defaultPrevented, id).toBe(false);
    }
    click(document.getElementById("c")!);
    click(document.getElementById("c")!, { button: 1 });
    expect(post).not.toHaveBeenCalled();
  });

  it("resolves a relative link against the document's base, as the browser would", () => {
    spyPost();
    document.body.innerHTML = `<a id="r" href="/somewhere">rel</a>`;
    click(document.getElementById("r")!);
    expect(post).toHaveBeenCalledWith({ type: OPEN_LINK_MESSAGE, url: new URL("/somewhere", document.baseURI).href }, "*");
  });

  it("posts window.open with an allowed scheme, and hands anything else to the browser", () => {
    spyPost();
    expect(window.open("https://example.com/w", "_blank")).toBeNull();
    expect(post).toHaveBeenCalledWith({ type: OPEN_LINK_MESSAGE, url: "https://example.com/w" }, "*");
    expect(originalOpen).not.toHaveBeenCalled();
    post.mockClear();
    window.open("javascript:alert(1)");
    window.open();
    window.open("");
    expect(post).not.toHaveBeenCalled();
    expect(originalOpen).toHaveBeenCalledTimes(3);
  });
});

describe("externalLinkProps", () => {
  it("opens another site in a new tab without opener or referrer", () => {
    expect(externalLinkProps("https://example.com")).toEqual({ target: "_blank", rel: "noopener noreferrer" });
    expect(externalLinkProps("http://example.com/x")).toEqual({ target: "_blank", rel: "noopener noreferrer" });
  });

  it("leaves portal links, anchors, mailto and other schemes as written", () => {
    for (const href of [undefined, "", "#top", "/portal/assets/x", `${window.location.origin}/portal`, "mailto:a@example.com", "mcp:asset:ast_1", "http://[::1"]) {
      expect(externalLinkProps(href), String(href)).toEqual({});
    }
  });
});
