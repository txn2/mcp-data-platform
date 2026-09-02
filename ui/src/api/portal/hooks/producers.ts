import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";

// What produced a file, and what a script has produced (#1569).
//
// This is a different question from an asset's provenance, which records the
// data calls its content was built from. This records who did the building: the
// scripts, sessions and people that created or modified a portal asset or a
// managed resource, and -- from the other end -- everything one script has
// written across all of its runs.

// ProducerKind is what wrote a file. A managed-script run is a script; every
// other tool call is the session it was made in; a write through the portal's
// own pages is the person who made it.
export type ProducerKind = "script" | "session" | "person";

// ProducedTargetKind is what was written.
export type ProducedTargetKind = "asset" | "resource" | "collection";

// Producer is one writer of one file, as the viewer lists it.
export interface Producer {
  kind: ProducerKind;
  id: string;
  /** A script's current name, or the name it had when it wrote if it has since
   * been deleted; a person's address. Absent for a session, whose id names it. */
  label?: string;
  /** False only where the platform established the producer is gone -- a script
   * id that resolves to nothing. */
  exists: boolean;
  /** Whether this producer brought the file into existence, as against having
   * only changed it since. */
  created: boolean;
  first_write_at: string;
  last_write_at: string;
  write_count: number;
  last_version: number;
}

export interface ProducersResponse {
  data: Producer[];
  total: number;
}

// ProducedItem is one file a script has produced or modified.
export interface ProducedItem {
  target_kind: ProducedTargetKind;
  target_id: string;
  /** Absent when the file no longer exists, which `deleted` reports. */
  name?: string;
  /** The address the file's row records as its owner, for an asset or a
   * collection. A transfer moves the script and, unless asked to, leaves this
   * as it was (#1588), so a file whose owner is not the script's owner is one
   * the script's owner cannot open. Absent for a resource, which is filed by
   * library, and for a deleted file. */
  owner_email?: string;
  created: boolean;
  first_write_at: string;
  last_write_at: string;
  write_count: number;
  last_version: number;
  deleted?: boolean;
}

export interface ProducedResponse {
  data: ProducedItem[];
  total: number;
}

const producersKey = (kind: ProducedTargetKind, id: string) => ["portal", "producers", kind, id];
// scriptProducedKey is the query a script's "Files written" list reads. It is
// exported so the transfer, which can change whose those files are, can
// invalidate it.
export const scriptProducedKey = (scriptId: string) => ["portal", "script-produced", scriptId];

// producersPath is the route that answers "what wrote this?" for either kind.
// One path shape for both keeps the two panels asking the same question.
const producersPath = (kind: ProducedTargetKind, id: string) =>
  kind === "asset" ? `/assets/${id}/producers` : `/resources/${id}/producers`;

// useProducers lists what has written one file, most recent writer first.
// Disabled without an id so a viewer that has not resolved its file can call it.
export function useProducers(kind: ProducedTargetKind, id: string | undefined) {
  return useQuery({
    queryKey: producersKey(kind, id ?? ""),
    enabled: Boolean(id),
    queryFn: () => apiFetch<ProducersResponse>(producersPath(kind, id ?? "")),
  });
}

// useScriptProduced lists everything one script has produced or modified,
// across every run. It is drawn from the producer relation rather than walked
// out of the run history, so a file a run modified without declaring it as an
// output appears too.
export function useScriptProduced(scriptId: string | undefined) {
  return useQuery({
    queryKey: scriptProducedKey(scriptId ?? ""),
    enabled: Boolean(scriptId),
    queryFn: () => apiFetch<ProducedResponse>(`/scripts/${scriptId ?? ""}/produced`),
  });
}
