import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// ScriptMetadataInput is a change to what a script says about itself. Every
// field is optional and an omitted one is left alone, so a form that edits one
// field cannot blank the others.
export interface ScriptMetadataInput {
  display_name?: string;
  description?: string;
  category?: string;
  tags?: string[];
}

// ScriptMetadataOutcome is the saved state, plus the non-blocking advisory that
// fires when a description has grown into a document of its own (#1369).
export interface ScriptMetadataOutcome {
  version: number;
  description_notice?: string;
  message: string;
}

// useSaveScriptMetadata saves the display name, description, category and tags
// of a script: what it SAYS about itself, not what it does. The change is
// still captured as a version.
export function useSaveScriptMetadata(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: ScriptMetadataInput) =>
      apiFetch<ScriptMetadataOutcome>(`/scripts/${scriptID}/metadata`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}
