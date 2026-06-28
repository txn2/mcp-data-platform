import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// The form only calls useKnowledgePage / useCreateKnowledgePage /
// useUpdateKnowledgePage; the rest are stubbed because the page module imports
// them at load time. Idle defaults so the form renders without a live query.
vi.mock("@/api/portal/hooks", () => ({
  useKnowledgePages: vi.fn(() => ({ data: undefined, isLoading: false })),
  useSearchKnowledgePages: vi.fn(() => ({ data: undefined, isLoading: false })),
  useKnowledgePage: vi.fn(() => ({ data: undefined, isLoading: false, isError: false })),
  useResolveRefs: vi.fn(() => ({ data: undefined })),
  useKnowledgePageVersions: vi.fn(() => ({ data: undefined })),
  useCreateKnowledgePage: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useUpdateKnowledgePage: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useDeleteKnowledgePage: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useThreadCounts: vi.fn(() => ({ data: {} })),
  MIN_SEARCH_LEN: 3,
}));

// The markdown editor pulls heavy deps that are irrelevant to the field markup.
vi.mock("@/components/MarkdownEditor", () => ({
  MarkdownEditor: () => <div data-testid="markdown-editor" />,
}));

import { KnowledgePageForm } from "./KnowledgePagesPage";

function wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

describe("KnowledgePageForm fields (#708)", () => {
  it("shows a persistent label for title, summary, and tags", () => {
    render(<KnowledgePageForm onDone={() => {}} />, { wrapper });
    // Labels are wired to inputs by htmlFor/id, so getByLabelText resolves the
    // field by its visible label rather than a placeholder that vanishes once
    // the field is populated.
    expect(screen.getByLabelText(/Title/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Summary/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Tags/i)).toBeInTheDocument();
  });

  it("renders the summary as a multi-line textarea", () => {
    render(<KnowledgePageForm onDone={() => {}} />, { wrapper });
    const summary = screen.getByLabelText(/Summary/i);
    expect(summary.tagName).toBe("TEXTAREA");
    expect(Number(summary.getAttribute("rows"))).toBeGreaterThanOrEqual(2);
  });

  it("keeps the title as a single-line input", () => {
    render(<KnowledgePageForm onDone={() => {}} />, { wrapper });
    const title = screen.getByLabelText(/Title/i);
    expect(title.tagName).toBe("INPUT");
  });
});
