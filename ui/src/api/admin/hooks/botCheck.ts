import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";
import type { BotCheck, BotCheckInput, BotCheckList } from "../types";

// The named bot checks a bot_check endpoint picks from (#2173, #2259).

const KEY = ["bot-checks"] as const;

export function useBotChecks() {
  return useQuery({
    queryKey: KEY,
    queryFn: () => apiFetch<BotCheckList>("/bot-checks"),
  });
}

export function usePutBotCheck() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, input }: { name: string; input: BotCheckInput }) =>
      apiFetch<BotCheck>(`/bot-checks/${encodeURIComponent(name)}`, { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEY }),
  });
}

// useDeleteBotCheck deletes a check. One an endpoint names is refused with
// 409, and the error carries the refusal, which names those endpoints.
export function useDeleteBotCheck() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (name: string) => {
      const res = await apiFetchRaw(`/bot-checks/${encodeURIComponent(name)}`, { method: "DELETE" });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { detail?: string };
        throw new Error(body.detail || `delete failed: ${res.status}`);
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: KEY }),
  });
}
