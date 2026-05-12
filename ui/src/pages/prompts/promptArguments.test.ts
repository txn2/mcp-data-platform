import { describe, it, expect } from "vitest";
import { extractPromptArguments } from "./promptArguments";

describe("extractPromptArguments", () => {
  it("returns empty list for content with no placeholders", () => {
    expect(extractPromptArguments("just text", [])).toEqual([]);
  });

  it("extracts placeholders in order of first occurrence", () => {
    const out = extractPromptArguments("Hello {{name}}, you have {{count}} items.", []);
    expect(out.map((a) => a.name)).toEqual(["name", "count"]);
  });

  it("deduplicates repeated placeholders", () => {
    const out = extractPromptArguments("{{x}} and {{x}} again and {{y}}", []);
    expect(out.map((a) => a.name)).toEqual(["x", "y"]);
  });

  it("defaults new placeholders to required=true with empty description", () => {
    const [a] = extractPromptArguments("{{fun}}", []);
    expect(a).toEqual({ name: "fun", description: "", required: true });
  });

  it("preserves description and required flag for existing placeholders", () => {
    const existing = [
      { name: "name", description: "User's name", required: false },
      { name: "count", description: "How many", required: true },
    ];
    const out = extractPromptArguments("{{name}} {{count}}", existing);
    expect(out).toEqual(existing);
  });

  it("drops arguments no longer referenced in content", () => {
    const existing = [
      { name: "kept", description: "Still here", required: true },
      { name: "gone", description: "Removed from content", required: true },
    ];
    const out = extractPromptArguments("Only {{kept}} remains", existing);
    expect(out.map((a) => a.name)).toEqual(["kept"]);
  });

  it("rejects whitespace inside braces (backend substituter is literal)", () => {
    // The backend uses strings.ReplaceAll("{{name}}", ...) with no whitespace
    // tolerance — if the UI extracted args from `{{ foo }}`, the runtime
    // substitution would leave the literal `{{ foo }}` in the output.
    const out = extractPromptArguments("{{ foo }} and {{  bar  }}", []);
    expect(out).toEqual([]);
  });

  it("accepts single-brace placeholders (legacy syntax)", () => {
    const out = extractPromptArguments("Run analysis on {dataset} for {region}.", []);
    expect(out.map((a) => a.name)).toEqual(["dataset", "region"]);
  });

  it("deduplicates across single- and double-brace forms of the same name", () => {
    const out = extractPromptArguments("{name} and {{name}}", []);
    expect(out.map((a) => a.name)).toEqual(["name"]);
  });

  it("preserves required=false across re-extract when the placeholder is still in content", () => {
    const existing = [
      { name: "optional_arg", description: "old description", required: false },
    ];
    const out = extractPromptArguments("Use {{optional_arg}} here", existing);
    expect(out).toEqual([
      { name: "optional_arg", description: "old description", required: false },
    ]);
  });

  it("preserves required=false when the legacy single-brace placeholder is still in content", () => {
    const existing = [
      { name: "region", description: "Which region", required: false },
    ];
    const out = extractPromptArguments("Look at {region}", existing);
    expect(out[0]?.required).toBe(false);
  });

  it("ignores malformed double-brace placeholders", () => {
    const out = extractPromptArguments("{{1bad}} or {{good_name}}", []);
    expect(out.map((a) => a.name)).toEqual(["good_name"]);
  });

  it("accepts underscores and digits in argument names", () => {
    const out = extractPromptArguments("{{_private}} {{arg2}} {{snake_case_99}}", []);
    expect(out.map((a) => a.name)).toEqual(["_private", "arg2", "snake_case_99"]);
  });

  it("handles undefined existing list", () => {
    const out = extractPromptArguments("{{x}}", undefined);
    expect(out).toEqual([{ name: "x", description: "", required: true }]);
  });
});
