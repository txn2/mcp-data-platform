import { describe, expect, it } from "vitest";

import type { SearchHit } from "@/api/portal/types";
import { hitDestination } from "./hitDestination";

const hit = (source: string, ref: string): SearchHit => ({
  text: "a result",
  source,
  ref,
  score: 0.5,
});

describe("hitDestination", () => {
  it("opens each portal-backed source at its own surface", () => {
    expect(hitDestination(hit("assets", "ast-001"))).toEqual({ href: "/assets/ast-001" });
    expect(hitDestination(hit("prompts", "prompt-001"))).toEqual({ href: "/prompts/prompt-001" });
    expect(hitDestination(hit("knowledge_pages", "kp-seed-1"))).toEqual({
      href: "/knowledge/pages/kp-seed-1",
    });
  });

  // A session and a recorded call are the reader's own work, listed under
  // Activity (#1322, #1321).
  it("opens a session and a call where the reader's own work is listed", () => {
    expect(hitDestination(hit("sessions", "dps_9f2c"))).toEqual({
      href: "/activity/sessions/dps_9f2c",
    });
    expect(hitDestination(hit("calls", "evt-1"))).toEqual({ href: "/activity/calls/evt-1" });
  });

  it("switches to the hub's own tab for memory and insights", () => {
    expect(hitDestination(hit("memory", "mem-1"))).toEqual({ tab: "memory" });
    expect(hitDestination(hit("insights", "ins-1"))).toEqual({ tab: "insights" });
  });

  // Sources with no portal surface, and an id that could not be interpolated
  // into a route, are not navigable: the drawer shows their metadata instead.
  it("has no destination for a source without a portal surface", () => {
    expect(hitDestination(hit("datahub", "urn:li:dataset:(x,y,PROD)"))).toBeNull();
    expect(hitDestination(hit("endpoints", "GET /orders"))).toBeNull();
    expect(hitDestination(hit("connections", "trino/warehouse"))).toBeNull();
    expect(hitDestination(hit("knowledge_pages", "../../admin"))).toBeNull();
  });
});
