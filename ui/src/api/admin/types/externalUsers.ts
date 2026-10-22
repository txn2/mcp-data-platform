// External accounts (#2092): people outside the organization whose accounts
// the platform creates, under an external persona, signed in by an emailed
// one-time link.

export type ExternalUserStatus = "active" | "suspended" | "expired" | "converted";

export interface ExternalUser {
  id: string;
  email: string;
  name: string;
  persona: string;
  expires_at?: string;
  suspended_at?: string;
  created_by: string;
  created_at: string;
  last_sign_in_at?: string;
  /** When the person's first identity-provider sign-in closed the account. */
  converted_at?: string;
  status: ExternalUserStatus;
  /** The live shares to the account's address. */
  shared_items: number;
}

export interface ExternalUserList {
  users: ExternalUser[];
}

export interface NewExternalUser {
  email: string;
  name: string;
  persona: string;
  expires_at?: string;
}

export interface ExternalAccountSettings {
  session_hours: number;
  invite_hours: number;
  link_minutes: number;
}
