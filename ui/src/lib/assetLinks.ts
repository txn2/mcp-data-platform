import { createContext, useContext, useEffect, type RefObject } from "react";
import { OPEN_VIDEO_MESSAGE, videoWatchURL } from "./videoCards";

/**
 * Links to websites from an HTML or JSX asset (#2501).
 *
 * The asset's frame is sandboxed `allow-scripts allow-downloads`
 * (ASSET_FRAME_SANDBOX) and stays that way: it has no popups and no top
 * navigation, so a link left to the browser would navigate the frame itself,
 * where most sites refuse to be framed. The document the platform frames
 * carries a handler (LINK_SCRIPT_SOURCE) that, for a link the reader clicks to
 * an http:, https: or mailto: address, and for window.open with one, asks the
 * page around the frame to open it. The page opens it in a new tab with
 * noopener and noreferrer, so the site gets no handle on the portal and is not
 * told its address.
 *
 * The page acts on a message only when it comes from the frame it rendered,
 * names one of those three schemes as read by the URL parser, and arrives
 * while the reader's click is still a current user activation. A click inside
 * a frame activates the pages around it, and window.open consumes the
 * activation, so one click opens at most one tab and a script posting links
 * with no click opens nothing. In-page #anchor links and download links are
 * left to the frame.
 */

/** The message the frame posts to the page around it. */
export const OPEN_LINK_MESSAGE = "mcp:open-link";

/** The schemes a link from an asset may open. */
const OPENABLE = new Set(["http:", "https:", "mailto:"]);

/** The longest address the page opens on an asset's behalf. */
const MAX_URL = 8192;

/**
 * The address a frame's message names, normalized by the URL parser, or null
 * for anything that is not an absolute http:, https: or mailto: URL. The
 * scheme is the parser's `.protocol`, compared exactly, so case, surrounding
 * whitespace and embedded tabs or newlines cannot dress up another scheme,
 * and a relative or protocol-relative address (no base is given) is refused.
 */
export function linkToOpen(raw: unknown): string | null {
  if (typeof raw !== "string" || raw.length === 0 || raw.length > MAX_URL) return null;
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  return OPENABLE.has(url.protocol) ? url.href : null;
}

/**
 * The address a message from an asset frame asks the page to open: a video
 * card's watch page, or a link. Null for any other message.
 */
export function frameMessageURL(data: unknown): string | null {
  if (typeof data !== "object" || data === null) return null;
  const msg = data as { type?: unknown; video?: unknown; url?: unknown };
  if (msg.type === OPEN_VIDEO_MESSAGE) return videoWatchURL(msg.video);
  if (msg.type === OPEN_LINK_MESSAGE) return linkToOpen(msg.url);
  return null;
}

/**
 * Whether the reader's gesture is still current. A browser without the
 * UserActivation API is trusted to apply its own popup rule to window.open.
 */
export function activationCurrent(): boolean {
  const ua = typeof navigator === "undefined" ? undefined : navigator.userActivation;
  return ua ? ua.isActive : true;
}

/**
 * Opens what a message from frame asks for, when it may. Returns whether it
 * opened anything.
 */
export function openFromFrame(e: MessageEvent, frame: Window | null | undefined): boolean {
  if (!frame || e.source !== frame) return false;
  const url = frameMessageURL(e.data);
  if (!url || !activationCurrent()) return false;
  window.open(url, "_blank", "noopener,noreferrer");
  return true;
}

/**
 * Links between assets (#2432). An author links another asset by writing
 * `mcp:asset:<id>` as a link target. A link grants nothing: following it opens
 * the target's portal page, whose own access check decides what the reader
 * sees, and nothing about the target is shown before the click.
 *
 * In an HTML or JSX asset the frame's handler posts {type: ASSET_LINK_MESSAGE,
 * id} for a click on such a link, and the page navigates only when the message
 * comes from the frame it rendered, names an id the displayed asset's current
 * content links to, and arrives while the reader's click is current.
 */
export const ASSET_LINK_MESSAGE = "mcp-asset-link";

/** The link target form an asset links another asset by. */
export const ASSET_LINK_PREFIX = "mcp:asset:";

/** The longest asset id a link may name. */
const MAX_ASSET_ID = 200;

/**
 * The asset id an `mcp:asset:<id>` link target names, without a fragment,
 * query or path after it, or null for anything else.
 */
export function assetLinkId(href: string | null | undefined): string | null {
  if (typeof href !== "string") return null;
  const ref = href.trim();
  if (!ref.startsWith(ASSET_LINK_PREFIX)) return null;
  const id = ref.slice(ASSET_LINK_PREFIX.length).split(/[#?/]/)[0] ?? "";
  return id.length > 0 && id.length <= MAX_ASSET_ID ? id : null;
}

/** The portal page an asset opens on. */
export function portalAssetHref(id: string): string {
  return `/portal/assets/${encodeURIComponent(id)}`;
}

/** Who follows a link between assets on this page, and which ids it may follow. */
export interface AssetLinkFollower {
  /**
   * The ids the displayed asset's current content links to. Undefined while
   * unknown, which follows nothing from a frame.
   */
  links: readonly string[] | undefined;
  /** Opens the asset's portal page. */
  open: (id: string) => void;
}

/** The page's follower of links between assets; absent, a frame's link is ignored. */
export const AssetLinkContext = createContext<AssetLinkFollower | null>(null);

/**
 * The asset a frame's message asks the page to open: only from the frame the
 * page rendered, only an ASSET_LINK_MESSAGE naming one of links.
 */
export function linkedAssetFromFrame(
  e: MessageEvent,
  frame: Window | null | undefined,
  links: readonly string[] | undefined,
): string | null {
  if (!frame || e.source !== frame || !links) return null;
  const data = e.data as { type?: unknown; id?: unknown } | null;
  if (typeof data !== "object" || data === null || data.type !== ASSET_LINK_MESSAGE) return null;
  if (typeof data.id !== "string" || !links.includes(data.id)) return null;
  return data.id;
}

/**
 * Follows a frame's link to another asset when it may. Returns whether it
 * did. A message with no current click is ignored, so a script cannot move the
 * reader on its own.
 */
export function followAssetLink(
  e: MessageEvent,
  frame: Window | null | undefined,
  follower: AssetLinkFollower | null,
): boolean {
  if (!follower) return false;
  const id = linkedAssetFromFrame(e, frame, follower.links);
  if (!id || !activationCurrent()) return false;
  follower.open(id);
  return true;
}

/**
 * Every asset an HTML, JSX or Markdown body links to: `mcp:asset:<id>` as an
 * anchor's href or a Markdown link's destination. The public viewer, which has
 * no route to ask, follows a frame's link only to one of these.
 */
export function contentAssetLinks(content: string): string[] {
  const out = new Set<string>();
  const re = /(?:href\s*=\s*\{?\s*["'`]?\s*|\]\(\s*<?)(mcp:asset:[^\s"'`<>()[\]{}\\]+)/gi;
  for (const m of content.matchAll(re)) {
    const id = assetLinkId(m[1]);
    if (id) out.add(id);
  }
  return [...out];
}

/**
 * Listens for the frame's requests to open a video or a link, and to follow a
 * link to another asset when the page has a follower (#2432).
 */
export function useFrameOpener(frameRef: RefObject<HTMLIFrameElement | null>): void {
  const follower = useContext(AssetLinkContext);
  useEffect(() => {
    function onMessage(e: MessageEvent) {
      const frame = frameRef.current?.contentWindow;
      if (followAssetLink(e, frame, follower)) return;
      openFromFrame(e, frame);
    }
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [frameRef, follower]);
}

/**
 * The handler the framed document carries. It runs before the asset's own
 * scripts, in the frame's opaque origin, and resolves a link the way the
 * browser would (against the document's base URL) before posting it. A click
 * an earlier handler already cancelled, a download link and any other scheme
 * are left alone; window.open with no address, or with another scheme, goes to
 * the browser as before. An in-page #anchor moves the frame's own fragment: an
 * srcdoc document resolves `#name` against the page that framed it, so left
 * to the browser it would load the portal page into the frame instead of
 * scrolling.
 */
export const LINK_SCRIPT_SOURCE =
  `(function(){` +
  `var T=${JSON.stringify(OPEN_LINK_MESSAGE)},S={"http:":1,"https:":1,"mailto:":1},X="http://www.w3.org/1999/xlink";` +
  `var M=${JSON.stringify(ASSET_LINK_MESSAGE)},A=${JSON.stringify(ASSET_LINK_PREFIX)};` +
  `function u(r){try{var x=new URL(String(r),document.baseURI);return S[x.protocol]===1?x.href:null}catch(e){return null}}` +
  `function p(h){parent.postMessage({type:T,url:h},"*")}` +
  `function f(e){var q=e.composedPath?e.composedPath():[e.target];for(var i=0;i<q.length;i++){var n=q[i];` +
  `if(n&&n.nodeType===1&&(n.localName==="a"||n.localName==="area")&&(n.hasAttribute("href")||n.hasAttributeNS(X,"href")))return n}return null}` +
  `window.addEventListener("click",function(e){if(e.defaultPrevented||e.button!==0)return;var a=f(e);` +
  `if(!a||a.hasAttribute("download"))return;var r=a.getAttribute("href");if(r===null)r=a.getAttributeNS(X,"href");` +
  `r=String(r).trim();if(r==="")return;if(r.charAt(0)==="#"){e.preventDefault();location.hash=r;return}` +
  `if(r.indexOf(A)===0){e.preventDefault();parent.postMessage({type:M,id:r.slice(A.length).split(/[#?\\/]/)[0]},"*");return}` +
  `var h=u(r);if(!h)return;e.preventDefault();p(h)});` +
  `var o=window.open;window.open=function(r){if(r===undefined||r===null||String(r).trim()==="")return o.apply(window,arguments);` +
  `var h=u(r);if(h){p(h);return null}return o.apply(window,arguments)};` +
  `})();`;

const LINK_SCRIPT = `<script>${LINK_SCRIPT_SOURCE}</script>`;

/**
 * content with the link handler as early as the document allows: first in
 * <head>, else at the start of <html>'s content, else after the doctype, else
 * first. Early, so it is in place before the asset's own scripts run.
 */
export function withAssetLinks(content: string): string {
  const head = /<head\b[^>]*>/i.exec(content);
  if (head) return insertAt(content, head.index + head[0].length, LINK_SCRIPT);
  const html = /<html\b[^>]*>/i.exec(content);
  if (html) return insertAt(content, html.index + html[0].length, `<head>${LINK_SCRIPT}</head>`);
  const doctype = /^\s*<!doctype[^>]*>/i.exec(content);
  if (doctype) return insertAt(content, doctype[0].length, LINK_SCRIPT);
  return LINK_SCRIPT + content;
}

function insertAt(s: string, at: number, what: string): string {
  return s.slice(0, at) + what + s.slice(at);
}

/**
 * What a link in a Markdown document rendered in the portal page carries: a
 * link to another site opens in a new tab with noopener and noreferrer, so the
 * reader keeps the portal (#2501). A link within the portal, an in-page
 * #anchor and a mailto: link (handed to the mail client without leaving the
 * page) are left as written.
 */
export function externalLinkProps(href: string | undefined): { target?: string; rel?: string } {
  if (!href) return {};
  const origin = typeof window === "undefined" ? undefined : window.location.origin;
  let url: URL;
  try {
    url = new URL(href, origin && origin !== "null" ? origin : undefined);
  } catch {
    return {};
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return {};
  if (origin && url.origin === origin) return {};
  return { target: "_blank", rel: "noopener noreferrer" };
}
