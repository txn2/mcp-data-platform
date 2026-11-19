import { useState } from "react";
import { AssetsTabs } from "@/components/AssetsTabs";
import { Button } from "@/components/ui/button";
import {
  useDeprecateAsset,
  useDismissGroup,
  useDuplicates,
  useTrashAsset,
  type DuplicateGroup,
} from "@/api/portal/hooks/library";
import { useAuthStore } from "@/stores/auth";
import { errorMessage } from "@/lib/utils";
import { EveryoneSwitch } from "./EveryoneSwitch";
import { StatusPill } from "./StatusPill";
import { useIsMobile } from "@/hooks/useIsMobile";

interface Props {
  onNavigate: (path: string) => void;
}

/**
 * DuplicatesPage lists groups of the reader's assets whose name, description
 * and tags are nearly the same (#2509), by the closeness save_asset reports as
 * similar_assets. Keep marks the one to keep; Deprecate sets the others
 * Deprecated with "Duplicate of <kept>"; Delete sends one to the trash; a
 * dismissed group stays dismissed until one of its assets changes.
 */
export function DuplicatesPage({ onNavigate }: Props) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const [everyone, setEveryone] = useState(false);
  const groups = useDuplicates(everyone);
  const dismiss = useDismissGroup();
  const [dismissed, setDismissed] = useState<string | null>(null);
  const error = dismiss.error;

  return (
    <div className="space-y-4">
      <AssetsTabs active="duplicates" onNavigate={onNavigate} />
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="space-y-1">
          <h1 className="text-xl font-semibold">Possible duplicates</h1>
          <p className="text-sm text-muted-foreground">
            Groups of your assets whose content is nearly the same. Keep one and deprecate or delete the others, or
            dismiss the group.
          </p>
        </div>
        {isAdmin && <EveryoneSwitch label="Everyone's" checked={everyone} onChange={setEveryone} />}
      </div>
      {dismissed && (
        <div role="status" className="rounded-lg bg-muted px-3 py-2 text-sm">
          Group dismissed. It stays dismissed until one of its assets changes.{" "}
          <button
            type="button"
            className="font-semibold underline"
            onClick={() => dismiss.mutate({ digest: dismissed, undo: true }, { onSuccess: () => setDismissed(null) })}
          >
            Undo
          </button>
        </div>
      )}
      {error && <p role="alert" className="text-sm text-destructive">{errorMessage(error)}</p>}
      {!groups.isLoading && (groups.data ?? []).length === 0 && (
        <p className="rounded-lg border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
          Empty: no two of your assets look alike.
        </p>
      )}
      {(groups.data ?? []).map((g) => (
        <Group
          key={g.digest}
          group={g}
          showOwner={everyone}
          onNavigate={onNavigate}
          onDismiss={() => dismiss.mutate({ digest: g.digest }, { onSuccess: () => setDismissed(g.digest) })}
        />
      ))}
      <p className="text-xs text-muted-foreground">
        Only your own assets are compared and listed. Similarity is read from the embeddings search already stores;
        the threshold is the one save_asset's similar_assets uses.
      </p>
    </div>
  );
}

function Group({
  group,
  showOwner,
  onNavigate,
  onDismiss,
}: {
  group: DuplicateGroup;
  showOwner: boolean;
  onNavigate: (path: string) => void;
  onDismiss: () => void;
}) {
  const [kept, setKept] = useState<string>(group.members[0]?.id ?? "");
  const deprecate = useDeprecateAsset();
  const trash = useTrashAsset();
  const keptName = group.members.find((m) => m.id === kept)?.name ?? "";
  const failure = deprecate.error ?? trash.error;
  const phone = useIsMobile();
  const actions = (id: string, wide: boolean) => (
    <div className={wide ? "flex gap-2 [&>button]:h-11 [&>button]:flex-1" : "inline-flex gap-2"}>
      <Button variant={kept === id ? "secondary" : "outline"} size="sm" aria-pressed={kept === id} onClick={() => setKept(id)}>
        Keep
      </Button>
      <Button
        variant="outline"
        size="sm"
        disabled={kept === id || deprecate.isPending}
        onClick={() => deprecate.mutate({ id, note: `Duplicate of ${keptName}` })}
      >
        Deprecate
      </Button>
      <Button variant="outline" size="sm" className="text-destructive" disabled={kept === id || trash.isPending} onClick={() => trash.mutate(id)}>
        Delete
      </Button>
    </div>
  );
  return (
    <section
      aria-label={`${group.members.length} assets, ${group.similarity}% similar`}
      className="flex flex-col gap-3 rounded-xl border bg-card p-4"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-base font-semibold">
          {group.members.length} assets, {group.similarity}% similar
        </h2>
        <Button variant="outline" size="sm" onClick={onDismiss}>Dismiss group</Button>
      </div>
      {phone ? (
        // On a phone each asset is a block with its actions full width (#2723).
        <ul className="flex flex-col" data-testid="duplicate-members">
          {group.members.map((m) => (
            <li key={m.id} className="flex flex-col gap-1.5 border-t py-2.5">
              <button type="button" className="text-left text-[15px] leading-snug font-medium" onClick={() => onNavigate(`/assets/${m.id}`)}>
                {m.name}
              </button>
              <span className="text-xs text-muted-foreground">
                {[showOwner ? m.owner_email : "", `${m.opens} ${m.opens === 1 ? "open" : "opens"}`, `Updated ${new Date(m.updated_at).toLocaleDateString(undefined, { month: "short", day: "numeric" })}`, `v${m.version}`].filter(Boolean).join(" · ")}
              </span>
              {actions(m.id, true)}
            </li>
          ))}
        </ul>
      ) : (
      <div className="overflow-x-auto">
        <table className="w-full min-w-[560px] text-sm">
          <thead>
            <tr className="border-b text-left text-xs text-muted-foreground">
              <th className="px-2 py-1.5 font-semibold">Name</th>
              {showOwner && <th className="px-2 py-1.5 font-semibold">Owner</th>}
              <th className="px-2 py-1.5 font-semibold">Updated</th>
              <th className="px-2 py-1.5 text-right font-semibold">Opens</th>
              <th className="px-2 py-1.5" />
            </tr>
          </thead>
          <tbody>
            {group.members.map((m) => (
              <tr key={m.id} className="border-b last:border-b-0">
                <td className="px-2 py-2">
                  <button type="button" className="text-left text-primary hover:underline" onClick={() => onNavigate(`/assets/${m.id}`)}>
                    {m.name}
                  </button>{" "}
                  {m.status && <StatusPill status={m.status} />}
                </td>
                {showOwner && <td className="px-2 py-2 text-muted-foreground">{m.owner_email}</td>}
                <td className="px-2 py-2 text-muted-foreground">
                  {new Date(m.updated_at).toLocaleDateString(undefined, { month: "short", day: "numeric" })} · v{m.version}
                </td>
                <td className="px-2 py-2 text-right">{m.opens}</td>
                <td className="whitespace-nowrap px-2 py-2 text-right">{actions(m.id, false)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      )}
      {failure && <p role="alert" className="text-sm text-destructive">{errorMessage(failure)}</p>}
      <p className="text-xs text-muted-foreground">
        Keep marks the one to keep and leaves it unchanged. Deprecate sets the status to Deprecated with the note
        "Duplicate of {keptName}". Delete sends it to Trash. A group with one asset left disappears.
      </p>
    </section>
  );
}
