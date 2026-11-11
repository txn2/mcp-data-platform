import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";

let reach: { reaches_someone: boolean } | undefined;
vi.mock("@/api/portal/hooks", () => ({ useGeneralFeedbackReach: () => ({ data: reach }) }));

import { GeneralFeedbackNotice } from "./GeneralFeedbackNotice";

describe("GeneralFeedbackNotice (#2603)", () => {
  it("says a post is sent to the people who handle feedback when anyone is told", () => {
    reach = { reaches_someone: true };
    render(<GeneralFeedbackNotice />);
    expect(screen.getByText("It is sent to the people who handle feedback.")).toBeInTheDocument();
  });

  it("never claims a post reached someone when nobody is told", () => {
    reach = { reaches_someone: false };
    render(<GeneralFeedbackNotice />);
    expect(screen.getByText(/Nobody is told about new posts here yet/)).toBeInTheDocument();
    expect(screen.queryByText(/sent to the people who handle feedback/)).toBeNull();
  });

  it("says only where the post is visible until it knows", () => {
    reach = undefined;
    render(<GeneralFeedbackNotice />);
    expect(screen.getByTestId("general-feedback-notice").textContent?.trim()).toBe(
      "Posting to the General channel, visible to everyone on the platform.",
    );
  });
});
