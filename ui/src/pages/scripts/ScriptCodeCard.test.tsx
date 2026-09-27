import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ScriptCodeCard } from "./ScriptCodeCard";

afterEach(cleanup);

function renderCard() {
  render(
    <ScriptCodeCard
      action={<button type="button">Save</button>}
      flow={(link) => (
        <div>
          <span data-testid="flow-selected">{link.selectedLines ? `${link.selectedLines.from}-${link.selectedLines.to}` : "none"}</span>
          <button type="button" onClick={() => link.showLines([7, 8])}>
            open card
          </button>
        </div>
      )}
      source={(link) => (
        <div>
          <span data-testid="marked">{link.markedLines.join(",")}</span>
          <input aria-label="draft" defaultValue="" />
          <button type="button" onClick={() => link.onSelectLines({ from: 3, to: 4 })}>
            select lines
          </button>
        </div>
      )}
    />,
  );
}

describe("ScriptCodeCard", () => {
  it("opens on Flow, with the Source controls only on Source", () => {
    renderCard();
    expect(screen.getByRole("tab", { name: "Flow" })).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Source" }));
    expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
  });

  it("opens a card's lines on Source, and hands lines selected there back to Flow", () => {
    renderCard();
    fireEvent.click(screen.getByRole("button", { name: "open card" }));
    expect(screen.getByRole("tab", { name: "Source" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByTestId("marked")).toHaveTextContent("7,8");

    fireEvent.click(screen.getByRole("button", { name: "select lines" }));
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Flow" }));
    expect(screen.getByTestId("flow-selected")).toHaveTextContent("3-4");
  });

  it("keeps an unsaved edit when the reader looks at Flow", () => {
    renderCard();
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Source" }));
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "edited" } });
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Flow" }));
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Source" }));
    expect(screen.getByLabelText("draft")).toHaveValue("edited");
  });
});
