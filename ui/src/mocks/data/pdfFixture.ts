/**
 * A real PDF for the mock library, carrying the action that #1783 is about.
 *
 * The fixture resources declare `application/pdf` but the content route used to
 * answer them with the string "binary contents of <name>", which no viewer can
 * render. That was survivable while a PDF was handed to the browser's plugin
 * and nothing was asserted about it. It is not survivable now: the viewer is
 * ours (components/renderers/PdfRenderer.tsx), and the one thing worth proving
 * about it is that a document cannot act on the reader.
 *
 * So this builds an actual file, and builds it hostile on purpose:
 *
 *   /OpenAction << /S /Named /N /Print >>   print dialog, the moment it opens
 *   /ViewerPreferences ... HideToolbar      and hide the viewer's own controls
 *
 * That is the PDF standard's named action for "open the print dialog". It is
 * not JavaScript, which is why it survived every other defence: Chrome ignores
 * a PDF's embedded JavaScript and honours its named actions. Opened through the
 * old `<object>` embed this file raises the print dialog; opened through PDF.js
 * it does not, and that difference is what the e2e case asserts.
 *
 * Two pages, with text, so paging and the text layer are exercised by the same
 * fixture rather than by a second one.
 */

/** The phrase the find case searches for; on page one only. */
export const PDF_FIXTURE_NEEDLE = "predicate pushdown";

/** Page count, asserted by the paging case. */
export const PDF_FIXTURE_PAGES = 2;

const PAGE_TEXT: readonly (readonly string[])[] = [
  ["Query Playbook", "Partitioning strategies and " + PDF_FIXTURE_NEEDLE + ".", "Page one of two."],
  ["Appendix", "Rollback planning and impact assessment.", "Page two of two."],
];

/** One page's content stream: Helvetica, three lines down the page. */
function contentStream(lines: readonly string[]): string {
  const body = lines
    .map((line, i) => `BT /F1 ${i === 0 ? 24 : 12} Tf 72 ${700 - i * 36} Td (${escapeText(line)}) Tj ET`)
    .join("\n");
  return `${body}\n`;
}

/** Escapes the three characters that are syntax inside a PDF string literal. */
function escapeText(s: string): string {
  return s.replace(/([\\()])/g, "\\$1");
}

/**
 * Assembles the file, computing the cross-reference offsets from the bytes
 * actually written.
 *
 * The offsets are computed rather than written by hand because a wrong one is
 * not a visible defect: pdf.js silently reconstructs a broken xref table, so a
 * hand-maintained one would drift until the fixture stopped resembling a file
 * any other reader would accept.
 */
export function pdfFixtureBytes(): Uint8Array {
  const objects: string[] = [
    // 1: catalog, carrying the open action and the viewer preferences.
    "<< /Type /Catalog /Pages 2 0 R /OpenAction 8 0 R /ViewerPreferences 9 0 R >>",
    // 2: page tree.
    `<< /Type /Pages /Kids [3 0 R 4 0 R] /Count ${PDF_FIXTURE_PAGES} >>`,
    // 3, 4: the pages.
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 7 0 R >> >> /Contents 5 0 R >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 7 0 R >> >> /Contents 6 0 R >>",
    // 5, 6: their content streams.
    stream(contentStream(PAGE_TEXT[0] ?? [])),
    stream(contentStream(PAGE_TEXT[1] ?? [])),
    // 7: the one font.
    "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    // 8: "open the print dialog", the reason this fixture exists.
    "<< /S /Named /N /Print >>",
    // 9: and take the viewer's own controls away while it is at it.
    "<< /DisplayDocTitle true /HideMenubar true /HideWindowUI true /HideToolbar true >>",
  ];

  let file = "%PDF-1.7\n";
  const offsets: number[] = [];
  objects.forEach((body, i) => {
    offsets.push(file.length);
    file += `${i + 1} 0 obj\n${body}\nendobj\n`;
  });

  const startxref = file.length;
  // Entry zero is the head of the free list, and every entry is exactly 20
  // bytes: ten digits, a space, five digits, a space, the type, a space, a
  // newline. A reader seeks by multiplying, so a short line is a corrupt table.
  file += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  for (const offset of offsets) {
    file += `${String(offset).padStart(10, "0")} 00000 n \n`;
  }
  file += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${startxref}\n%%EOF\n`;

  // Latin-1 rather than UTF-8: every byte written above is ASCII, and the
  // offsets recorded in the xref table are byte offsets, so a multi-byte
  // encoding of any character would put every later offset out by the
  // difference.
  const bytes = new Uint8Array(file.length);
  for (let i = 0; i < file.length; i += 1) bytes[i] = file.charCodeAt(i) & 0xff;
  return bytes;
}

function stream(content: string): string {
  return `<< /Length ${content.length} >>\nstream\n${content}endstream`;
}
