import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// ScriptExclusiveOutcome is the saved setting and the sentence that states it.
export interface ScriptExclusiveOutcome {
  exclusive: boolean;
  message: string;
}

// useSetScriptExclusive sets whether a script runs one at a time (#1986). It
// is refused with the server's reason while more than one run is open.
export function useSetScriptExclusive(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (exclusive: boolean) =>
      apiFetch<ScriptExclusiveOutcome>(`/scripts/${scriptID}/exclusive`, {
        method: "PUT",
        body: JSON.stringify({ exclusive }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}
