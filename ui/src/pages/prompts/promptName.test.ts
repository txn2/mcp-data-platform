import { describe, it, expect } from "vitest";
import { validatePromptName, isPromptNameConflict, PROMPT_NAME_MAX_LENGTH } from "./promptName";

describe("validatePromptName", () => {
  it("accepts lowercase, digits, hyphens, and underscores", () => {
    for (const name of ["daily-sales-report", "daily_sales_report", "report1", "a", "0name"]) {
      expect(validatePromptName(name)).toBeNull();
    }
  });

  it("rejects empty, uppercase, spaces, and a leading non-alphanumeric", () => {
    expect(validatePromptName("")).toMatch(/required/i);
    expect(validatePromptName("Daily Report")).toMatch(/lowercase/i);
    expect(validatePromptName("daily report")).toMatch(/lowercase/i);
    expect(validatePromptName("CamelCase")).toMatch(/lowercase/i);
    // Must start with a letter or digit, not a hyphen/underscore.
    expect(validatePromptName("-leading")).toMatch(/lowercase/i);
    expect(validatePromptName("_leading")).toMatch(/lowercase/i);
  });

  it("rejects names over the max length", () => {
    expect(validatePromptName("a".repeat(PROMPT_NAME_MAX_LENGTH))).toBeNull();
    expect(validatePromptName("a".repeat(PROMPT_NAME_MAX_LENGTH + 1))).toMatch(/at most/i);
  });

  it("mirrors the backend pattern exactly", () => {
    // Same regex as pkg/prompt/prompt.go validNamePattern.
    expect(validatePromptName("ok-1_2")).toBeNull();
    expect(validatePromptName("bad!")).toMatch(/lowercase/i);
  });
});

describe("isPromptNameConflict", () => {
  it("detects the server's duplicate-name message", () => {
    expect(isPromptNameConflict("prompt name already exists")).toBe(true);
    expect(isPromptNameConflict("Prompt name already exists")).toBe(true);
  });

  it("does not match unrelated errors", () => {
    expect(isPromptNameConflict("internal server error")).toBe(false);
    expect(isPromptNameConflict("")).toBe(false);
  });
});
