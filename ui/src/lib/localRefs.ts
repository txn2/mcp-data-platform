/**
 * A document's reference URLs on the page's own origin (#2395).
 *
 * The platform rewrites a declared mcp:// reference to an absolute URL on
 * portal.public_base_url, which is not always the origin the reader browses (a
 * proxy, a relocated port). The asset frame allows media from its own origin
 * ('self') and the embed hosts, so a <video> naming the reference route on
 * another origin was refused, in the portal and on a share link alike. Written
 * root-relative, the address resolves against the page that framed the srcdoc
 * document, which is the reader's own origin. Only the reference route's paths
 * change; the token in each is kept.
 */
const ABSOLUTE_REF = /https?:\/\/[^\s"'<>()/]+(\/portal\/refs\/[A-Za-z0-9_-]+\/[A-Za-z0-9_-]+)/g;

export function withLocalRefs(content: string): string {
  if (!content.includes("/portal/refs/")) return content;
  return content.replace(ABSOLUTE_REF, "$1");
}
