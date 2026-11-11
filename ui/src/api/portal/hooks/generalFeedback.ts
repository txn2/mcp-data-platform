import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";

// Whether a new General feedback post is told to anyone (#2603). It never
// names who.
export function useGeneralFeedbackReach() {
  return useQuery({
    queryKey: ["feedback", "general-reach"],
    queryFn: () => apiFetch<{ reaches_someone: boolean }>("/feedback/general"),
  });
}
