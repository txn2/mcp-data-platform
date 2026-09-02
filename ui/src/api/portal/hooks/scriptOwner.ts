import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptProducedKey } from "./producers";
import { scriptsKey } from "./scriptKeys";

// Moving a script to another person (#1404), and what the move does with the
// files the script's runs have already written (#1588). It is an
// administrator's action: ownership is the whole of what a script is, so
// handing it over hands over everything at once, and the run identity is
// re-captured from the administrator making the move. It sits apart from the
// owner's own hooks because it is the one write on a script that is not the
// owner's.

// ScriptOutputDisposition is what a transfer does with the assets and
// collections the script's runs have created (#1588): they move with the
// script, or they stay with whoever owns them now.
export type ScriptOutputDisposition = "move" | "keep";

// ScriptOwnerOutput is one file a transfer left with its previous owner.
export interface ScriptOwnerOutput {
  target_kind: "asset" | "collection";
  target_id: string;
  name?: string;
  owner_email?: string;
}

// ScriptOwnerOutputs is the account of a script's outputs across a transfer:
// how many the disposition applied to, and, when they were kept, which of them
// the new owner cannot open, share or delete.
export interface ScriptOwnerOutputs {
  assets: number;
  collections: number;
  disposition: ScriptOutputDisposition;
  kept?: ScriptOwnerOutput[];
}

// ScriptOwnerOutcome is a completed transfer: where the script landed, the
// version the move recorded, what it means for the next run, and what became
// of the files its runs had already written. `outputs` is absent when there
// were none.
export interface ScriptOwnerOutcome {
  owner_email: string;
  version: number;
  message: string;
  outputs?: ScriptOwnerOutputs;
}

// ScriptOwnerTransferInput is the move as the administrator asked for it. The
// disposition is stated whenever the script has created files; the route
// refuses to move such a script without one, so the control never sends the
// move blind.
export interface ScriptOwnerTransferInput {
  ownerEmail: string;
  outputs?: ScriptOutputDisposition;
}

// useTransferScriptOwner moves a script to another person. It is an
// administrator's action: ownership is the whole of what a script is, so
// handing it over hands over everything at once, and the run identity is
// re-captured from the administrator making the move. The files the script
// wrote are invalidated with the script, because a move can change whose they
// are.
export function useTransferScriptOwner(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ ownerEmail, outputs }: ScriptOwnerTransferInput) =>
      apiFetch<ScriptOwnerOutcome>(`/scripts/${scriptID}/owner`, {
        method: "PUT",
        body: JSON.stringify(
          outputs ? { owner_email: ownerEmail, outputs } : { owner_email: ownerEmail },
        ),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: scriptsKey });
      void queryClient.invalidateQueries({ queryKey: scriptProducedKey(scriptID) });
    },
  });
}

