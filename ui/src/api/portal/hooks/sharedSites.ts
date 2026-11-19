import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";

/**
 * One site environment shared with the reader (#2718): a grant to their
 * email or persona opens it. Nothing else of the site is named.
 */
export interface SharedSiteEnvironment {
  site: string;
  environment: string;
  /** Where the environment is reached; "" when it has no address yet. */
  address: string;
  /** False while the site's managers have taken it offline. */
  online: boolean;
  message?: string;
}

/** The site environments shared with the reader, external people included. */
export function useSharedSites() {
  return useQuery({
    queryKey: ["shared-sites"],
    queryFn: async () => (await apiFetch<{ environments?: SharedSiteEnvironment[] }>("/shared-sites")).environments ?? [],
    staleTime: 60_000,
  });
}
