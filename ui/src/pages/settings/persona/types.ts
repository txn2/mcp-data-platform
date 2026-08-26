import type { APIRouteRule } from "@/api/admin/types";
import type { RouteResolution } from "./apiRoutes";

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
  // apiRoutes are the persona's per-(connection, method, path) rules for
  // api-kind connections. Objects rather than patterns, because a rule names
  // three things at once and one of them is a path.
  apiRoutes: APIRouteRule[];
  priority: number;
  descriptionPrefix: string;
  descriptionOverride: string;
  agentInstructionsSuffix: string;
  agentInstructionsOverride: string;
}

// Scope selects the axis the allow/deny editor and the explorer address:
// tools, connections, or the operations of an api-kind connection.
// StatusFilter narrows the explorer list.
export type Scope = "tools" | "connections" | "api";
export type StatusFilter = "all" | "allowed" | "denied";

// RouteFocus is the operation the pointer is on in the API-endpoint scope,
// with the decision already resolved so the rail renders rather than recomputes.
export interface RouteFocus {
  connection: string;
  method: string;
  path: string;
  resolution: RouteResolution;
}

// Item is one row in the permissions explorer — a tool or a connection,
// normalized to a common display shape.
export interface Item {
  key: string;
  primary: string;
  secondary: string;
  tertiary: string;
  kind: string;
}
