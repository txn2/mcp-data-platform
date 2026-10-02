import { describe, expect, it, vi, afterEach } from "vitest";
import { downloadPdf, pdfFileName, pdfURLFor, versionContentURL } from "./pdfExport";

afterEach(() => vi.restoreAllMocks());

describe("pdfURLFor", () => {
  it("is the route beside a content route", () => {
    expect(pdfURLFor("/api/v1/portal/assets/a1/content")).toBe("/api/v1/portal/assets/a1/pdf");
    expect(pdfURLFor("/api/v1/portal/assets/a1/versions/3/content")).toBe("/api/v1/portal/assets/a1/versions/3/pdf");
    expect(pdfURLFor("/portal/view/tok/items/a2/content?x=1")).toBe("/portal/view/tok/items/a2/pdf?x=1");
  });

  it("is nothing for a URL that is not a content route", () => {
    expect(pdfURLFor(undefined)).toBeUndefined();
    expect(pdfURLFor("blob:https://x/1")).toBeUndefined();
    expect(pdfURLFor("data:text/html,x")).toBeUndefined();
    expect(pdfURLFor("/api/v1/portal/content/signed-token")).toBeUndefined();
  });
});

describe("pdfFileName", () => {
  it("reads the name the response gives", () => {
    expect(pdfFileName('inline; filename="Q3 review.pdf"')).toBe("Q3 review.pdf");
    expect(pdfFileName("attachment; filename*=UTF-8''r%C3%A9sum%C3%A9.pdf")).toBe("résumé.pdf");
    expect(pdfFileName("inline; filename*=UTF-8''%E0%A4%A.pdf")).toBe("%E0%A4%A.pdf");
    expect(pdfFileName(null)).toBe("document.pdf");
    expect(pdfFileName("inline")).toBe("document.pdf");
  });
});

describe("downloadPdf", () => {
  it("throws the server's words, or the status when it sent JSON", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(new Response("Only an HTML document is exported to PDF.", { status: 415 }));
    await expect(downloadPdf("/x/pdf")).rejects.toThrow("Only an HTML document is exported to PDF.");
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(new Response('{"title":"authentication required"}', { status: 401 }));
    await expect(downloadPdf("/x/pdf")).rejects.toThrow("HTTP 401");
  });
});

describe("versionContentURL", () => {
  it("is the content route of the version shown", () => {
    expect(versionContentURL("/api/v1/portal/assets/a1/content", 2)).toBe("/api/v1/portal/assets/a1/versions/2/content");
    expect(versionContentURL("/api/v1/admin/assets/a1/content", 3)).toBe("/api/v1/admin/assets/a1/versions/3/content");
    expect(versionContentURL("/api/v1/portal/assets/a1/content", null)).toBe("/api/v1/portal/assets/a1/content");
    expect(versionContentURL("blob:x", 2)).toBe("blob:x");
  });
});
