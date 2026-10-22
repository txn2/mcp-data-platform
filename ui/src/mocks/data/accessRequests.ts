import type { AccessRequest } from "@/api/portal/hooks";

// One request for access waiting on the demo owner (#2090).
export const mockAccessRequests: AccessRequest[] = [
  {
    id: "ar-1",
    target_type: "asset",
    target_id: "ast-001",
    requester_email: "priya.patel@example.com",
    owner_email: "alice@example.com",
    message:
      "I am preparing the quarterly review and need the revenue figures.",
    status: "pending",
    created_at: "2026-10-20T15:04:00Z",
    expires_at: "2026-11-03T15:04:00Z",
    target_name: "Q4 Revenue Dashboard",
  },
];
