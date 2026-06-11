import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";

import { SourceBadge } from "./CatalogsPanel";

describe("SourceBadge", () => {
  it("renders a label and icon for every known source kind", () => {
    const cases: Array<{ kind: "inline" | "upload" | "url" | "embedded"; label: string }> = [
      { kind: "inline", label: "inline" },
      { kind: "upload", label: "upload" },
      { kind: "url", label: "URL" },
      { kind: "embedded", label: "embedded" },
    ];
    for (const { kind, label } of cases) {
      const { unmount } = render(<SourceBadge kind={kind} />);
      expect(screen.getByText(label)).toBeInTheDocument();
      unmount();
    }
  });

  // Regression: an embedded spec (re-seeded from the platform self-connection)
  // previously crashed the whole catalogs page because SourceBadge indexed a
  // three-key map and read `.icon` off undefined. It must render, not throw.
  it("does not throw for embedded specs", () => {
    expect(() => render(<SourceBadge kind="embedded" />)).not.toThrow();
  });

  // Any future backend source_kind must degrade to a plain badge showing the
  // raw kind rather than crashing the page.
  it("degrades gracefully for an unknown source kind", () => {
    const kind = "future-kind" as unknown as "inline";
    expect(() => render(<SourceBadge kind={kind} />)).not.toThrow();
    expect(screen.getByText("future-kind")).toBeInTheDocument();
  });
});
