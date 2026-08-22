import { describe, it, expect, vi, afterEach } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react";
import type { Asset, AssetVersion } from "@/api/portal/types";
import { VersionControls } from "./contentControls";

// A scheduled script refreshes an asset hourly, so "v11" and "v12" say nothing
// about which one to open. The picker's list dates every option; its trigger
// does not, because the trigger is 36 units wide and the list is where the
// choice is made (#1422).

const asset = {
  id: "asset-1",
  owner_id: "u1",
  owner_email: "analyst@example.com",
  name: "Weather Watch",
  content_type: "text/html",
  s3_bucket: "assets",
  s3_key: "artifacts/u1/asset-1/content.html",
  size_bytes: 2048,
  tags: [],
  provenance: {},
  session_id: "dps_abc",
  current_version: 3,
  created_at: "2026-08-20T09:00:00Z",
  updated_at: "2026-08-21T11:05:00Z",
} as Asset;

function version(n: number, createdAt: string): AssetVersion {
  return {
    id: `ver-${n}`,
    asset_id: "asset-1",
    version: n,
    s3_key: `artifacts/u1/asset-1/ver-${n}/content.html`,
    s3_bucket: "assets",
    content_type: "text/html",
    size_bytes: 2048,
    created_by: "u1",
    change_summary: "",
    created_at: createdAt,
  };
}

// Dated in the current year, because the picker carries the year only for a
// version from an earlier one and these cases are about the in-year format.
const YEAR = new Date().getFullYear();

const versions = [
  version(3, `${YEAR}-08-21T11:05:00Z`),
  version(2, `${YEAR}-08-21T10:05:00Z`),
  version(1, `${YEAR}-08-20T09:00:00Z`),
];

/** What the picker's list renders for a version, per the reader's locale. */
function expectedTime(iso: string, withYear = false): string {
  return new Date(iso).toLocaleString(undefined, {
    year: withYear ? "numeric" : undefined,
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

function renderControls(props: Partial<Parameters<typeof VersionControls>[0]> = {}) {
  return render(
    <VersionControls
      asset={asset}
      versions={versions}
      selectedVersion={null}
      onSelectVersion={vi.fn()}
      viewingOldVersion={false}
      canRevert={false}
      onRevert={vi.fn()}
      {...props}
    />,
  );
}

afterEach(cleanup);

describe("VersionControls", () => {
  it("dates every option and marks the current version", async () => {
    renderControls();

    fireEvent.click(screen.getByRole("combobox", { name: "Asset version" }));
    await waitFor(() => expect(screen.getByRole("listbox")).toBeInTheDocument());

    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(3);

    expect(within(options[0]!).getByText("v3 (current)")).toBeInTheDocument();
    expect(within(options[1]!).getByText("v2")).toBeInTheDocument();
    expect(within(options[2]!).getByText("v1")).toBeInTheDocument();

    for (const [i, v] of versions.entries()) {
      expect(
        within(options[i]!).getByText(expectedTime(v.created_at)),
      ).toBeInTheDocument();
    }
  });

  // The timestamp belongs in the list. Radix would otherwise portal the whole
  // selected option, timestamp included, into a trigger far too narrow for it.
  it("keeps the trigger showing the version alone", async () => {
    renderControls();

    const trigger = screen.getByRole("combobox", { name: "Asset version" });
    expect(trigger).toHaveTextContent("v3 (current)");
    expect(trigger).not.toHaveTextContent(expectedTime(versions[0]!.created_at));

    fireEvent.click(trigger);
    await waitFor(() => expect(screen.getByRole("listbox")).toBeInTheDocument());
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("listbox")).not.toBeInTheDocument(),
    );

    expect(trigger).toHaveTextContent("v3 (current)");
    expect(trigger).not.toHaveTextContent(expectedTime(versions[0]!.created_at));
  });

  it("names the pinned version in the trigger while an older one is open", () => {
    renderControls({ selectedVersion: 1, viewingOldVersion: true });

    const trigger = screen.getByRole("combobox", { name: "Asset version" });
    expect(trigger).toHaveTextContent("v1");
    expect(trigger).not.toHaveTextContent("current");
  });

  // A version row whose created_at is missing or unparseable is still a version
  // a reader can open; it renders without a time rather than "Invalid Date".
  it("renders an option with no usable timestamp as the version alone", async () => {
    renderControls({
      versions: [
        { ...version(3, ""), version: 3 },
        { ...version(2, "not-a-date"), version: 2 },
      ],
    });

    fireEvent.click(screen.getByRole("combobox", { name: "Asset version" }));
    await waitFor(() => expect(screen.getByRole("listbox")).toBeInTheDocument());

    const options = screen.getAllByRole("option");
    expect(options[0]).toHaveTextContent(/^v3 \(current\)$/);
    expect(options[1]).toHaveTextContent(/^v2$/);
  });

  // Without the year, two versions written on the same day in different years
  // read identically — the exact ambiguity the timestamp is here to remove. An
  // asset keeps up to 100 versions by default (#1421), so a weekly refresh
  // spans years.
  it("carries the year only for a version from an earlier one", async () => {
    const thisYear = `${YEAR}-03-04T15:30:00Z`;
    const lastYear = `${YEAR - 1}-03-04T15:30:00Z`;
    renderControls({
      versions: [version(3, thisYear), version(2, lastYear)],
    });

    fireEvent.click(screen.getByRole("combobox", { name: "Asset version" }));
    await waitFor(() => expect(screen.getByRole("listbox")).toBeInTheDocument());

    const options = screen.getAllByRole("option");
    expect(
      within(options[0]!).getByText(expectedTime(thisYear)),
    ).toBeInTheDocument();
    expect(
      within(options[1]!).getByText(expectedTime(lastYear, true)),
    ).toBeInTheDocument();
    expect(options[1]).toHaveTextContent(String(YEAR - 1));
    expect(options[0]).not.toHaveTextContent(String(YEAR));
  });

  it("renders nothing when the asset has no version history", () => {
    const { container } = renderControls({ versions: [] });
    expect(container).toBeEmptyDOMElement();
  });
});
