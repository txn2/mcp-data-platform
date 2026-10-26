import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { FormSubmissionsPage } from "@/api/portal/hooks/scriptSubmissions";
import { ScriptSubmissions, cellText } from "./ScriptSubmissions";

// A form's stored submissions (#2180): the newest rows, sensitive columns
// marked, and the two exports.

vi.mock("@/api/portal/hooks/scriptEndpoint", () => ({ useScriptEndpoint: vi.fn() }));
vi.mock("@/api/portal/hooks/scriptSubmissions", async (orig) => ({
  ...(await orig<typeof import("@/api/portal/hooks/scriptSubmissions")>()),
  useScriptSubmissions: vi.fn(),
}));

import { useScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { useScriptSubmissions } from "@/api/portal/hooks/scriptSubmissions";

const refetch = vi.fn();

const page: FormSubmissionsPage = {
  form: "apply",
  source: "form-apply",
  table: "webhook_form_apply",
  connection: "scratch",
  fields: ["name", "ssn", "topics", "agree"],
  sensitive: ["ssn"],
  limit: 50,
  submissions: [
    {
      id: "s2",
      received_at: "2026-10-26T09:30:00Z",
      submitter: { user_id: "u-bo", email: "bo@example.com" },
      values: { name: "Bo", ssn: "987-65-4321", topics: ["a", "b"], agree: true },
    },
    { id: "s1", received_at: "2026-10-26T08:30:00Z", values: { name: "Ana", ssn: "123-45-6789", topics: [], agree: false } },
  ],
};

function withForm(stored: boolean, data?: FormSubmissionsPage) {
  vi.mocked(useScriptEndpoint).mockReturnValue({
    data: stored
      ? { name: "apply", bound: true, is_form: true, form: { source: "form-apply", table: "webhook_form_apply", connection: "scratch", decrypt_personas: [] } }
      : { name: "apply", bound: false, is_form: true },
  } as never);
  vi.mocked(useScriptSubmissions).mockReturnValue({ data, isLoading: false, isFetching: false, error: null, refetch } as never);
}

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe("ScriptSubmissions", () => {
  it("shows the newest submissions with the sensitive column marked", () => {
    withForm(true, page);
    render(<ScriptSubmissions scriptId="script-001" />);
    expect(screen.getByRole("heading", { name: "Submissions" })).toBeInTheDocument();
    expect(screen.getByText("987-65-4321")).toBeInTheDocument();
    expect(screen.getByText("bo@example.com")).toBeInTheDocument();
    expect(screen.getByLabelText("Encrypted when stored")).toBeInTheDocument();
    expect(screen.getByText("a, b")).toBeInTheDocument();
    expect(screen.getByText("Stored in webhook_form_apply on scratch.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Export CSV" })).toHaveAttribute(
      "href",
      "/api/v1/portal/scripts/script-001/submissions/export?format=csv",
    );
    expect(screen.getByRole("link", { name: "Export Excel" })).toHaveAttribute(
      "href",
      "/api/v1/portal/scripts/script-001/submissions/export?format=xlsx",
    );
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(refetch).toHaveBeenCalled();
  });

  it("says when nothing has been submitted", () => {
    withForm(true, { ...page, submissions: [] });
    render(<ScriptSubmissions scriptId="script-001" />);
    expect(screen.getByText("No submissions yet.")).toBeInTheDocument();
  });

  it("shows nothing for a form whose source does not exist yet", () => {
    withForm(false);
    const { container } = render(<ScriptSubmissions scriptId="script-001" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders each value plainly", () => {
    expect(cellText(undefined)).toBe("—");
    expect(cellText("")).toBe("—");
    expect(cellText([])).toBe("—");
    expect(cellText(true)).toBe("Yes");
    expect(cellText(false)).toBe("No");
    expect(cellText(4.5)).toBe("4.5");
    expect(cellText({ a: 1 })).toBe('{"a":1}');
  });
});
