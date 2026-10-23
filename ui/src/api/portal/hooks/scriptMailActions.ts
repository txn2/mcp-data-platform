import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// ScriptMailActionsOutcome is the saved setting and the sentence that states it.
export interface ScriptMailActionsOutcome {
  mail_actions: boolean;
  message: string;
}

// useSetScriptMailActions sets whether a script may act on mail it reads
// (#2247). Off, a run that has read mail is refused every later write.
export function useSetScriptMailActions(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (mailActions: boolean) =>
      apiFetch<ScriptMailActionsOutcome>(`/scripts/${scriptID}/mail-actions`, {
        method: "PUT",
        body: JSON.stringify({ mail_actions: mailActions }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}
