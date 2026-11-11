import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { GeneralFeedbackView } from "@/api/admin/types";

const save = vi.fn();
let view: GeneralFeedbackView;

vi.mock("@/api/admin/hooks", () => ({
  useGeneralFeedback: () => ({ data: view, isLoading: false, error: null, refetch: vi.fn() }),
  useSetGeneralFeedback: () => ({ mutate: save, isPending: false }),
  useNotificationChannels: () => ({ data: { channels: [{ name: "data-team", enabled: true }] }, isLoading: false }),
}));

import { GeneralFeedbackCard } from "./GeneralFeedbackCard";

const base: GeneralFeedbackView = {
  personas: ["admin"],
  recipients: [],
  channels: [],
  acknowledge_author: false,
  people: ["admin@example.com", "ops@example.com"],
  persona_options: [
    { name: "admin", display_name: "Administrator", people: 2 },
    { name: "analyst", display_name: "Data Analyst", people: 24 },
  ],
  warnings: [],
};

describe("GeneralFeedbackCard (#2603)", () => {
  beforeEach(() => {
    save.mockReset();
    view = { ...base };
  });

  it("says how many people a new post reaches and lists them on request", () => {
    render(<GeneralFeedbackCard isReadOnly={false} />);
    expect(screen.getByText("General feedback")).toBeInTheDocument();
    expect(screen.getByText("Default")).toBeInTheDocument();
    expect(screen.getByText("2 people", { selector: "strong" })).toBeInTheDocument();
    expect(screen.queryByText("ops@example.com")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show them" }));
    expect(screen.getByText("ops@example.com")).toBeInTheDocument();
  });

  it("saves the personas, addresses, channels and acknowledgement it is given", () => {
    render(<GeneralFeedbackCard isReadOnly={false} />);
    fireEvent.click(screen.getByRole("checkbox", { name: /Data Analyst/ }));
    fireEvent.change(screen.getByLabelText("Add recipient"), { target: { value: "requests@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: /Add/ }));
    fireEvent.click(screen.getByRole("checkbox", { name: "Post to data-team" }));
    fireEvent.click(screen.getByRole("checkbox", { name: /Tell the author it was received/ }));
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    expect(save).toHaveBeenCalledWith(
      {
        personas: ["admin", "analyst"],
        recipients: ["requests@example.com"],
        channels: ["data-team"],
        acknowledge_author: true,
      },
      expect.anything(),
    );
  });

  it("warns when the saved list reaches nobody, naming the persona with no people", () => {
    view = {
      ...base,
      people: [],
      persona_options: [{ name: "admin", display_name: "Administrator", people: 0 }],
      warnings: ["reaches_nobody"],
      updated_by: "admin@example.com",
      updated_at: "2026-11-10T12:00:00Z",
    };
    render(<GeneralFeedbackCard isReadOnly={false} />);
    expect(screen.getByText("A new post reaches nobody.")).toBeInTheDocument();
    expect(screen.getByText(/Administrator has no people with an address/)).toBeInTheDocument();
    expect(screen.queryByText("Show them")).toBeNull();
    expect(screen.queryByText("Default")).toBeNull();
  });

  it("is read-only when the settings cannot be written", () => {
    render(<GeneralFeedbackCard isReadOnly />);
    expect(screen.getByRole("checkbox", { name: /Data Analyst/ })).toBeDisabled();
    expect(screen.queryByRole("button", { name: /Save/ })).toBeNull();
  });
});
