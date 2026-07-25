import { useState } from "react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  useMentionCandidates: vi.fn(),
  useMentionEligibility: vi.fn(() => ({})),
}));

vi.mock("@/stores/auth", () => ({
  useAuthStore: (sel: (s: { user: { email: string } }) => unknown) =>
    sel({ user: { email: "sarah.chen@example.com" } }),
}));

import { MentionTextarea } from "./MentionTextarea";
import { useMentionCandidates, useMentionEligibility } from "@/api/portal/hooks";

const mockCandidates = vi.mocked(useMentionCandidates);
const mockEligibility = vi.mocked(useMentionEligibility);

const target = { type: "asset", id: "a1" } as const;

const marcus = {
  email: "marcus.johnson@example.com",
  first_name: "Marcus",
  last_name: "Johnson",
  confirmed: true,
};

// Controlled wrapper: the composer is a controlled input, so the test holds the
// value the way the real forms do.
function Harness({ initial = "" }: { initial?: string }) {
  const [value, setValue] = useState(initial);
  return <MentionTextarea target={target} value={value} onChange={setValue} aria-label="Reply" />;
}

// The picker opens off a debounced query, so tests advance timers after typing.
function typeInto(text: string, caret = text.length) {
  const box = screen.getByLabelText("Reply");
  fireEvent.change(box, { target: { value: text, selectionStart: caret } });
  act(() => {
    vi.advanceTimersByTime(200);
  });
  return box;
}

afterEach(() => {
  vi.useRealTimers();
});

beforeEach(() => {
  vi.useFakeTimers();
  mockCandidates.mockReturnValue({ data: { candidates: [marcus] } } as never);
  mockEligibility.mockReturnValue({});
});

describe("MentionTextarea", () => {
  it("offers audience candidates once an @ is typed", () => {
    render(<Harness />);
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();

    typeInto("cc @mar");
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    expect(screen.getByText("Marcus Johnson")).toBeInTheDocument();
  });

  it("inserts the chosen person as a @local(domain) token", () => {
    render(<Harness />);
    typeInto("cc @mar");
    fireEvent.click(screen.getByText("Marcus Johnson"));

    expect(screen.getByLabelText("Reply")).toHaveValue("cc @marcus.johnson(example.com) ");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("picks the highlighted suggestion on Enter", () => {
    render(<Harness />);
    const box = typeInto("@mar");
    fireEvent.keyDown(box, { key: "Enter" });

    expect(box).toHaveValue("@marcus.johnson(example.com) ");
  });

  it("closes the picker on Escape without inserting", () => {
    render(<Harness />);
    const box = typeInto("@mar");
    fireEvent.keyDown(box, { key: "Escape" });

    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(box).toHaveValue("@mar");
  });

  it("does not open the picker inside an email address", () => {
    render(<Harness />);
    typeInto("write to bob@exa");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("marks someone in the audience as being notified", () => {
    mockEligibility.mockReturnValue({ "marcus.johnson@example.com": true });
    render(<Harness initial="@marcus.johnson(example.com) please review" />);

    expect(screen.getByText(/Notifying marcus.johnson@example.com/)).toBeInTheDocument();
  });

  // The decision on a name outside the audience is to accept the comment and
  // deliver nothing, so the composer has to say so before the comment is sent.
  it("warns that someone outside the audience will not be notified", () => {
    mockEligibility.mockReturnValue({ "outsider@example.com": false });
    render(<Harness initial="@outsider(example.com) take a look" />);

    expect(screen.getByText(/not among the people this item is shared with/)).toBeInTheDocument();
    expect(screen.getByText(/Share it with them first/)).toBeInTheDocument();
  });

  // Mentioning yourself notifies nobody, and the server drops it, so the
  // composer must not claim the author lacks access to their own item.
  it("says nothing about a mention of the author themselves", () => {
    mockEligibility.mockReturnValue({});
    render(<Harness initial="@sarah.chen(example.com) note to self" />);

    expect(screen.queryByText(/not among the people/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Notifying/)).not.toBeInTheDocument();
    expect(mockEligibility).toHaveBeenCalledWith(expect.anything(), []);
  });

  it("says nothing about delivery while eligibility is unknown", () => {
    mockEligibility.mockReturnValue({ "marcus.johnson@example.com": undefined });
    render(<Harness initial="@marcus.johnson(example.com) hi" />);

    expect(screen.queryByText(/Notifying/)).not.toBeInTheDocument();
    expect(screen.queryByText(/does not have access/)).not.toBeInTheDocument();
  });
});
