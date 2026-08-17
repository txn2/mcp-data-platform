import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import type { Insight } from "@/api/portal/types";
import { uniqueUrns } from "@/components/knowledge/UrnBadge";
import { InsightCard } from "./cards";

const ORDERS = "urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)";
const REGIONS = "urn:li:dataset:(urn:li:dataPlatform:trino,sales.regions,PROD)";

function insight(overrides: Partial<Insight> = {}): Insight {
  return {
    id: "ins-1",
    session_id: "dps_abc",
    captured_by: "analyst@example.com",
    persona: "data-engineer",
    source: "agent_discovery",
    category: "data_quality",
    insight_text: "Revenue excludes canceled orders.",
    confidence: "high",
    entity_urns: [ORDERS],
    related_columns: [],
    suggested_actions: [],
    status: "pending",
    created_at: "2026-08-16T10:00:00Z",
    ...overrides,
  };
}

afterEach(cleanup);

describe("a record's linked entities", () => {
  // A record is about an entity once. The write path normalizes what it
  // stores, but a row written before that did not, and keying a list on the
  // URN would drop the repeat rather than render it.
  it("names each entity once, however the row repeats it", () => {
    render(<InsightCard insight={insight({ entity_urns: [ORDERS, ORDERS, REGIONS] })} />);

    expect(screen.getAllByTitle(ORDERS)).toHaveLength(1);
    expect(screen.getAllByTitle(REGIONS)).toHaveLength(1);
  });

  it("keeps the order the record named them in, and drops blanks", () => {
    expect(uniqueUrns([REGIONS, ORDERS, REGIONS, "", "   "])).toEqual([REGIONS, ORDERS]);
    expect(uniqueUrns(undefined)).toEqual([]);
  });
});
