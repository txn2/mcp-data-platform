import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// ExclusiveMode is whether a script's runs may overlap (#2209).
export type ExclusiveMode = "off" | "refuse" | "queue";

// ScriptExclusiveOutcome is the saved setting and the sentence that states it.
export interface ScriptExclusiveOutcome {
  exclusive: ExclusiveMode;
  message: string;
}

// useSetScriptExclusive sets whether a script's runs may overlap (#1986,
// #2209). It is refused with the server's reason while the runs already open
// break the setting asked for.
export function useSetScriptExclusive(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (exclusive: ExclusiveMode) =>
      apiFetch<ScriptExclusiveOutcome>(`/scripts/${scriptID}/exclusive`, {
        method: "PUT",
        body: JSON.stringify({ exclusive }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}
