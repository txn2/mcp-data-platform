import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// ScriptDeleteOutcome is what the platform answers a removal with: the script
// that is gone and the sentence stating what went with it and what did not.
export interface ScriptDeleteOutcome {
  status: string;
  name: string;
  message: string;
}

// useDeleteScript removes a script (#1575). It is the same removal
// `manage_script command=delete` performs, through the same store, so the
// cascade cannot come out differently depending on which surface asked.
//
// The removed script's own cache entries are DROPPED, and the listing is
// invalidated. Dropping them is about what the reader sees LATER: a contract
// left in the cache would render a deleted script for a moment if somebody
// walked back to its address. It is not a guarantee that nothing re-asks —
// query-core's remove destroys the entry and an observer still mounted rebuilds
// it on its next setOptions — which is why the control navigates away as soon
// as the delete lands rather than relying on the cache to hide the gap.
export function useDeleteScript(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => apiFetch<ScriptDeleteOutcome>(`/scripts/${scriptID}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: [...scriptsKey, scriptID] });
      // Returned rather than fired: the listing is where the caller lands, and
      // awaiting the refresh here is what keeps the deleted script off it.
      return queryClient.invalidateQueries({ queryKey: scriptsKey });
    },
  });
}
