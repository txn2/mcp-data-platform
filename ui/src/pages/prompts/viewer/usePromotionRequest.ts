import { useCallback, useState } from "react";
import { useUpdateMyPrompt } from "@/api/portal/hooks";
import type { Prompt } from "@/api/admin/types";
import { useAuthStore } from "@/stores/auth";

// usePromotionRequest holds everything the "request promotion" flow needs: the
// caller's persona, the chosen target scope, the in-flight request, and its
// error. It is one unit because the dialog is only ever opened, answered, and
// closed together — the viewer page just renders it.
export function usePromotionRequest(prompt: Prompt | undefined) {
  const update = useUpdateMyPrompt();
  const myPersona = useAuthStore((s) => s.user?.persona) ?? "";
  const [open, setOpen] = useState(false);
  const [scope, setScope] = useState<"persona" | "global">("persona");
  const [error, setError] = useState<string | null>(null);

  const openDialog = useCallback(() => {
    setError(null);
    // Default to promoting into the user's own persona; fall back to global if
    // they are not assigned to one.
    setScope(myPersona ? "persona" : "global");
    setOpen(true);
  }, [myPersona]);

  const close = useCallback(() => {
    setOpen(false);
    setError(null);
  }, []);

  const submit = useCallback(() => {
    if (!prompt) return;
    setError(null);
    if (scope === "persona" && !myPersona) {
      setError("You are not assigned to a persona; request global instead.");
      return;
    }
    update.mutate(
      {
        id: prompt.id,
        requested_scope: scope,
        requested_personas: scope === "persona" ? [myPersona] : [],
      },
      {
        onSuccess: () => close(),
        onError: (err) => setError(err instanceof Error ? err.message : "Request failed"),
      },
    );
  }, [prompt, scope, myPersona, update, close]);

  return {
    myPersona,
    open,
    scope,
    setScope,
    error,
    pending: update.isPending,
    openDialog,
    close,
    submit,
  };
}
