import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { useDeleteKnowledgePage } from "@/api/portal/hooks";
import { KnowledgePageDetail } from "./KnowledgePageDetail";

const PAGE = {
  id: "kp_1",
  title: "Fiscal Calendar",
  summary: "How the company defines fiscal quarters.",
  body: "# Fiscal Calendar",
  tags: ["finance"],
  current_version: 2,
  updated_by: "sarah.chen@example.com",
  updated_at: "2026-06-10T05:00:00Z",
};

vi.mock("@/api/portal/hooks", () => ({
  useKnowledgePage: vi.fn(() => ({ data: PAGE, isLoading: false, isError: false })),
  useResolveRefs: vi.fn(() => ({ data: undefined })),
  useKnowledgePageVersions: vi.fn(() => ({ data: undefined })),
  useDeleteKnowledgePage: vi.fn(() => ({ mutate: vi.fn(), isPending: false, isError: false })),
}));

// The panels below the article each read their own endpoint and are covered by
// their own tests; this one is about the page's own actions.
vi.mock("@/components/knowledge/RelatedPanel", () => ({ RelatedPanel: () => null }));
vi.mock("@/components/knowledge/LineagePanel", () => ({ LineagePanel: () => null }));
vi.mock("@/components/knowledge/RefPicker", () => ({ RefPicker: () => null }));
vi.mock("@/components/knowledge/KnowledgeBacklinks", () => ({ KnowledgeBacklinks: () => null }));
vi.mock("@/components/feedback/FeedbackButton", () => ({ FeedbackButton: () => null }));
vi.mock("@/components/renderers/MarkdownRenderer", () => ({
  MarkdownRenderer: ({ content }: { content: string }) => <div>{content}</div>,
}));

function renderDetail(onDeleted = vi.fn()) {
  render(
    <KnowledgePageDetail
      id="kp_1"
      canEdit
      onBack={vi.fn()}
      onEdit={vi.fn()}
      onDeleted={onDeleted}
    />,
  );
  return onDeleted;
}

describe("KnowledgePageDetail remove", () => {
  beforeEach(() => vi.clearAllMocks());

  // Remove used to run on window.confirm, which jsdom stubs to false and no
  // reader can style or read back. It is a real dialog now, so the delete must
  // wait for it rather than firing on the button itself.
  it("asks before deleting rather than deleting on the button", () => {
    const mutate = vi.fn();
    vi.mocked(useDeleteKnowledgePage).mockReturnValue({
      mutate,
      isPending: false,
      isError: false,
    } as unknown as ReturnType<typeof useDeleteKnowledgePage>);
    renderDetail();

    fireEvent.click(screen.getByRole("button", { name: /Remove/ }));

    expect(screen.getByText(/Remove "Fiscal Calendar"\?/)).toBeInTheDocument();
    expect(mutate).not.toHaveBeenCalled();
  });

  it("deletes the page once the dialog is confirmed", () => {
    const mutate = vi.fn();
    vi.mocked(useDeleteKnowledgePage).mockReturnValue({
      mutate,
      isPending: false,
      isError: false,
    } as unknown as ReturnType<typeof useDeleteKnowledgePage>);
    renderDetail();

    fireEvent.click(screen.getByRole("button", { name: /Remove/ }));
    // The dialog's own confirm, not the page action that opened it.
    const dialog = within(screen.getByRole("dialog"));
    fireEvent.click(dialog.getByRole("button", { name: "Remove" }));

    expect(mutate).toHaveBeenCalledWith("kp_1", expect.anything());
  });

  // The dialog stays open on failure, so it has to say what went wrong;
  // otherwise the confirm button reads as doing nothing at all.
  it("states why a failed remove did not happen", () => {
    vi.mocked(useDeleteKnowledgePage).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      isError: true,
      error: new Error("page is referenced by a changeset"),
    } as unknown as ReturnType<typeof useDeleteKnowledgePage>);
    renderDetail();

    fireEvent.click(screen.getByRole("button", { name: /Remove/ }));

    expect(screen.getByText(/page is referenced by a changeset/)).toBeInTheDocument();
  });
});

describe("KnowledgePageDetail reading", () => {
  beforeEach(() => vi.clearAllMocks());

  it("names the page, its version line, and its tags", () => {
    renderDetail();

    expect(screen.getByRole("heading", { name: "Fiscal Calendar" })).toBeInTheDocument();
    expect(screen.getByText(/v2 · last edited by sarah\.chen@example\.com/)).toBeInTheDocument();
    expect(screen.getByText("finance")).toBeInTheDocument();
  });

  it("offers no edit controls to a reader who cannot edit", () => {
    render(
      <KnowledgePageDetail
        id="kp_1"
        canEdit={false}
        onBack={vi.fn()}
        onEdit={vi.fn()}
        onDeleted={vi.fn()}
      />,
    );

    expect(screen.queryByRole("button", { name: /Remove/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /History/ })).not.toBeInTheDocument();
  });
});
