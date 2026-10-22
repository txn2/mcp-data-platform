import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { AccessRequest, AccessRequestPage } from "@/api/portal/hooks";

// The two surfaces of a request for access (#2090): the page a person who
// cannot open an item sees, and the owner's list of requests to answer.

const state = vi.hoisted(() => ({
  page: { isLoading: false, data: undefined as AccessRequestPage | undefined },
  request: {
    mutate: vi.fn(),
    isPending: false,
    error: null as Error | null,
    data: undefined as AccessRequest | undefined,
  },
  list: { data: { requests: [] as AccessRequest[] } },
  decide: { mutate: vi.fn(), isPending: false },
}));

vi.mock("@/api/portal/hooks", () => ({
  useAccessRequestPage: () => state.page,
  useRequestAccess: () => state.request,
  useAccessRequests: () => state.list,
  useDecideAccessRequest: () => state.decide,
}));

import { NoAccess } from "./RequestAccessPanel";
import { AccessRequestsList } from "./AccessRequestsList";

const pending: AccessRequest = {
  id: "ar-1",
  target_type: "asset",
  target_id: "a1",
  requester_email: "priya@example.com",
  owner_email: "alice@example.com",
  message: "For the quarterly review.",
  status: "pending",
  created_at: "2026-10-20T15:04:00Z",
  expires_at: "2026-11-03T15:04:00Z",
  target_name: "Q4 Revenue Dashboard",
};

beforeEach(() => {
  state.page = { isLoading: false, data: undefined };
  state.request = {
    mutate: vi.fn(),
    isPending: false,
    error: null,
    data: undefined,
  };
  state.list = { data: { requests: [] } };
  state.decide = { mutate: vi.fn(), isPending: false };
});

describe("NoAccess", () => {
  it("shows the not-found state for an item that does not exist", () => {
    render(<NoAccess type="asset" id="a1" notFound={<p>Asset not found</p>} />);
    expect(screen.getByText("Asset not found")).toBeInTheDocument();
  });

  it("shows the not-found state to a person who does open the item", () => {
    state.page.data = { has_access: true, owner_email: "alice@example.com" };
    render(<NoAccess type="asset" id="a1" notFound={<p>Asset not found</p>} />);
    expect(screen.getByText("Asset not found")).toBeInTheDocument();
  });

  it("renders nothing while the item is read", () => {
    state.page.isLoading = true;
    const { container } = render(
      <NoAccess type="asset" id="a1" notFound={<p>Asset not found</p>} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("asks the owner for access with the message written", () => {
    state.page.data = { has_access: false, owner_email: "alice@example.com" };
    render(<NoAccess type="collection" id="c1" notFound={null} />);
    expect(
      screen.getByText("You do not have access to this item"),
    ).toBeInTheDocument();
    expect(screen.getByText("Owned by alice@example.com")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Message"), {
      target: { value: "  need it  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Request access" }));
    expect(state.request.mutate).toHaveBeenCalledWith({
      type: "collection",
      id: "c1",
      message: "need it",
    });
  });

  it("sends no message when none is written", () => {
    state.page.data = { has_access: false, owner_email: "alice@example.com" };
    render(<NoAccess type="prompt" id="p1" notFound={null} />);
    fireEvent.click(screen.getByRole("button", { name: "Request access" }));
    expect(state.request.mutate).toHaveBeenCalledWith({
      type: "prompt",
      id: "p1",
      message: undefined,
    });
  });

  it("names the item when the deployment reveals names", () => {
    state.page.data = {
      has_access: false,
      name: "Q4 Revenue Dashboard",
      owner_email: "alice@example.com",
    };
    render(<NoAccess type="asset" id="a1" notFound={null} />);
    expect(screen.getByText("Q4 Revenue Dashboard")).toBeInTheDocument();
  });

  it("says the request was sent, in place of the form, once one is pending", () => {
    state.page.data = {
      has_access: false,
      owner_email: "alice@example.com",
      pending,
    };
    render(<NoAccess type="asset" id="a1" notFound={null} />);
    expect(
      screen.getByText("Your request was sent to alice@example.com."),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Request access" }),
    ).not.toBeInTheDocument();
  });

  it("shows why a request was refused", () => {
    state.page.data = { has_access: false, owner_email: "alice@example.com" };
    state.request.error = new Error(
      "you have asked for access to this item too often today; try again tomorrow",
    );
    render(<NoAccess type="asset" id="a1" notFound={null} />);
    expect(screen.getByText(/too often today/)).toBeInTheDocument();
  });
});

describe("AccessRequestsList", () => {
  it("renders nothing when no request waits", () => {
    const { container } = render(<AccessRequestsList />);
    expect(container).toBeEmptyDOMElement();
  });

  it("lists each request with the item's name and answers it in one step", () => {
    state.list.data = { requests: [pending] };
    render(<AccessRequestsList />);
    expect(screen.getByText("priya@example.com")).toBeInTheDocument();
    expect(screen.getByText(/Q4 Revenue Dashboard/)).toBeInTheDocument();
    expect(screen.getByText("For the quarterly review.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Approve as viewer" }));
    expect(state.decide.mutate).toHaveBeenCalledWith({
      id: "ar-1",
      decision: "viewer",
    });
    fireEvent.click(screen.getByRole("button", { name: "Approve as editor" }));
    expect(state.decide.mutate).toHaveBeenCalledWith({
      id: "ar-1",
      decision: "editor",
    });
    fireEvent.click(screen.getByRole("button", { name: "Decline" }));
    expect(state.decide.mutate).toHaveBeenCalledWith({
      id: "ar-1",
      decision: "decline",
    });
  });

  it("leaves the item's name out of one item's list", () => {
    state.list.data = { requests: [pending] };
    render(<AccessRequestsList target={{ type: "asset", id: "a1" }} />);
    expect(screen.queryByText(/Q4 Revenue Dashboard/)).not.toBeInTheDocument();
  });
});
