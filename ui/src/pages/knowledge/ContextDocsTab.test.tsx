import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

vi.mock("@/api/portal/datahub", () => ({
  MIN_SEARCH_LEN: 2,
  useDocumentsBrowse: vi.fn(),
  useDocumentsSearch: vi.fn(),
  useDocument: vi.fn(),
  useCreateDocument: vi.fn(),
  useUpdateDocument: vi.fn(),
  useDeleteDocument: vi.fn(),
  documentId: (urn: string) => urn.replace(/^urn:li:document:/, ""),
}));
vi.mock("@/components/knowledge/DataHubConnectionSelect", () => ({
  DataHubConnectionSelect: () => null,
  useConnectionWritable: vi.fn(() => true),
}));
// CodeMirror does not render cleanly in jsdom; stand in a plain textarea.
vi.mock("@/components/MarkdownEditor", () => ({
  MarkdownEditor: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <textarea aria-label="content" value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));
vi.mock("@/components/renderers/MarkdownRenderer", () => ({
  MarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div>,
}));

let mockIsAdmin = true;
let mockTools: string[] = [];
vi.mock("@/stores/auth", () => ({
  useAuthStore: (sel: (s: unknown) => unknown) =>
    sel({ user: { tools: mockTools }, isAdmin: () => mockIsAdmin }),
}));

import { ContextDocsTab } from "./ContextDocsTab";
import {
  useDocumentsBrowse,
  useDocumentsSearch,
  useDocument,
  useCreateDocument,
  useUpdateDocument,
  useDeleteDocument,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";

const q = (data: unknown) => ({ data, isLoading: false, isError: false }) as never;
const noopMut = () => ({ mutate: vi.fn(), isPending: false, isError: false, error: null }) as never;

const doc1 = {
  urn: "urn:li:document:doc-1",
  title: "Refresh runbook",
  sub_type: "runbook",
  body: "# Refresh\n\nRuns at 06:00.",
  show_in_global_context: true,
};

beforeEach(() => {
  mockIsAdmin = true;
  mockTools = [];
  vi.mocked(useConnectionWritable).mockReturnValue(true);
  vi.mocked(useDocumentsBrowse).mockReturnValue(q({ documents: [doc1], total: 1 }));
  vi.mocked(useDocumentsSearch).mockReturnValue(q([]));
  vi.mocked(useDocument).mockReturnValue(q(doc1));
  [useCreateDocument, useUpdateDocument, useDeleteDocument].forEach((h) =>
    vi.mocked(h).mockImplementation(noopMut),
  );
});

describe("ContextDocsTab", () => {
  it("browses documents and opens one, rendering its markdown body", () => {
    render(<ContextDocsTab conn="primary" onConnChange={vi.fn()} />);
    expect(screen.getByText("Refresh runbook")).toBeInTheDocument();

    fireEvent.click(screen.getByText("Refresh runbook"));
    expect(screen.getByRole("heading", { name: "Refresh runbook" })).toBeInTheDocument();
    expect(screen.getByText(/Runs at 06:00/)).toBeInTheDocument();
  });

  it("shows create/edit/delete affordances for a writer", () => {
    render(<ContextDocsTab conn="primary" onConnChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: "New document" })).toBeInTheDocument();
    fireEvent.click(screen.getByText("Refresh runbook"));
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument();
  });

  it("rejects an unsupported entity type and disables create", () => {
    render(<ContextDocsTab conn="primary" onConnChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "New document" }));
    fireEvent.change(screen.getAllByRole("textbox")[0]!, { target: { value: "A note" } });
    fireEvent.change(screen.getByPlaceholderText(/urn:li:dataset/), {
      target: { value: "urn:li:dashboard:(looker,1)" },
    });
    expect(screen.getByText(/attach only to Dataset/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create document" })).toBeDisabled();
  });

  it("creates a document with a supported entity type", () => {
    const mutate = vi.fn();
    vi.mocked(useCreateDocument).mockReturnValue({ mutate, isPending: false, isError: false, error: null } as never);
    render(<ContextDocsTab conn="primary" onConnChange={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "New document" }));
    fireEvent.change(screen.getAllByRole("textbox")[0]!, { target: { value: "Schema notes" } });
    fireEvent.change(screen.getByPlaceholderText(/urn:li:dataset/), {
      target: { value: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create document" }));
    expect(mutate).toHaveBeenCalledWith(
      expect.objectContaining({ title: "Schema notes", entity_urn: "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)" }),
      expect.anything(),
    );
  });

  it("surfaces a failed delete instead of silently returning", () => {
    // The delete mutation now throws on a non-ok response, so isError drives a
    // visible message (previously dead code because apiFetchRaw never threw).
    vi.mocked(useDeleteDocument).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      isError: true,
      error: null,
    } as never);
    render(<ContextDocsTab conn="primary" onConnChange={vi.fn()} />);
    fireEvent.click(screen.getByText("Refresh runbook"));
    expect(screen.getByText("Delete failed.")).toBeInTheDocument();
  });

  it("hides write affordances when read-only", () => {
    vi.mocked(useConnectionWritable).mockReturnValue(false);
    render(<ContextDocsTab conn="primary" onConnChange={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "New document" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("Refresh runbook"));
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });
});
