import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { useAuthStore } from "@/stores/auth";
import { formatWhen } from "./runFormat";

// Automatic approval of a personal script (#1367).
//
// A script only its owner can see and only its owner can run is approved on
// save: the roles it presents were always copied from the version's author, so
// a reviewer of a personal script was narrowing a script against the person it
// belongs to. What the surfaces have to say about it is here, once, so the
// listing, the contract summary, the editor and the version history cannot
// describe the same approval three different ways.

/**
 * useViewerIdentity is the name this caller's scripts are owned by: their email,
 * falling back to their user id when the credential carries no email. It is the
 * same fallback the server applies when it records an owner, so a script
 * authored through an agent is recognized here as the same person's.
 */
export function useViewerIdentity(): string {
  const user = useAuthStore((s) => s.user);
  return user?.email || user?.user_id || "";
}

/**
 * ownedByViewer reports whether this script belongs to the person reading it —
 * which is not the same question as whether they may read it, since an
 * administrator may read everyone's.
 */
export function ownedByViewer(contract: ScriptContract, viewer: string): boolean {
  return !!viewer && contract.owner_email === viewer;
}

/**
 * approvesOnSave reports whether saving an edit here approves it with no
 * reviewer: a personal script, edited by the person who owns it.
 *
 * It is what the page can know before the save. Whether the approval actually
 * lands also depends on the grant being readable from the source, which only the
 * server can answer — so the notice this drives says what will normally happen
 * and the save's own answer says what did.
 */
export function approvesOnSave(contract: ScriptContract, viewer: string): boolean {
  return contract.scope === "personal" && ownedByViewer(contract, viewer);
}

/**
 * approvalFact is the execution gate in one line for the contract summary,
 * naming an approval nobody reviewed as one rather than letting the owner's own
 * address read as a decision somebody made.
 */
export function approvalFact(contract: ScriptContract): string {
  const { approval } = contract;
  if (!approval.approved) return "nothing approved";
  const when = formatWhen(approval.approved_at);
  if (approval.automatic) {
    return `v${approval.version} automatically on ${when} — nobody reviewed it`;
  }
  return `v${approval.version} by ${approval.approved_by || "unknown"} on ${when}`;
}
