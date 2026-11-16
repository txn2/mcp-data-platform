import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { mockWebhookOverview, mockWebhookSources } from "@/mocks/data/webhooks";
import { AckTooltip, WebhookHistory } from "./WebhookHistory";

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

  it("names the slowest, the bucket p99 was estimated in, and the stages' averages (#2651)", () => {
    const row = { at: "2026-09-29T10:32:00Z", max: 2410, p99: 1180, p50: 85, count: 1102, put: 64, mark: 3, buffer: 2 };
    render(<AckTooltip active payload={[{ payload: row }]} step={60} edges={[500, 1000, 1500, 2000]} />);
    const tip = screen.getByTestId("ack-tooltip");
    expect(tip).toHaveTextContent("max 2,410 ms");
    expect(tip).toHaveTextContent("p99 1,180 ms (estimated within 1,000 to 1,500 ms)");
    expect(tip).toHaveTextContent("put 64 ms, mark 3 ms, buffer 2 ms (averages)");
    expect(tip).toHaveTextContent("1,102 deliveries");
  });

  it("says a line between edges is an estimate, never a timeout", () => {
    render(<WebhookHistory data={mockWebhookOverview(mockWebhookSources, "day")} source="" isLoading={false} />);
    expect(screen.getByText(/never a timeout/)).toBeInTheDocument();
  });
});
