import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";

const LIMIT = 32768;
const ADVISORY = 12288;

const h = vi.hoisted(() => ({
  baseline: {
    data: undefined as
      | { baseline: string; limit_bytes: number; advisory_bytes: number }
      | undefined,
    isLoading: false,
  },
  entryValue: "",
}));

// Override only the hooks the page consumes; keep everything else real.
vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useAgentInstructionsBaseline: () => h.baseline,
    useSystemInfo: () => ({ data: { config_mode: "database" } }),
    useEffectiveConfig: () => ({
      data: [{ key: "server.agent_instructions", value: h.entryValue, source: "database" }],
      error: undefined,
      refetch: vi.fn(),
    }),
    useSetConfigEntry: () => ({ mutate: vi.fn(), isPending: false }),
    useDeleteConfigEntry: () => ({ mutate: vi.fn(), isPending: false }),
  };
});

// The real editor is a CodeMirror instance; a textarea is enough to drive the
// value the size meter measures.
vi.mock("@/components/MarkdownEditor", () => ({
  MarkdownEditor: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (v: string) => void;
  }) => (
    <textarea
      data-testid="editor"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}));

import { ConfigEditorPage, PlatformBaselinePanel } from "./ConfigEditorPage";

afterEach(() => {
  cleanup();
  h.baseline = { data: undefined, isLoading: false };
  h.entryValue = "";
});

describe("PlatformBaselinePanel", () => {
  it("renders the baseline text when present", () => {
    h.baseline = {
      data: {
        baseline: "How to operate this platform:\n- Call `search` first.",
        limit_bytes: LIMIT,
        advisory_bytes: ADVISORY,
      },
      isLoading: false,
    };
    render(<PlatformBaselinePanel />);
    expect(screen.getByTestId("platform-baseline-panel")).toBeInTheDocument();
    expect(screen.getByText(/How to operate this platform/)).toBeInTheDocument();
    expect(screen.getByText(/Call `search` first/)).toBeInTheDocument();
  });

  it("renders nothing when the baseline is empty", () => {
    h.baseline = {
      data: { baseline: "", limit_bytes: LIMIT, advisory_bytes: ADVISORY },
      isLoading: false,
    };
    const { container } = render(<PlatformBaselinePanel />);
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByTestId("platform-baseline-panel")).not.toBeInTheDocument();
  });

  it("renders nothing while loading", () => {
    h.baseline = { data: undefined, isLoading: true };
    const { container } = render(<PlatformBaselinePanel />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when the baseline is only whitespace", () => {
    h.baseline = {
      data: { baseline: "   \n  ", limit_bytes: LIMIT, advisory_bytes: ADVISORY },
      isLoading: false,
    };
    const { container } = render(<PlatformBaselinePanel />);
    expect(container).toBeEmptyDOMElement();
  });
});

// The size of the customized instruction layer is otherwise invisible until a
// write is refused: every session on the deployment reads it in its first
// response (#1607).
describe("ConfigEditorPage size meter", () => {
  const withBounds = () => {
    h.baseline = {
      data: { baseline: "How to operate this platform:", limit_bytes: LIMIT, advisory_bytes: ADVISORY },
      isLoading: false,
    };
  };

  const renderInstructions = () =>
    render(
      <ConfigEditorPage
        configKey="server.agent_instructions"
        label="Agent Instructions"
        description="Guidance for AI agents using this platform"
        showPlatformBaseline
        sizeBounded
      />,
    );

  const setEditorValue = (value: string) => {
    fireEvent.change(screen.getByTestId("editor"), { target: { value } });
  };

  it("reports the size against the limit", () => {
    withBounds();
    h.entryValue = "one rule";
    renderInstructions();
    expect(screen.getByTestId("instruction-size-meter")).toHaveTextContent("8 / 32,768 bytes");
  });

  it("measures UTF-8 bytes, not characters", () => {
    withBounds();
    renderInstructions();
    // Four three-byte characters: the server measures twelve bytes.
    setEditorValue("日本語で");
    expect(screen.getByTestId("instruction-size-meter")).toHaveTextContent("12 / 32,768 bytes");
  });

  it("stays quiet below the advisory", () => {
    withBounds();
    h.entryValue = "one rule";
    renderInstructions();
    expect(screen.queryByText(/above the/)).not.toBeInTheDocument();
    expect(screen.queryByText(/cannot be saved/)).not.toBeInTheDocument();
  });

  it("advises above the advisory while leaving the save available", () => {
    withBounds();
    renderInstructions();
    setEditorValue("x".repeat(ADVISORY + 1));
    expect(screen.getByText(/above the/)).toBeInTheDocument();
    expect(screen.getByText(/knowledge page/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /save/i })).toBeEnabled();
  });

  it("refuses a save past the limit and says where the content belongs", () => {
    withBounds();
    renderInstructions();
    setEditorValue("x".repeat(LIMIT + 1));
    expect(screen.getByText(/cannot be saved/)).toBeInTheDocument();
    expect(screen.getByText(/mcp:knowledge_page:<slug>/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /save/i })).toBeDisabled();
  });

  it("shows no meter for a key that is not size-bounded", () => {
    withBounds();
    render(
      <ConfigEditorPage
        configKey="server.description"
        label="Description"
        description="Platform identity visible to MCP clients"
      />,
    );
    expect(screen.queryByTestId("instruction-size-meter")).not.toBeInTheDocument();
  });

  it("shows no meter until the bounds have loaded", () => {
    h.baseline = { data: undefined, isLoading: true };
    renderInstructions();
    expect(screen.queryByTestId("instruction-size-meter")).not.toBeInTheDocument();
  });
});
