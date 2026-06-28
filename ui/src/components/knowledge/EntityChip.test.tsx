import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { EntityChip } from "./EntityChip";
import type { ResolvedRef } from "@/lib/entityRefs";

const resolved = (over: Partial<ResolvedRef>): ResolvedRef => ({
  urn: "mcp:asset:a1",
  type: "asset",
  label: "Sales Dashboard",
  exists: true,
  accessible: true,
  ...over,
});

describe("EntityChip", () => {
  it("deep-links an asset chip via onNavigate (criterion 5)", () => {
    const onNavigate = vi.fn();
    const { container } = render(
      <EntityChip urn="mcp:asset:a1" resolved={resolved({})} onNavigate={onNavigate} />,
    );
    const a = container.querySelector("a");
    expect(a).not.toBeNull();
    fireEvent.click(a!);
    expect(onNavigate).toHaveBeenCalledWith("/assets/a1");
  });

  it("renders a deleted reference as a broken, non-link chip (criterion 7)", () => {
    const onNavigate = vi.fn();
    const { container } = render(
      <EntityChip
        urn="mcp:knowledge_page:kp-9"
        resolved={resolved({ urn: "mcp:knowledge_page:kp-9", type: "knowledge_page", label: "Old Page", exists: false })}
        onNavigate={onNavigate}
      />,
    );
    expect(container.querySelector("a")).toBeNull(); // a broken ref is never a link
    expect(container.textContent).toContain("Old Page");
    expect(container.querySelector(".line-through")).not.toBeNull();
  });

  it("deep-links a knowledge-page chip to its detail route (#709)", () => {
    const onNavigate = vi.fn();
    const { container } = render(
      <EntityChip
        urn="mcp:knowledge_page:kp-7"
        resolved={resolved({ urn: "mcp:knowledge_page:kp-7", type: "knowledge_page", label: "Fiscal Calendar" })}
        onNavigate={onNavigate}
      />,
    );
    const a = container.querySelector("a");
    expect(a).not.toBeNull();
    fireEvent.click(a!);
    expect(onNavigate).toHaveBeenCalledWith("/knowledge/pages/kp-7");
  });

  it("renders a destination-less type as a neutral, non-link chip (connection)", () => {
    const onNavigate = vi.fn();
    const { container } = render(
      <EntityChip urn="mcp:connection:(trino,warehouse)" onNavigate={onNavigate} />,
    );
    // No route, so it must not be a link nor styled like one (#709): no <a>,
    // no primary/link tone, and not struck through (it is live, not broken).
    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).toContain("warehouse (trino)");
    const chip = container.querySelector("span.inline-flex");
    expect(chip?.className).not.toContain("text-primary");
    expect(chip?.className).not.toContain("line-through");
  });
});
