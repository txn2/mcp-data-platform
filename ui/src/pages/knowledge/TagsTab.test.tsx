import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";

vi.mock("@/api/portal/datahub", () => ({
  MIN_SEARCH_LEN: 2,
  TAG_LIST_LIMIT: 100,
  useTagList: vi.fn(),
  useTagUsage: vi.fn(),
  useCreateTag: vi.fn(),
  useDeleteTag: vi.fn(),
  useUpdateDescription: vi.fn(),
}));
// The connection picker lives on CatalogSection, not here; only the writable
// lookup is still read from this module.
vi.mock("@/components/knowledge/DataHubConnectionSelect", () => ({
  useConnectionWritable: vi.fn(() => true),
}));

let mockIsAdmin = true;
let mockTools: string[] = [];
vi.mock("@/stores/auth", () => ({
  useAuthStore: (sel: (s: unknown) => unknown) =>
    sel({ user: { tools: mockTools }, isAdmin: () => mockIsAdmin }),
}));

import { TagsTab } from "./TagsTab";
import {
  TAG_LIST_LIMIT,
  useTagList,
  useTagUsage,
  useCreateTag,
  useDeleteTag,
  useUpdateDescription,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";

const q = (data: unknown) => ({ data, isLoading: false, isError: false }) as never;
// lastCall reads the arguments of a mock's most recent call.
const lastCall = <T,>(calls: T[]): T | undefined => calls[calls.length - 1];
const noopMut = () =>
  ({ mutate: vi.fn(), isPending: false, isError: false, isSuccess: false, error: null }) as never;

const certified = {
  urn: "urn:li:tag:certified",
  name: "certified",
  description: "Reviewed by the data team.",
};
const pii = { urn: "urn:li:tag:pii", name: "pii" };
const carrier = {
  urn: "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.public.daily_sales,PROD)",
  name: "daily_sales",
  description: "Daily aggregated sales.",
};

beforeEach(() => {
  mockIsAdmin = true;
  mockTools = [];
  vi.mocked(useConnectionWritable).mockReturnValue(true);
  vi.mocked(useTagList).mockReturnValue(q([certified, pii]));
  vi.mocked(useTagUsage).mockReturnValue(q([carrier]));
  [useCreateTag, useDeleteTag, useUpdateDescription].forEach((h) =>
    vi.mocked(h).mockImplementation(noopMut),
  );
});

describe("TagsTab", () => {
  it("lists tags with their descriptions", () => {
    render(<TagsTab conn="primary" />);
    expect(screen.getByText("certified")).toBeInTheDocument();
    expect(screen.getByText("Reviewed by the data team.")).toBeInTheDocument();
    // A tag with no description says so rather than rendering an empty line.
    expect(screen.getByText("No description")).toBeInTheDocument();
  });

  it("sends the typed filter to the server after the debounce, not per keystroke", () => {
    vi.useFakeTimers();
    try {
      // The server does the filtering, so a filtered read is a distinct result:
      // here nothing matches, which the tab must report as a filter miss rather
      // than as an empty connection.
      vi.mocked(useTagList).mockImplementation(
        ((_conn: string, query: string) => q(query ? [] : [certified, pii])) as never,
      );
      render(<TagsTab conn="primary" />);
      fireEvent.change(screen.getByPlaceholderText("Filter tags by name…"), {
        target: { value: "cert" },
      });
      // Before the debounce elapses the server query is still the empty one:
      // filtering happens server-side, so a per-keystroke refetch would be a
      // request per character.
      expect(lastCall(vi.mocked(useTagList).mock.calls)).toEqual(["primary", ""]);

      act(() => vi.advanceTimersByTime(300));
      expect(lastCall(vi.mocked(useTagList).mock.calls)).toEqual(["primary", "cert"]);
      expect(screen.getByText("No tags match that name.")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("opens a tag and shows the tables carrying it", () => {
    const onNavigate = vi.fn();
    render(<TagsTab conn="primary" onNavigate={onNavigate} />);
    fireEvent.click(screen.getByText("certified"));

    expect(screen.getByRole("heading", { name: /certified/ })).toBeInTheDocument();
    expect(screen.getByText("urn:li:tag:certified")).toBeInTheDocument();
    expect(screen.getByText("Tables carrying this tag")).toBeInTheDocument();

    // The carrier deep-links into the catalog entity editor rather than
    // reloading the page.
    fireEvent.click(screen.getByText("daily_sales"));
    expect(onNavigate).toHaveBeenCalledWith(
      `/knowledge/catalog?urn=${encodeURIComponent(carrier.urn)}`,
    );
  });

  it("reports an empty usage list instead of an empty section", () => {
    vi.mocked(useTagUsage).mockReturnValue(q([]));
    render(<TagsTab conn="primary" />);
    fireEvent.click(screen.getByText("certified"));
    expect(screen.getByText("No table in this connection carries this tag.")).toBeInTheDocument();
  });

  it("creates a tag with its name and description", () => {
    const mutate = vi.fn();
    vi.mocked(useCreateTag).mockReturnValue({
      mutate,
      isPending: false,
      isError: false,
      error: null,
    } as never);
    render(<TagsTab conn="primary" />);
    fireEvent.click(screen.getByRole("button", { name: "New tag" }));

    // Create is refused until the tag has a name.
    expect(screen.getByRole("button", { name: "Create tag" })).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText("e.g. certified"), {
      target: { value: "  golden  " },
    });
    fireEvent.change(screen.getByPlaceholderText(/What this tag means/), {
      target: { value: "Trusted for executive reporting." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create tag" }));

    expect(mutate).toHaveBeenCalledWith(
      { name: "golden", description: "Trusted for executive reporting." },
      expect.anything(),
    );
  });

  it("edits a tag description through the entity-description write", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateDescription).mockReturnValue({
      mutate,
      isPending: false,
      isError: false,
      isSuccess: false,
      error: null,
    } as never);
    render(<TagsTab conn="primary" />);
    fireEvent.click(screen.getByText("certified"));
    fireEvent.click(screen.getByRole("button", { name: "Edit description" }));

    const box = screen.getByLabelText("Tag description");
    expect(box).toHaveValue("Reviewed by the data team.");
    fireEvent.change(box, { target: { value: "Certified for reuse." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(mutate).toHaveBeenCalledWith(
      { urn: "urn:li:tag:certified", description: "Certified for reuse." },
      expect.anything(),
    );
  });

  it("shows what carries a tag before confirming its delete", () => {
    const mutate = vi.fn();
    vi.mocked(useDeleteTag).mockReturnValue({
      mutate,
      isPending: false,
      isError: false,
      error: null,
    } as never);
    render(<TagsTab conn="primary" />);
    fireEvent.click(screen.getByText("certified"));
    fireEvent.click(screen.getByRole("button", { name: "Delete tag" }));

    expect(
      screen.getByText(/1 table in this connection carries this tag/),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    expect(mutate).toHaveBeenCalledWith("urn:li:tag:certified", expect.anything());
  });

  it("does not claim a tag is unused when the usage read failed", () => {
    vi.mocked(useTagUsage).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
    } as never);
    render(<TagsTab conn="primary" />);
    fireEvent.click(screen.getByText("certified"));
    fireEvent.click(screen.getByRole("button", { name: "Delete tag" }));
    expect(screen.getByText(/effect of deleting it is unknown/)).toBeInTheDocument();
    expect(screen.queryByText(/No table in this connection carries/)).not.toBeInTheDocument();
  });

  it("surfaces a failed delete instead of silently returning", () => {
    vi.mocked(useDeleteTag).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      isError: true,
      error: null,
    } as never);
    render(<TagsTab conn="primary" />);
    fireEvent.click(screen.getByText("certified"));
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });

  it("hides every write affordance on a read-only connection", () => {
    vi.mocked(useConnectionWritable).mockReturnValue(false);
    render(<TagsTab conn="primary" />);
    expect(screen.queryByRole("button", { name: "New tag" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("certified"));
    expect(screen.queryByRole("button", { name: "Delete tag" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit description" })).not.toBeInTheDocument();
    // The read surfaces stay.
    expect(screen.getByText("Tables carrying this tag")).toBeInTheDocument();
    expect(screen.getByText("Reviewed by the data team.")).toBeInTheDocument();
  });

  it("hides each write affordance the persona does not grant", () => {
    mockIsAdmin = false;
    mockTools = ["datahub_update"];
    render(<TagsTab conn="primary" />);
    // Update alone grants the description edit, not create or delete.
    expect(screen.queryByRole("button", { name: "New tag" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("certified"));
    expect(screen.getByRole("button", { name: "Edit description" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete tag" })).not.toBeInTheDocument();
  });

  it("says the list is capped rather than presenting a full page as complete", () => {
    const many = Array.from({ length: TAG_LIST_LIMIT }, (_, i) => ({
      urn: `urn:li:tag:t${i}`,
      name: `t${i}`,
    }));
    vi.mocked(useTagList).mockReturnValue(q(many));
    render(<TagsTab conn="primary" />);
    expect(
      screen.getByText(new RegExp(`Showing the first ${TAG_LIST_LIMIT} tags`)),
    ).toBeInTheDocument();
  });

  it("reports a capped usage count as a floor in the delete confirmation", () => {
    const many = Array.from({ length: TAG_LIST_LIMIT }, (_, i) => ({
      urn: `urn:li:dataset:(urn:li:dataPlatform:trino,d${i},PROD)`,
      name: `d${i}`,
    }));
    vi.mocked(useTagUsage).mockReturnValue(q(many));
    render(<TagsTab conn="primary" />);
    fireEvent.click(screen.getByText("certified"));
    fireEvent.click(screen.getByRole("button", { name: "Delete tag" }));
    expect(
      screen.getByText(new RegExp(`At least ${TAG_LIST_LIMIT} tables`)),
    ).toBeInTheDocument();
  });

  it("reports a failed tag list rather than an empty one", () => {
    vi.mocked(useTagList).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
    } as never);
    render(<TagsTab conn="primary" />);
    expect(screen.getByText("Failed to load tags.")).toBeInTheDocument();
  });
});
