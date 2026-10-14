import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { mockCatalogDocuments } from "@/mocks/data/catalogScan";

const h = vi.hoisted(() => ({
  docs: { data: undefined as unknown, isLoading: false, isError: false },
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return { ...actual, useCatalogDocuments: () => h.docs };
});

import { catalogDocumentHref } from "@/api/admin/hooks";
import { DocumentsTab } from "./DocumentsTab";

beforeEach(() => {
  h.docs = { data: { documents: mockCatalogDocuments }, isLoading: false, isError: false };
});

describe("DocumentsTab", () => {
  it("lists each connection's schema document with a download link", () => {
    render(<DocumentsTab />);
    expect(screen.getByText("billing")).toBeInTheDocument();
    expect(screen.getByText("schema.graphql")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
    const links = screen.getAllByRole("link", { name: /Download/ });
    expect(links[0]).toHaveAttribute("href", "/api/v1/admin/catalog/documents/api/billing/openapi.json");
    expect(links[0]).toHaveAttribute("download", "openapi.json");
  });

  it("says when no connection has a document yet", () => {
    h.docs = { data: { documents: [] }, isLoading: false, isError: false };
    render(<DocumentsTab />);
    expect(screen.getByText(/No API or GraphQL connection has been scanned yet/)).toBeInTheDocument();
  });

  it("says when the documents cannot be read", () => {
    h.docs = { data: undefined, isLoading: false, isError: true };
    render(<DocumentsTab />);
    expect(screen.getByText("The schema documents could not be read.")).toBeInTheDocument();
  });

  it("escapes each part of the download address", () => {
    expect(catalogDocumentHref({ connection_kind: "api", connection: "a b", filename: "x/y.json" })).toBe(
      "/api/v1/admin/catalog/documents/api/a%20b/x%2Fy.json",
    );
  });
});
