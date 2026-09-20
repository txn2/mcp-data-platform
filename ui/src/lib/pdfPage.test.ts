import { describe, it, expect } from "vitest";
import { pdfFailureReason } from "./pdfPage";

// What a person reads in the thumbnail panel when a PDF has no tile (#1794).
// pdf.js names its own refusals; two of them read as a platform fault rather
// than as a property of the file, and are said plainly instead.
//
// drawFirstPage is not here: it needs a real 2d canvas context and a real
// worker, neither of which jsdom has. It is held by the Go integration suite,
// which draws a PDF in the renderer that takes the picture.
describe("pdfFailureReason", () => {
  it("says plainly that an encrypted document cannot be opened", () => {
    expect(pdfFailureReason({ name: "PasswordException", message: "No password given" })).toBe(
      "the document is password-protected",
    );
  });

  it("says a file that is not a PDF is not one", () => {
    expect(pdfFailureReason({ name: "InvalidPDFException", message: "Invalid PDF structure." })).toBe(
      "the file could not be read as a PDF",
    );
  });

  it("says a document whose bytes never arrived could not be loaded", () => {
    expect(pdfFailureReason({ name: "MissingPDFException", message: "Missing PDF" })).toBe(
      "the document could not be loaded",
    );
  });

  // Anything else is reported as pdf.js put it, which is more use than one
  // sentence written here that covers every remaining case equally badly.
  it("passes any other refusal through as pdf.js worded it", () => {
    expect(pdfFailureReason(new Error("the document has no pages"))).toBe("the document has no pages");
    expect(pdfFailureReason({ name: "XFAException", message: "XFA forms are not supported" })).toBe(
      "XFA forms are not supported",
    );
  });

  // A rejection that is not an Error at all still has to produce a sentence:
  // the renderer records whatever comes back, and an empty reason would read
  // as a tile that was drawn.
  it("makes a sentence out of a rejection that carries no message", () => {
    expect(pdfFailureReason("worker died")).toBe("worker died");
    expect(pdfFailureReason(undefined)).toBe("undefined");
    expect(pdfFailureReason({ name: "PasswordException" })).toBe("the document is password-protected");
  });
});
