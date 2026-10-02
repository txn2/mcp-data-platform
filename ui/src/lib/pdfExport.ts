/**
 * The PDF of an HTML document, printed by the platform's renderer (#1983).
 *
 * Every route that serves a document's bytes at `.../content` has a `.../pdf`
 * beside it that prints the same document with the same access checks, so the
 * PDF route is derived from the content URL a viewer already holds. A URL that
 * is not a content route -- a blob: URL, a signed content URL -- has no PDF
 * beside it.
 */
export function pdfURLFor(contentUrl: string | undefined): string | undefined {
  if (!contentUrl || contentUrl.startsWith("blob:") || contentUrl.startsWith("data:")) return undefined;
  const [path, query] = contentUrl.split("?", 2);
  if (!path || !path.endsWith("/content")) return undefined;
  const pdf = path.slice(0, -"/content".length) + "/pdf";
  return query ? `${pdf}?${query}` : pdf;
}

/**
 * The content URL of one version of the document at contentUrl, which every
 * asset content route serves beside itself at `.../versions/{n}/content`.
 */
export function versionContentURL(contentUrl: string, version: number | null | undefined): string {
  if (!version || !contentUrl.endsWith("/content")) return contentUrl;
  return `${contentUrl.slice(0, -"/content".length)}/versions/${version}/content`;
}

/** The file name a PDF response names, or document.pdf. */
export function pdfFileName(disposition: string | null): string {
  const match = disposition ? /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(disposition) : null;
  const raw = match?.[1];
  if (!raw) return "document.pdf";
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

/**
 * Fetches the PDF at url and saves it. A refusal is thrown as an Error whose
 * message is the server's own text, which says what to do (#1983).
 */
export async function downloadPdf(url: string): Promise<void> {
  const res = await fetch(url, { credentials: "same-origin" });
  if (!res.ok) {
    const text = (await res.text().catch(() => "")).trim();
    throw new Error(text && !text.startsWith("{") ? text : `The PDF could not be made (HTTP ${res.status}).`);
  }
  const blob = await res.blob();
  const href = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = href;
    a.download = pdfFileName(res.headers.get("Content-Disposition"));
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    // The click has handed the bytes to the browser's download; the URL is
    // released on the next turn so the download has started from it.
    setTimeout(() => URL.revokeObjectURL(href), 0);
  }
}
