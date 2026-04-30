import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, act } from "@testing-library/react";
import { useState } from "react";
import { useTryItSession, type TryItSession } from "./useTryItSession";

// Stub the inspector store so the hook's replay-intent effect doesn't
// fire under any of these test cases — we're proving lifecycle, not
// replay handoff.
vi.mock("@/stores/inspector", () => ({
  useInspectorStore: vi.fn(() => null),
}));

// Test parent: holds the session AND a child-mount flag. The session
// hook lives at this level (mirroring ToolDetail's role). The child
// component that receives the session is conditionally rendered, so
// toggling the flag mimics the user clicking away from / back to the
// Try It tab. If the hook were called inside the child, state would
// reset every time the flag flips back to true. With the hook here,
// state must persist across remounts.
function Parent({ toolName }: { toolName: string }) {
  const session = useTryItSession(toolName);
  const [childMounted, setChildMounted] = useState(true);
  return (
    <div>
      <button
        type="button"
        data-testid="toggle-child"
        onClick={() => setChildMounted((v) => !v)}
      >
        toggle
      </button>
      <span data-testid="history-count-from-parent">
        {session.history.length}
      </span>
      {childMounted && <Child session={session} />}
    </div>
  );
}

function Child({ session }: { session: TryItSession }) {
  return (
    <div>
      <span data-testid="history-count-from-child">
        {session.history.length}
      </span>
      <button
        type="button"
        data-testid="add"
        onClick={() =>
          session.addHistoryEntry({
            id: `id-${session.history.length + 1}`,
            timestamp: "2026-04-30T00:00:00Z",
            parameters: { x: 1 },
            response: null,
            is_loading: true,
          })
        }
      >
        add
      </button>
      <button
        type="button"
        data-testid="clear"
        onClick={() => session.clearHistory()}
      >
        clear
      </button>
    </div>
  );
}

describe("useTryItSession state survival across tab-switch unmount/remount (#343 bug 3)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("preserves history across child unmount/remount when hook is mounted at parent level", () => {
    const { getByTestId, queryByTestId } = render(<Parent toolName="t1" />);

    // Add three entries via the child.
    act(() => {
      getByTestId("add").click();
      getByTestId("add").click();
      getByTestId("add").click();
    });
    expect(getByTestId("history-count-from-parent").textContent).toBe("3");
    expect(getByTestId("history-count-from-child").textContent).toBe("3");

    // Unmount the child (simulates tab switch away from Try It).
    act(() => {
      getByTestId("toggle-child").click();
    });
    expect(queryByTestId("history-count-from-child")).toBeNull();
    // Parent's view of the session is still the same — state lives here.
    expect(getByTestId("history-count-from-parent").textContent).toBe("3");

    // Remount the child (simulates tab switch back to Try It). Because
    // the hook lives in the parent, this is a fresh Child instance
    // receiving the EXISTING session; history must still show 3.
    // Without the fix in #343, history would be 0 here.
    act(() => {
      getByTestId("toggle-child").click();
    });
    expect(getByTestId("history-count-from-child").textContent).toBe(
      "3",
      // Failure here means the fix has regressed — the hook moved back
      // into the child component, or the parent stopped holding it.
    );
  });

  it("resets history when toolName changes (selecting a different tool)", () => {
    const { getByTestId, rerender } = render(<Parent toolName="t1" />);

    act(() => {
      getByTestId("add").click();
      getByTestId("add").click();
    });
    expect(getByTestId("history-count-from-child").textContent).toBe("2");

    // Selecting a different tool from the list re-mounts the parent
    // with a different toolName — useEffect on [toolName] resets
    // session state.
    rerender(<Parent toolName="t2" />);
    expect(getByTestId("history-count-from-child").textContent).toBe("0");
  });

  it("clearHistory empties the list without affecting other state", () => {
    const { getByTestId } = render(<Parent toolName="t1" />);

    act(() => {
      getByTestId("add").click();
      getByTestId("add").click();
      getByTestId("clear").click();
    });
    expect(getByTestId("history-count-from-child").textContent).toBe("0");
  });
});
