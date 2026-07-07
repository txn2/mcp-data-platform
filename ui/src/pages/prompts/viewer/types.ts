import type { Prompt } from "@/api/admin/types";

// Shared view-model types for the prompt viewer subcomponents. Extracted from
// PromptViewerPage.tsx (#819) so the header, edit form, and read view can share
// them without importing back through the page module.

export type ViewMode = "preview" | "source";

export interface EditForm {
  name: string;
  display_name: string;
  description: string;
  content: string;
  category: string;
  tags: string[];
  arguments: Prompt["arguments"];
}
