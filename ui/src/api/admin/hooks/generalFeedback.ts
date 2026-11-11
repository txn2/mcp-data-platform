import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { GeneralFeedbackSettings, GeneralFeedbackView } from "../types";

// Who hears about a General feedback post (#2603).

const SETTINGS = ["settings", "general-feedback"] as const;
const PATH = "/settings/general-feedback";

export function useGeneralFeedback() {
  return useQuery({
    queryKey: SETTINGS,
    queryFn: () => apiFetch<GeneralFeedbackView>(PATH),
  });
}

export function useSetGeneralFeedback() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: GeneralFeedbackSettings) =>
      apiFetch<GeneralFeedbackView>(PATH, { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: (data) => {
      qc.setQueryData(SETTINGS, data);
      void qc.invalidateQueries({ queryKey: ["feedback", "general-reach"] });
    },
  });
}
