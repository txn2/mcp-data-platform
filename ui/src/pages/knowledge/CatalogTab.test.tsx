import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

// Mock the DataHub API module so the tab renders against controlled data with no
// network. Each hook is a vi.fn configured per test.
vi.mock("@/api/portal/datahub", () => ({
  MIN_SEARCH_LEN: 2,
  useCatalogBrowse: vi.fn(),
  useCatalogSearch: vi.fn(),
  useCatalogEntity: vi.fn(),
  useUpdateDescription: vi.fn(),
  useUpdateTags: vi.fn(),
  useUpdateOwners: vi.fn(),
  useUpdateGlossaryTerms: vi.fn(),
  useUpdateDomain: vi.fn(),
}));
vi.mock("@/components/knowledge/DataHubConnectionSelect", () => ({
  DataHubConnectionSelect: () => null,
  useConnectionWritable: vi.fn(() => true),
}));

let mockIsAdmin = true;
let mockTools: string[] = [];
vi.mock("@/stores/auth", () => ({
  useAuthStore: (sel: (s: unknown) => unknown) =>
    sel({ user: { tools: mockTools }, isAdmin: () => mockIsAdmin }),
}));

import { CatalogTab } from "./CatalogTab";
import {
  useCatalogBrowse,
  useCatalogSearch,
  useCatalogEntity,
  useUpdateDescription,
  useUpdateTags,
  useUpdateOwners,
  useUpdateGlossaryTerms,
  useUpdateDomain,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";

const q = (data: unknown) => ({ data, isLoading: false, isError: false }) as never;
const noopMut = () => ({ mutate: vi.fn(), isPending: false, isError: false, error: null }) as never;

const daily = {
  urn: "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.public.daily_sales,PROD)",
  name: "analytics.public.daily_sales",
  description: "Daily sales.",
  tags: ["urn:li:tag:finance"],
};

beforeEach(() => {
  mockIsAdmin = true;
  mockTools = [];
  vi.mocked(useConnectionWritable).mockReturnValue(true);
  vi.mocked(useCatalogBrowse).mockReturnValue(q([daily]));
  vi.mocked(useCatalogSearch).mockReturnValue(q([]));
  vi.mocked(useCatalogEntity).mockReturnValue(
    q({
      urn: daily.urn,
      context: {
        urn: daily.urn,
        description: "Daily sales.",
        tags: ["urn:li:tag:finance"],
        owners: [{ urn: "urn:li:corpuser:sarah", type: "TECHNICAL_OWNER", name: "Sarah" }],
        glossary_terms: [{ urn: "urn:li:glossaryTerm:Revenue", name: "Revenue" }],
        domain: { urn: "urn:li:domain:finance", name: "Finance" },
      },
      columns: { revenue: { name: "revenue", description: "USD", is_sensitive: true } },
    }),
  );
  [useUpdateDescription, useUpdateTags, useUpdateOwners, useUpdateGlossaryTerms, useUpdateDomain].forEach((h) =>
    vi.mocked(h).mockImplementation(noopMut),
  );
});

describe("CatalogTab", () => {
  it("browses datasets and opens an entity showing metadata and columns", () => {
    render(<CatalogTab conn="primary" onConnChange={vi.fn()} />);
    expect(screen.getByText("analytics.public.daily_sales")).toBeInTheDocument();

    fireEvent.click(screen.getByText("analytics.public.daily_sales"));
    expect(screen.getByRole("heading", { name: /daily_sales/ })).toBeInTheDocument();
    expect(screen.getByText("Revenue")).toBeInTheDocument();
    expect(screen.getByText("Sarah")).toBeInTheDocument();
    expect(screen.getByText("Finance")).toBeInTheDocument();
    // Column with a sensitivity badge.
    expect(screen.getByText("revenue")).toBeInTheDocument();
    expect(screen.getByText("Sensitive")).toBeInTheDocument();
  });

  it("shows edit affordances for a writer and drives a description edit", () => {
    const mutate = vi.fn();
    vi.mocked(useUpdateDescription).mockReturnValue({ mutate, isPending: false, isError: false, error: null } as never);
    render(<CatalogTab conn="primary" onConnChange={vi.fn()} />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));

    // Edit the description.
    const editButtons = screen.getAllByRole("button", { name: "Edit" });
    fireEvent.click(editButtons[0]!);
    // The description editor renders a <textarea>; the tag/owner/domain add fields
    // are <input>s. Target the textarea specifically.
    const textarea = document.querySelector("textarea")!;
    fireEvent.change(textarea, { target: { value: "Updated." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(mutate).toHaveBeenCalledWith(
      { urn: daily.urn, description: "Updated." },
      expect.anything(),
    );
  });

  it("hides all edit affordances when the connection is read-only", () => {
    vi.mocked(useConnectionWritable).mockReturnValue(false);
    mockIsAdmin = true;
    render(<CatalogTab conn="primary" onConnChange={vi.fn()} />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add" })).not.toBeInTheDocument();
  });

  it("hides edit affordances when the persona lacks datahub_update and is not admin", () => {
    mockIsAdmin = false;
    mockTools = ["datahub_browse"];
    render(<CatalogTab conn="primary" onConnChange={vi.fn()} />);
    fireEvent.click(screen.getByText("analytics.public.daily_sales"));
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
  });
});
