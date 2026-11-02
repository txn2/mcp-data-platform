/**
 * Video embeds an HTML asset carries (#2069).
 *
 * The asset is framed in an opaque origin (ASSET_FRAME_SANDBOX), and the
 * sandbox carries into every frame the asset opens. The Wistia player and a
 * <video> from a CDN play there; the YouTube and Vimeo players do not run at
 * all (checked in Chrome: a black frame, a placeholder with no controls).
 * Their iframes are drawn as a card instead: the video's poster where the
 * provider serves one at a fixed address, and a button that asks the viewer
 * page to open the video on the provider's own site. The page rebuilds the
 * address from the video id (videoWatchURL), so the asset can ask it to open
 * a YouTube or Vimeo video and nothing else. Inline play is #2396.
 *
 * The same document draws the card in the portal, on both kinds of share
 * link and in the tile, so a deck looks the same everywhere it is shown.
 */

/** The message a card posts to the page it is framed in. */
export const OPEN_VIDEO_MESSAGE = "mcp:open-video";

/** A YouTube video id, and a Vimeo one. */
const YOUTUBE_ID = /^[A-Za-z0-9_-]{6,20}$/;
const VIMEO_ID = /^\d{1,12}$/;

/** A YouTube or Vimeo player iframe, with the address its src names. */
const PLAYER_IFRAME =
  /<iframe\b[^>]*?\bsrc\s*=\s*(["'])\s*(?:https?:)?\/\/(?:www\.)?(youtube(?:-nocookie)?\.com\/embed\/|player\.vimeo\.com\/video\/)([A-Za-z0-9_-]+)[^"']*\1[^>]*>(?:\s*<\/iframe\s*>)?/gi;

/** An attribute value of the iframe, read to keep the card the player's size. */
function attr(tag: string, name: string): string {
  const m = new RegExp(`\\b${name}\\s*=\\s*(["'])([^"']*)\\1`, "i").exec(tag);
  return m?.[2] ?? "";
}

/** A length attribute as CSS: a bare number is pixels. */
function cssLength(v: string): string {
  if (/^\d+$/.test(v)) return `${v}px`;
  if (/^\d+(\.\d+)?%$/.test(v)) return v;
  return "";
}

function escapeAttr(v: string): string {
  return v.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
}

/** The video a card names, as `youtube:<id>` or `vimeo:<id>`, or null. */
function videoOf(kind: string, id: string): string | null {
  if (kind.startsWith("youtube") && YOUTUBE_ID.test(id)) return `youtube:${id}`;
  if (kind.startsWith("player.vimeo") && VIMEO_ID.test(id)) return `vimeo:${id}`;
  return null;
}

/** The card's markup in place of one player iframe. */
function card(tag: string, video: string): string {
  const [provider, id] = video.split(":") as [string, string];
  const youtube = provider === "youtube";
  const width = cssLength(attr(tag, "width"));
  const height = cssLength(attr(tag, "height"));
  const size = `${width ? `width:${width};` : "width:100%;"}${height ? `height:${height};` : "aspect-ratio:16/9;"}`;
  const poster = youtube
    ? `background-image:url(https://i.ytimg.com/vi/${id}/hqdefault.jpg);background-size:cover;background-position:center;`
    : "";
  const label = youtube ? "Watch on YouTube" : "Watch on Vimeo";
  const title = attr(tag, "title");
  return (
    `<button type="button" data-mcp-video="${escapeAttr(video)}" class="${escapeAttr(attr(tag, "class"))}"` +
    ` aria-label="${escapeAttr(title ? `${label}: ${title}` : label)}"` +
    ` style="${size}${poster}display:flex;align-items:center;justify-content:center;max-width:100%;` +
    `border:0;border-radius:8px;background-color:#111;color:#fff;cursor:pointer;font:600 15px system-ui,sans-serif;">` +
    `<span style="display:inline-flex;align-items:center;gap:8px;padding:10px 16px;border-radius:999px;background:rgba(0,0,0,.72);">` +
    `<span aria-hidden="true">&#9654;</span>${label}</span></button>`
  );
}

/** The script a document with a card carries: a click asks the page to open it. */
const CARD_SCRIPT =
  `<script>document.addEventListener("click",function(e){var t=e.target&&e.target.closest&&e.target.closest("[data-mcp-video]");` +
  `if(!t)return;e.preventDefault();parent.postMessage({type:"${OPEN_VIDEO_MESSAGE}",video:t.getAttribute("data-mcp-video")},"*");});</script>`;

/**
 * content with each YouTube and Vimeo player iframe drawn as a card, and the
 * one script the cards need. A document with neither is returned as given.
 */
export function withVideoCards(content: string): string {
  if (!/youtube|vimeo/i.test(content)) return content;
  let carded = false;
  const out = content.replace(PLAYER_IFRAME, (tag: string, _q: string, kind: string, id: string) => {
    const video = videoOf(kind.toLowerCase(), id);
    if (!video) return tag;
    carded = true;
    return card(tag, video);
  });
  if (!carded) return content;
  return /<\/body\s*>/i.test(out) ? out.replace(/<\/body\s*>/i, `${CARD_SCRIPT}</body>`) : out + CARD_SCRIPT;
}

/**
 * The address a card's video opens at, rebuilt from its id, or null for
 * anything that is not a YouTube or Vimeo video id. The page opens nothing
 * else on an asset's behalf.
 */
export function videoWatchURL(video: unknown): string | null {
  if (typeof video !== "string") return null;
  const [provider, id, extra] = video.split(":");
  if (extra !== undefined || !id) return null;
  if (provider === "youtube" && YOUTUBE_ID.test(id)) return `https://www.youtube.com/watch?v=${id}`;
  if (provider === "vimeo" && VIMEO_ID.test(id)) return `https://vimeo.com/${id}`;
  return null;
}

/** A host as an embed hosts entry is stored: https, a host, an optional port. */
const EMBED_HOST = /^https:\/\/(\*\.)?[a-z0-9.-]+(:\d{1,5})?$/;

/**
 * The policy the portal's asset frame enforces (#2069): frame-src and
 * media-src limited to the page's own origin and the deployment's embed
 * hosts, as the share viewer's header limits them. An entry not in the stored
 * form is dropped, so nothing else reaches the policy.
 */
export function embedPolicy(hosts: readonly string[]): string {
  const extra = hosts.filter((h) => EMBED_HOST.test(h)).join(" ");
  const sources = `'self' blob: data:${extra ? ` ${extra}` : ""}`;
  return `frame-src ${sources}; media-src ${sources}`;
}

/**
 * content with the embed policy as its first element, so it is in force
 * before anything the document loads. The policy is a meta element, which a
 * browser applies to an srcdoc document alongside any policy it inherits:
 * both must allow a frame or a clip for it to load.
 */
export function withEmbedPolicy(content: string, hosts: readonly string[]): string {
  const meta = `<meta http-equiv="Content-Security-Policy" content="${embedPolicy(hosts)}">`;
  const head = /<head\b[^>]*>/i.exec(content);
  if (head) return content.slice(0, head.index + head[0].length) + meta + content.slice(head.index + head[0].length);
  const html = /<html\b[^>]*>/i.exec(content);
  if (html) return content.slice(0, html.index + html[0].length) + `<head>${meta}</head>` + content.slice(html.index + html[0].length);
  const doctype = /^\s*<!doctype[^>]*>/i.exec(content);
  if (doctype) return content.slice(0, doctype[0].length) + meta + content.slice(doctype[0].length);
  return meta + content;
}
