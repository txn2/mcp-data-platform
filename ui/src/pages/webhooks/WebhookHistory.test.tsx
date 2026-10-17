import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { mockWebhookOverview, mockWebhookSources } from "@/mocks/data/webhooks";
import { WebhookHistory } from "./WebhookHistory";

describe("WebhookHistory (#2150)", () => {
  it("says plainly that an answer from in front of the platform is not counted", () => {
    render(<WebhookHistory data={mockWebhookOverview(mockWebhookSources, "day")} source="" isLoading={false} />);
    expect(screen.getByTestId("not-counted-note")).toHaveTextContent("502 or 504");
    expect(screen.getByTestId("not-counted-note")).toHaveTextContent("not counted");
    for (const title of ["Responses by status", "Received vs written", "Spool depth", "Acknowledgement", "Compaction", "Window size", "Storage by source"]) {
      expect(screen.getByText(title)).toBeInTheDocument();
    }
  });

  it("warns when the largest window takes half the memory limit", () => {
    const data = mockWebhookOverview(mockWebhookSources, "day");
    render(<WebhookHistory data={data} source="" isLoading={false} />);
    expect(screen.getByTestId("window-sizing")).toHaveTextContent("of the memory limit");
    expect(screen.getByRole("alert")).toHaveTextContent("Shorten the source's compaction window");
  });

  it("says what is missing rather than drawing empty charts", () => {
    const data = mockWebhookOverview([], "hour");
    render(<WebhookHistory data={data} source="" isLoading={false} />);
    expect(screen.getByText("No requests in this range.")).toBeInTheDocument();
    expect(screen.getAllByText("No window was compacted in this range.")).toHaveLength(2);
    expect(screen.getByText("No source holds anything in storage.")).toBeInTheDocument();
  });

  it("shows a skeleton while the overview loads", () => {
    render(<WebhookHistory data={undefined} source="" isLoading />);
    expect(screen.getByText("History")).toBeInTheDocument();
  });

  it("sets the window against no limit when the replica has none", () => {
    const data = mockWebhookOverview(mockWebhookSources, "day");
    data.history.sizing = { ...data.history.sizing, memory_limit_bytes: 0, warning: false };
    render(<WebhookHistory data={data} source="email-events" isLoading={false} />);
    expect(screen.getByText("This replica has no memory limit to set it against.")).toBeInTheDocument();
  });
});
