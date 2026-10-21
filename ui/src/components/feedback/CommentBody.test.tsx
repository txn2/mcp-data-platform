import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  useDirectoryNames: vi.fn(),
}));

import { CommentBody } from "./CommentBody";
import { useDirectoryNames } from "@/api/portal/hooks";

const mockNames = vi.mocked(useDirectoryNames);

beforeEach(() => {
  mockNames.mockReturnValue({ "marcus.johnson@example.com": "Marcus Johnson" });
});

describe("CommentBody mentions", () => {
  // The body stores an address; the reader sees a name. Storing the name would
  // go stale, so the chip resolves it at render time.
  it("renders a delivered mention as a name chip", () => {
    render(
      <CommentBody
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
      <CommentBody
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
      <CommentBody body="@outside.contractor(example.com) fyi" mentions={[]} />,
    );
    expect(container.querySelector(".text-primary")).toBeNull();
    expect(container.textContent).toContain("@outside.contractor(example.com) fyi");
  });

  it("renders a body with no mentions as plain text", () => {
    render(<CommentBody body="looks good to me" />);
    expect(screen.getByText("looks good to me")).toBeInTheDocument();
  });
});

describe("CommentBody Markdown (#2214)", () => {
  it("renders emphasis, code, lists, headings and tables rather than their syntax", () => {
    const { container } = render(
      <CommentBody body={"## Changed\n\n**Fixed** the `join`.\n\n- one\n- two\n\n| a | b |\n| - | - |\n| 1 | 2 |"} />,
    );
    expect(container.querySelector("h2")).toHaveTextContent("Changed");
    expect(container.querySelector("strong")).toHaveTextContent("Fixed");
    expect(container.querySelector("code")).toHaveTextContent("join");
    expect(container.querySelectorAll("li")).toHaveLength(2);
    expect(container.querySelector("table")).not.toBeNull();
    expect(container.textContent).not.toContain("**");
  });

  it("keeps a plain-text body's single line breaks", () => {
    const { container } = render(<CommentBody body={"first line\nsecond line"} />);
    expect(container.querySelectorAll("br")).toHaveLength(1);
    expect(container.textContent).toContain("first line");
    expect(container.textContent).toContain("second line");
  });

  it("drops raw HTML and does not follow a script link", () => {
    const { container } = render(
      <CommentBody body={'<img src=x onerror="alert(1)"> [click](javascript:alert(1)) [ok](https://example.com)'} />,
    );
    expect(container.querySelector("img")).toBeNull();
    const links = [...container.querySelectorAll("a")];
    expect(links.some((a) => (a.getAttribute("href") ?? "").startsWith("javascript:"))).toBe(false);
    const ok = links.find((a) => a.textContent === "ok");
    expect(ok).toHaveAttribute("href", "https://example.com");
    expect(ok).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("leaves a mention inside code as written", () => {
    const { container } = render(
      <CommentBody body="`@marcus.johnson(example.com)`" mentions={["marcus.johnson@example.com"]} />,
    );
    expect(container.querySelector(".text-primary")).toBeNull();
    expect(container.querySelector("code")).toHaveTextContent("@marcus.johnson(example.com)");
  });

  it("chips a delivered mention inside emphasis", () => {
    render(<CommentBody body="**ping @marcus.johnson(example.com)**" mentions={["marcus.johnson@example.com"]} />);
    expect(screen.getByText("@Marcus Johnson")).toBeInTheDocument();
  });
});
