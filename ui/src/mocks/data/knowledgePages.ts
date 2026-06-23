import type { KnowledgePage } from "@/api/portal/types";

// mockKnowledgePages is a mutable in-memory store for the knowledge-pages MSW
// handlers (create/update/delete mutate it). Seeded with two canonical pages so
// the browse view is non-empty and content search has something to match.
export const mockKnowledgePages: KnowledgePage[] = [
  {
    id: "kp-seed-1",
    slug: "fiscal-calendar",
    title: "Fiscal Calendar",
    summary: "How the company defines fiscal quarters.",
    body:
      "# Fiscal Calendar\n\nOur fiscal year starts in **February**.\n\n- Q1: February - April\n- Q2: May - July\n- Q3: August - October\n- Q4: November - January\n",
    tags: ["finance", "calendar"],
    created_by: "sarah.chen@example.com",
    updated_by: "sarah.chen@example.com",
    current_version: 2,
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-10T12:00:00Z",
  },
  {
    id: "kp-seed-2",
    slug: "revenue-definition",
    title: "Revenue Definition",
    summary: "What the amount column means.",
    body:
      "# Revenue Definition\n\nThe `amount` column is **gross margin before returns**, not gross revenue. Use `net_revenue` for top-line reporting.\n",
    tags: ["finance", "metrics"],
    created_by: "sarah.chen@example.com",
    updated_by: "sarah.chen@example.com",
    current_version: 1,
    created_at: "2026-06-05T09:00:00Z",
    updated_at: "2026-06-05T09:00:00Z",
  },
];
