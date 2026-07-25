import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  useDirectoryNames: vi.fn(),
}));

import { MentionText } from "./MentionText";
import { useDirectoryNames } from "@/api/portal/hooks";

const mockNames = vi.mocked(useDirectoryNames);

beforeEach(() => {
  mockNames.mockReturnValue({ "marcus.johnson@example.com": "Marcus Johnson" });
});

describe("MentionText", () => {
  // The body stores an address; the reader sees a name. Storing the name would
  // go stale, so the chip resolves it at render time.
  it("renders a delivered mention as a name chip", () => {
    render(
      <MentionText
        body="@marcus.johnson(example.com) please review"
        mentions={["marcus.johnson@example.com"]}
      />,
    );

    expect(screen.getByText("@Marcus Johnson")).toBeInTheDocument();
    expect(screen.getByText("@Marcus Johnson")).toHaveAttribute(
      "title",
      "marcus.johnson@example.com",
    );
    expect(screen.getByText(/please review/)).toBeInTheDocument();
  });

  it("falls back to the address when the directory does not know the person", () => {
    render(
      <MentionText
        body="@outside.contractor(example.com) fyi"
        mentions={["outside.contractor@example.com"]}
      />,
    );
    expect(screen.getByText("@outside.contractor@example.com")).toBeInTheDocument();
  });

  // A token naming someone outside the audience was never recorded and
  // notified nobody, so chipping it would tell every reader they were tagged.
  it("leaves an undelivered mention as plain text", () => {
    const { container } = render(
      <MentionText body="@outside.contractor(example.com) fyi" mentions={[]} />,
    );
    expect(container.querySelector(".text-primary")).toBeNull();
    expect(container.textContent).toContain("@outside.contractor(example.com) fyi");
  });

  it("renders a body with no mentions as plain text", () => {
    render(<MentionText body="looks good to me" />);
    expect(screen.getByText("looks good to me")).toBeInTheDocument();
  });
});
