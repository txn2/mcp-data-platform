import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { BotCheckSettings, BotCheckSettingsInput } from "../types";

// The bot check a public endpoint requires (#2173).

const KEY = ["settings", "bot-check"] as const;

export function useBotCheckSettings() {
  return useQuery({
    queryKey: KEY,
    queryFn: () => apiFetch<BotCheckSettings>("/settings/bot-check"),
  });
}

export function useSetBotCheckSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: BotCheckSettingsInput) =>
      apiFetch<BotCheckSettings>("/settings/bot-check", { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: (data) => qc.setQueryData(KEY, data),
  });
}
