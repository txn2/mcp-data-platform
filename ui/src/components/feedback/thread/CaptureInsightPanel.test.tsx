import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ThreadEvent } from "@/api/portal/types";

const mutate = vi.fn();
let state: { isPending: boolean; isError: boolean; error: Error | null };

vi.mock("@/api/portal/hooks", () => ({
  useCaptureThreadInsight: () => ({ mutate, ...state }),
}));

import { CaptureInsightPanel } from "./CaptureInsightPanel";

const events = (body: string) => [{ body } as ThreadEvent];
const longText = "a".repeat(4357);
const button = () => screen.getByRole("button", { name: /Capture as insight|Capturing/ });
const field = () => screen.getByLabelText("Insight text");

beforeEach(() => {
  mutate.mockClear();
  state = { isPending: false, isError: false, error: null };
});

describe("CaptureInsightPanel", () => {
  it("is a plain button when the default text fits, and sends no text", () => {
    render(<CaptureInsightPanel threadId="t1" events={events("a correction worth keeping")} loading={false} />);
    expect(screen.queryByLabelText("Insight text")).toBeNull();
    fireEvent.click(button());
    expect(mutate).toHaveBeenCalledWith({ threadId: "t1" });
  });

  it("holds the button until the events have loaded", () => {
    render(<CaptureInsightPanel threadId="t1" events={undefined} loading />);
    expect((button() as HTMLButtonElement).disabled).toBe(true);
  });

  it("offers the button when the events could not be read, leaving the answer to the server", () => {
    render(<CaptureInsightPanel threadId="t1" title="A title for it" events={undefined} loading={false} />);
    expect((button() as HTMLButtonElement).disabled).toBe(false);
  });

  it("prefills the field with the whole text over the limit, says why and blocks the capture", () => {
    render(<CaptureInsightPanel threadId="t1" events={events(longText)} loading={false} />);
    expect((field() as HTMLTextAreaElement).value).toBe(longText);
    expect(screen.getByText("4,357 of 4,000 bytes")).toBeTruthy();
    expect(screen.getByText("357 bytes over the limit. Shorten the text to capture this.")).toBeTruthy();
    expect((button() as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(button());
    expect(mutate).not.toHaveBeenCalled();
  });

  it("sends the shortened text and keeps the field while it does", () => {
    render(<CaptureInsightPanel threadId="t1" events={events(longText)} loading={false} />);
    fireEvent.change(field(), { target: { value: "the part worth keeping" } });
    expect(screen.getByText("The insight is created from this text.")).toBeTruthy();
    fireEvent.click(button());
    expect(mutate).toHaveBeenCalledWith({ threadId: "t1", content: "the part worth keeping" });
    expect(screen.getByLabelText("Insight text")).toBeTruthy();
  });

  it("blocks text under the minimum, so an empty field never means the default", () => {
    render(<CaptureInsightPanel threadId="t1" events={events(longText)} loading={false} />);
    fireEvent.change(field(), { target: { value: "" } });
    expect(screen.getByText("Write at least 10 bytes.")).toBeTruthy();
    expect((button() as HTMLButtonElement).disabled).toBe(true);
  });

  it("disables the field and the button while capturing", () => {
    state = { isPending: true, isError: false, error: null };
    render(<CaptureInsightPanel threadId="t1" events={events(longText.slice(0, 4000) + longText)} loading={false} />);
    expect((field() as HTMLTextAreaElement).disabled).toBe(true);
    expect(screen.getByRole("button", { name: "Capturing…" })).toBeTruthy();
  });

  it("shows the server's message for a refusal and keeps the text", () => {
    state = { isPending: false, isError: true, error: new Error("content must be at most 4000 bytes (got 4357)") };
    render(<CaptureInsightPanel threadId="t1" events={events(longText)} loading={false} />);
    expect(screen.getByRole("alert").textContent).toBe("content must be at most 4000 bytes (got 4357)");
    expect((field() as HTMLTextAreaElement).value).toBe(longText);
  });
});
