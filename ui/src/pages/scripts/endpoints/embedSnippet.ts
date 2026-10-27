// The snippet a site embeds a form's hosted page with (#2331), kept apart from
// the component that offers it so the browser acceptance case frames exactly
// what the portal hands out.

// The message a form's page posts its parent with its height
// (epformpage.HeightMessage).
export const HEIGHT_MESSAGE = "mcp-form:height";

/** pageUrl as an absolute address, on origin when the platform wrote a path. */
export function absolutePageURL(pageUrl: string, origin: string): string {
  return /^https?:\/\//.test(pageUrl) ? pageUrl : origin.replace(/\/+$/, "") + pageUrl;
}

/**
 * The snippet: a frame of the form's hosted page and the few lines that size
 * the frame to the height the page posts. Only a message from the platform's
 * origin, from that frame, resizes it.
 */
export function embedSnippet(pageUrl: string, name: string): string {
  const origin = new URL(pageUrl).origin;
  return [
    `<iframe src="${pageUrl}" title="${name}" data-platform-form style="width:100%;border:0" loading="lazy"></iframe>`,
    "<script>",
    'window.addEventListener("message", function (e) {',
    `  if (e.origin !== ${JSON.stringify(origin)} || !e.data || e.data.type !== ${JSON.stringify(HEIGHT_MESSAGE)}) return;`,
    '  document.querySelectorAll("iframe[data-platform-form]").forEach(function (f) {',
    '    if (f.contentWindow === e.source) f.style.height = e.data.height + "px";',
    "  });",
    "});",
    "</script>",
  ].join("\n");
}
