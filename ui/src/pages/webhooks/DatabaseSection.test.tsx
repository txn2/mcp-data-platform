import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { DatabaseSection } from "./DatabaseSection";

afterEach(cleanup);

describe("DatabaseSection (#2408)", () => {
  it("shows the replica listening, what it filed and refused, and its last failure", () => {
    render(
      <DatabaseSection
        database={{ connection: "orders", mode: "outbox", table: "public.outbox", poll_seconds: 10 }}
        listener={{ replica: "platform-0", delivered: 12, rejected: 1, last_error: "connection reset" }}
      />,
    );
    expect(screen.getByRole("heading", { name: "Database" })).toBeInTheDocument();
    expect(screen.getByText("Listening on platform-0.")).toBeInTheDocument();
    expect(screen.getByText("public.outbox")).toBeInTheDocument();
    expect(screen.getByText("10 seconds")).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();
    expect(screen.getByText("connection reset")).toBeInTheDocument();
  });

  it("names a notify source's channels and says when no replica listens", () => {
    render(<DatabaseSection database={{ connection: "orders", mode: "notify", channels: ["order_events", "refunds"] }} />);
    expect(screen.getByText("No replica is listening.")).toBeInTheDocument();
    expect(screen.getByText("order_events, refunds")).toBeInTheDocument();
    expect(screen.getByText("None yet")).toBeInTheDocument();
  });

  it("renders nothing without a database", () => {
    const { container } = render(<DatabaseSection />);
    expect(container).toBeEmptyDOMElement();
  });
});
