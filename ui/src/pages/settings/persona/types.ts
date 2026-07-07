// Shared types for the persona editor facets. Extracted from PersonaEditor.tsx
// (#766) so the panel, editor, and rule/explorer facets share one definition.

// PersonaDraft mirrors PersonasPanel.PersonaDraft — the flat, editor-facing
// shape of a persona before it is serialized to the admin API payload.
export interface PersonaDraft {
  name: string;
  displayName: string;
  description: string;
  roles: string[];
  allowTools: string[];
  denyTools: string[];
  allowConnections: string[];
  denyConnections: string[];
  priority: number;
  descriptionPrefix: string;
  descriptionOverride: string;
  agentInstructionsSuffix: string;
  agentInstructionsOverride: string;
}

// Scope toggles the allow/deny editor between the tool axis and the
// connection axis; StatusFilter narrows the explorer list.
export type Scope = "tools" | "connections";
export type StatusFilter = "all" | "allowed" | "denied";

// Item is one row in the permissions explorer — a tool or a connection,
// normalized to a common display shape.
export interface Item {
  key: string;
  primary: string;
  secondary: string;
  tertiary: string;
  kind: string;
}
