import type { Secret } from "@/api/admin/types";

// Stored secrets for the demo portal (#2051). No value: the server never
// returns one.
export const mockSecrets: Secret[] = [
  {
    name: "portal_password",
    description:
      "Sign-in for the ACME supplier portal, typed by the weekly invoice download automation.",
    kind: "value",
    allow_connections: ["selenium-grid"],
    allow_personas: [],
    created_by: "admin@example.com",
    updated_by: "admin@example.com",
    created_at: "2026-09-30T14:02:00Z",
    updated_at: "2026-10-06T09:15:00Z",
  },
  {
    name: "billing_api_key",
    description:
      "Key the billing API wants in the request body of its export call.",
    kind: "value",
    allow_connections: ["billing-api"],
    allow_personas: ["admin", "finance"],
    created_by: "admin@example.com",
    updated_by: "ops@example.com",
    created_at: "2026-09-12T10:40:00Z",
    updated_at: "2026-09-12T10:40:00Z",
  },
  {
    // An authenticator seed (#2065): the supplier portal's second factor,
    // whose code the same automation types after the password.
    name: "portal_mfa",
    description: "Authenticator for the ACME supplier portal's second sign-in step.",
    kind: "totp",
    totp: { algorithm: "SHA1", digits: 6, period: 30 },
    allow_connections: ["selenium-grid"],
    allow_personas: [],
    created_by: "admin@example.com",
    updated_by: "admin@example.com",
    created_at: "2026-10-07T16:20:00Z",
    updated_at: "2026-10-07T16:20:00Z",
  },
  {
    // A long description and many connections, the row the table must wrap
    // rather than draw over its neighbours (#2070).
    name: "marketing_service_account",
    description:
      "Service account reports-reader@acme-marketing.iam.gserviceaccount.com, key id 3f9a1c0be27d4a6f, backing the campaign manager, display and video, and display and video reports connections.",
    kind: "value",
    allow_connections: ["cm360", "dv360", "dv360-reports", "ga4-export", "search-ads-360"],
    allow_personas: ["admin", "marketing-analyst"],
    created_by: "admin@example.com",
    updated_by: "ops@example.com",
    created_at: "2026-10-08T11:05:00Z",
    updated_at: "2026-10-09T08:30:00Z",
  },
];
