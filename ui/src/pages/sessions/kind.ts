import type { SessionKind } from "@/api/admin/types";

// A session id says who minted it, and that is the difference between an
// agent's own working session and one isolated run the platform drove itself.
// The labels are the reader's vocabulary for that distinction; the SQL side
// derives the same four values from the id's prefix.

export const SESSION_KINDS: SessionKind[] = [
  "agent",
  "portal",
  "script",
  "transport",
];

const LABELS: Record<SessionKind, string> = {
  agent: "Agent",
  portal: "Portal run",
  script: "Script run",
  transport: "Transport",
};

const DESCRIPTIONS: Record<SessionKind, string> = {
  agent: "A handle an agent minted with platform_info and threaded across calls",
  portal: "One portal-driven tool run, isolated to a single request",
  script: "One managed-script run",
  transport:
    "A transport-derived session, or a call recorded before explicit handles existed",
};

export function kindLabel(kind: SessionKind): string {
  return LABELS[kind] ?? kind;
}

export function kindDescription(kind: SessionKind): string {
  return DESCRIPTIONS[kind] ?? "";
}

/** Shortens a session id for a table cell, keeping its prefix readable. */
export function shortSessionId(id: string): string {
  return id.length > 18 ? `${id.slice(0, 12)}...${id.slice(-4)}` : id;
}
