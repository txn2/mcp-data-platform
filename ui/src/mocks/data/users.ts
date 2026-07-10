import type { DirectoryUser } from "@/api/admin/types";
import type { DirectoryUser as PortalDirectoryUser } from "@/api/portal/types";

// ---------------------------------------------------------------------------
// Known-users directory fixture (#614)
//
// Identities reuse the people referenced by the audit fixture
// (src/mocks/data/audit.ts) so the directory stays consistent with the
// activity data shown elsewhere in the demo. The project convention is the
// @example.com domain (matching audit.ts).
//
// Spread: most users are SSO sign-ins (source "auth", confirmed -> Active,
// with a recent last_seen_at). A few are invited-by-email placeholders
// (source "admin", not yet confirmed -> Invited, no last_seen_at, added_by an
// admin) so the Users page exercises both status badges.
// ---------------------------------------------------------------------------

export const mockDirectoryUsers: DirectoryUser[] = [
  {
    email: "sarah.chen@example.com",
    first_name: "Sarah",
    last_name: "Chen",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-28T16:42:00Z",
    created_at: "2025-01-03T09:12:00Z",
    updated_at: "2025-01-28T16:42:00Z",
  },
  {
    email: "lisa.chang@example.com",
    first_name: "Lisa",
    last_name: "Chang",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-28T14:05:00Z",
    created_at: "2025-01-04T11:30:00Z",
    updated_at: "2025-01-28T14:05:00Z",
  },
  {
    email: "david.park@example.com",
    first_name: "David",
    last_name: "Park",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-27T18:20:00Z",
    created_at: "2025-01-04T13:47:00Z",
    updated_at: "2025-01-27T18:20:00Z",
  },
  {
    email: "marcus.johnson@example.com",
    first_name: "Marcus",
    last_name: "Johnson",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-28T09:55:00Z",
    created_at: "2025-01-06T08:15:00Z",
    updated_at: "2025-01-28T09:55:00Z",
  },
  {
    email: "emily.watson@example.com",
    first_name: "Emily",
    last_name: "Watson",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-26T12:38:00Z",
    created_at: "2025-01-07T10:02:00Z",
    updated_at: "2025-01-26T12:38:00Z",
  },
  {
    email: "jennifer.martinez@example.com",
    first_name: "Jennifer",
    last_name: "Martinez",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-28T11:14:00Z",
    created_at: "2025-01-08T15:40:00Z",
    updated_at: "2025-01-28T11:14:00Z",
  },
  {
    email: "kevin.wilson@example.com",
    first_name: "Kevin",
    last_name: "Wilson",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-25T17:03:00Z",
    created_at: "2025-01-09T09:28:00Z",
    updated_at: "2025-01-25T17:03:00Z",
  },
  {
    email: "mike.davis@example.com",
    first_name: "Mike",
    last_name: "Davis",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-27T10:47:00Z",
    created_at: "2025-01-10T14:19:00Z",
    updated_at: "2025-01-27T10:47:00Z",
  },
  {
    email: "rachel.thompson@example.com",
    first_name: "Rachel",
    last_name: "Thompson",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-28T08:31:00Z",
    created_at: "2025-01-13T09:05:00Z",
    updated_at: "2025-01-28T08:31:00Z",
  },
  {
    email: "carlos.rodriguez@example.com",
    first_name: "Carlos",
    last_name: "Rodriguez",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-24T15:22:00Z",
    created_at: "2025-01-14T11:50:00Z",
    updated_at: "2025-01-24T15:22:00Z",
  },
  {
    email: "amanda.lee@example.com",
    first_name: "Amanda",
    last_name: "Lee",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-28T13:09:00Z",
    created_at: "2025-01-15T16:33:00Z",
    updated_at: "2025-01-28T13:09:00Z",
  },
  {
    email: "brian.taylor@example.com",
    first_name: "Brian",
    last_name: "Taylor",
    source: "auth",
    confirmed: true,
    last_seen_at: "2025-01-23T09:44:00Z",
    created_at: "2025-01-16T08:57:00Z",
    updated_at: "2025-01-23T09:44:00Z",
  },
  // Invited-by-email placeholders: added by an admin, not yet signed in.
  {
    email: "priya.patel@example.com",
    first_name: "Priya",
    last_name: "Patel",
    source: "admin",
    confirmed: false,
    added_by: "sarah.chen@example.com",
    created_at: "2025-01-21T10:15:00Z",
    updated_at: "2025-01-21T10:15:00Z",
  },
  {
    email: "thomas.nguyen@example.com",
    first_name: "Thomas",
    last_name: "Nguyen",
    source: "admin",
    confirmed: false,
    added_by: "sarah.chen@example.com",
    created_at: "2025-01-24T14:48:00Z",
    updated_at: "2025-01-24T14:48:00Z",
  },
  {
    email: "olivia.brooks@example.com",
    first_name: "Olivia",
    last_name: "Brooks",
    source: "admin",
    confirmed: false,
    added_by: "marcus.johnson@example.com",
    created_at: "2025-01-27T09:33:00Z",
    updated_at: "2025-01-27T09:33:00Z",
  },
];

// Portal share-picker directory shape is a subset of the admin shape; map from
// the single source of truth above so the two surfaces never drift.
export const mockPortalDirectoryUsers: PortalDirectoryUser[] =
  mockDirectoryUsers.map((u) => ({
    email: u.email,
    first_name: u.first_name,
    last_name: u.last_name,
    confirmed: u.confirmed,
  }));
