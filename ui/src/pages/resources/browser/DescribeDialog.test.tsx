import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { DescribeDialog } from "./ActionDialogs";

afterEach(cleanup);

describe("the describe dialog (#2111)", () => {
  it("opens on the folder's description and saves the edit", async () => {
    const onSave = vi.fn(async () => {});
    const onClose = vi.fn();
    render(<DescribeDialog folder="pipelines/audience" load={async () => "old"} onClose={onClose} onSave={onSave} />);
    const box = await screen.findByDisplayValue("old");
    fireEvent.change(box, { target: { value: "  Daily audience archive  " } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith("Daily audience archive"));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("says when the description could not be read", async () => {
    render(<DescribeDialog folder="x" load={async () => { throw new Error("no folder at that path"); }}
      onClose={() => {}} onSave={async () => {}} />);
    expect(await screen.findByText("no folder at that path")).toBeTruthy();
  });

  it("reports a save that failed and stays open", async () => {
    const onClose = vi.fn();
    render(<DescribeDialog folder="x" load={async () => ""} onClose={onClose}
      onSave={async () => { throw new Error("you may not change folders in that library"); }} />);
    await screen.findByDisplayValue("");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("you may not change folders in that library")).toBeTruthy();
    expect(onClose).not.toHaveBeenCalled();
  });
});
