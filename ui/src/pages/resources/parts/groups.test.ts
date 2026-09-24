import { describe, it, expect } from "vitest";
import type { Resource } from "@/api/resources/types";
import { neverRead } from "./groups";

const daysAgo = (n: number) => new Date(Date.now() - n * 86_400_000).toISOString();

function resource(overrides: Partial<Resource> = {}): Resource {
  return {
    id: "res-1",
    scope: "user",
    scope_id: "analyst@example.com",
    path: "references",
    filename: "notes.md",
    display_name: "Notes",
    description: "",
    mime_type: "text/markdown",
    size_bytes: 64,
    s3_key: "k",
    uri: "mcp://resources/analyst/notes.md",
    tags: [],
    uploader_sub: "analyst@example.com",
    uploader_email: "analyst@example.com",
    created_at: "2026-08-03T10:00:00Z",
    updated_at: "2026-08-17T10:00:00Z",
    ...overrides,
  };
}

describe("what counts as never read", () => {
  it("flags a resource old enough to have been read and never was", () => {
    expect(neverRead(resource({ created_at: daysAgo(60) }))).toBe(true);
  });

  it("does not flag one uploaded too recently for that to mean anything", () => {
    expect(neverRead(resource({ created_at: daysAgo(3) }))).toBe(false);
  });

  it("does not flag one that has been read", () => {
    expect(neverRead(resource({ created_at: daysAgo(60), last_read_at: daysAgo(1) }))).toBe(false);
  });
});
