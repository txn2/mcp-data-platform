import { errorMessage } from "@/lib/utils";

/** ActionError says why an action on a library page failed; nothing when none did. */
export function ActionError({ error }: { error: unknown }) {
  if (!error) return null;
  return <p role="alert" className="text-sm text-destructive">{errorMessage(error)}</p>;
}
