import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "@/api/portal/client";
import { useCreateKnowledgePage } from "@/api/portal/hooks";

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

describe("KnowledgePageForm duplicate gate (#705)", () => {
  // A create mutation whose mutate() rejects with the 409 duplicate_blocked
  // payload, mirroring what the backend gate returns.
  function mockCreateRejectingWithDuplicate() {
    const mutate = vi.fn((_input: unknown, opts?: { onError?: (e: unknown) => void }) => {
      opts?.onError?.(
        new ApiError(409, "A similar knowledge page already exists.", {
          duplicate_blocked: true,
          candidates: [{ id: "kp_existing", slug: "return-policy", title: "Return Policy", score: 0.93 }],
          message: "A similar knowledge page already exists.",
        }),
      );
    });
    vi.mocked(useCreateKnowledgePage).mockReturnValue({ mutate, isPending: false } as unknown as ReturnType<
      typeof useCreateKnowledgePage
    >);
    return mutate;
  }

  it("surfaces candidate pages when create is blocked as a near-duplicate", () => {
    mockCreateRejectingWithDuplicate();
    render(<KnowledgePageForm onDone={() => {}} />, { wrapper });

    fireEvent.change(screen.getByLabelText(/Title/i), { target: { value: "ACME Returns Policy" } });
    fireEvent.click(screen.getByRole("button", { name: /Create page/i }));

    expect(screen.getByText(/Similar pages already exist/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Return Policy/i })).toBeInTheDocument();
    expect(screen.getByText(/93% match/i)).toBeInTheDocument();
  });

  it("resubmits with force_new when the user chooses to create anyway", () => {
    const mutate = mockCreateRejectingWithDuplicate();
    render(<KnowledgePageForm onDone={() => {}} />, { wrapper });

    fireEvent.change(screen.getByLabelText(/Title/i), { target: { value: "ACME Returns Policy" } });
    fireEvent.click(screen.getByRole("button", { name: /Create page/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create separate page anyway/i }));

    // The second create call carries force_new: true.
    expect(mutate).toHaveBeenCalledTimes(2);
    const secondCall = mutate.mock.calls[1];
    const lastInput = (secondCall?.[0] ?? {}) as { force_new?: boolean };
    expect(lastInput.force_new).toBe(true);
  });

  it("navigates to a candidate page when one is opened", () => {
    mockCreateRejectingWithDuplicate();
    const onDone = vi.fn();
    render(<KnowledgePageForm onDone={onDone} />, { wrapper });

    fireEvent.change(screen.getByLabelText(/Title/i), { target: { value: "ACME Returns Policy" } });
    fireEvent.click(screen.getByRole("button", { name: /Create page/i }));
    fireEvent.click(screen.getByRole("button", { name: /Return Policy/i }));

    expect(onDone).toHaveBeenCalledWith("kp_existing");
  });
});
