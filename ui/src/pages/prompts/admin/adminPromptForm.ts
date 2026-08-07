import type { Prompt } from "@/api/admin/types";

// The admin prompt editor's form model, kept out of the dialog so the shape,
// its defaults, and the lifecycle rule it obeys are stated once.

export type PromptStatus = NonNullable<Prompt["status"]>;

export interface FormData {
  name: string;
  display_name: string;
  description: string;
  content: string;
  category: string;
  scope: Prompt["scope"];
  personas: string;
  tags: string[];
  status: PromptStatus;
  superseded_by: string;
  owner_email: string;
  enabled: boolean;
}

export const emptyForm: FormData = {
  name: "",
  display_name: "",
  description: "",
  content: "",
  category: "",
  scope: "global",
  personas: "",
  tags: [],
  status: "draft",
  superseded_by: "",
  owner_email: "",
  enabled: true,
};

// formFromPrompt seeds the editor from the prompt being edited. Personas round
// -trip as the comma-separated string the field edits.
export function formFromPrompt(p: Prompt): FormData {
  return {
    name: p.name,
    display_name: p.display_name,
    description: p.description,
    content: p.content,
    category: p.category,
    scope: p.scope,
    personas: (p.personas ?? []).join(", "),
    tags: p.tags ?? [],
    status: p.status ?? "draft",
    superseded_by: p.superseded_by ?? "",
    owner_email: p.owner_email,
    enabled: p.enabled,
  };
}

// validStatusNext mirrors the server-side prompt lifecycle state machine
// (pkg/prompt/prompt.go validStatusTransitions). The select offers only the
// current status plus its reachable successors so the server never rejects the
// choice with a 400.
const validStatusNext: Record<PromptStatus, PromptStatus[]> = {
  draft: ["approved", "superseded"],
  approved: ["deprecated", "superseded"],
  deprecated: ["superseded"],
  superseded: [],
};

export function statusOptionsFor(current: PromptStatus): PromptStatus[] {
  return [current, ...validStatusNext[current]];
}

// parsePersonas turns the comma-separated field back into the list the API
// takes.
export function parsePersonas(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}
