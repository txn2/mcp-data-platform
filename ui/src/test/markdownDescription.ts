import { expect } from "vitest";
import { screen } from "@testing-library/react";

// The shape a real vocabulary description takes once its surface renders
// markdown (#1200): a heading, emphasis, a list of what is in and out, and a
// fenced example. Domains and glossary entities share the fixture because they
// share the component under test, and a difference between them here would only
// be noise.
export const MARKDOWN_DESCRIPTION = [
  "## Included",
  "",
  "**Recognized** revenue only.",
  "",
  "- Subscriptions",
  "- Services",
  "",
  "```sql",
  "SELECT SUM(amount) FROM sales",
  "```",
].join("\n");

// expectRenderedMarkdown asserts MARKDOWN_DESCRIPTION reached the reader
// formatted. It checks the rendered elements rather than the absence of the
// source characters alone: a renderer that dropped the content entirely would
// also lack "## Included", and that is a different failure from rendering it.
export function expectRenderedMarkdown(): void {
  expect(screen.getByRole("heading", { level: 2, name: "Included" })).toBeInTheDocument();
  expect(screen.getByText("Recognized").tagName).toBe("STRONG");
  expect(screen.getByText("Subscriptions").tagName).toBe("LI");
  expect(screen.getByText("Services").tagName).toBe("LI");
  expect(screen.getByText("SELECT SUM(amount) FROM sales").tagName).toBe("CODE");
  // And the source markers are gone, so nothing is being shown twice: once
  // formatted and once literally.
  expect(screen.queryByText(/## Included/)).not.toBeInTheDocument();
  expect(screen.queryByText(/- Subscriptions/)).not.toBeInTheDocument();
}
