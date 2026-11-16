import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// Site routing (#2659): the routing script's status where a hostname is shown,
// and a retry. Kept apart from sites.ts for its size.

/**
 * The Site routing script's status for one hostname (#2659), present only while
 * an administrator has chosen a routing script. Pending until the script
 * answers; a message the script returned is shown as text. When the script
 * itself failed, message is one sentence and script_failed is set; run_id and
 * run_error reach administrators only.
 */
export interface Routing {
  status: "pending" | "ready" | "failed";
  message: string;
  checked_at?: string;
  script_failed?: boolean;
  run_id?: string;
  /** The script the failing run belongs to, which its page is under. */
  run_script_id?: string;
  run_error?: string;
}

/** Where an application answers: its page origin, its domains and the origin's routing status. */
export interface ApplicationAddresses {
  page_origin: string;
  domains: string[];
  page_origin_routing?: Routing;
}

/** Asks the Site routing script to run now with every hostname (#2659). */
export function useRetryRouting(site: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiFetch<{ site: string; retried: boolean }>(`/sites/${encodeURIComponent(site)}/routing/retry`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["sites"] }),
  });
}

export function useApplicationAddresses(application: string) {
  return useQuery({
    queryKey: ["application-addresses", application],
    queryFn: () => apiFetch<ApplicationAddresses>(`/application-addresses/${encodeURIComponent(application)}`),
    retry: false,
  });
}
