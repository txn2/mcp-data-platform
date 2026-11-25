import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ToolDetail } from "@/api/admin/types";
import { ActivityTab } from "./ActivityTab";

const detail = (hold_count: number) =>
  ({
    name: "manage_site",
    description: "",
    toolkit_kind: "site",
    activity: { window_seconds: 7 * 86400, call_count: 152, success_rate: 1, avg_duration_ms: 1900, hold_count },
  }) as unknown as ToolDetail;

describe("ActivityTab holds (#2819)", () => {
  it("counts holds apart from the success rate and says why", () => {
    render(<ActivityTab detail={detail(29)} />);
    expect(screen.getByText("Holds")).toBeInTheDocument();
    expect(screen.getByText("29")).toBeInTheDocument();
    expect(screen.getByText("of 123 calls that finished")).toBeInTheDocument();
    expect(screen.getByText("Why holds are counted apart")).toBeInTheDocument();
  });

  it("leaves the explanation off a tool with no holds", () => {
    render(<ActivityTab detail={detail(0)} />);
    expect(screen.queryByText("Why holds are counted apart")).toBeNull();
    expect(screen.getByText("of 152 calls that finished")).toBeInTheDocument();
  });
});
