import { useState } from "react";
import { Check, ChevronDown } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { useClearAssetStatus, useSetAssetStatus } from "@/api/portal/hooks/assetMarks";
import type { Asset, AssetStatusValue } from "@/api/portal/types";
import { STATUS_LABEL, STATUS_MEANING } from "./AssetMarkBadges";
import { MarkDialog, errorText } from "./MarkDialog";

const STATUSES: AssetStatusValue[] = ["draft", "published", "deprecated"];

/**
 * The Status menu the owner and an editor set an asset's status from (#2502):
 * Draft, Published, Deprecated (which asks for an optional note readers see)
 * and Clear status. A status changes no access, and the menu says so.
 */
export function AssetStatusMenu({ asset }: { asset: Pick<Asset, "id" | "status"> }) {
  const set = useSetAssetStatus();
  const clear = useClearAssetStatus();
  const [deprecating, setDeprecating] = useState(false);
  const [note, setNote] = useState("");
  const current = asset.status?.value;

  function choose(status: AssetStatusValue) {
    if (status === "deprecated") {
      setNote(asset.status?.value === "deprecated" ? (asset.status.note ?? "") : "");
      set.reset();
      setDeprecating(true);
      return;
    }
    set.mutate({ id: asset.id, status });
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" size="sm" data-testid="asset-status-menu">
            Status
            <ChevronDown className="size-3.5" aria-hidden />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-72">
          {STATUSES.map((s) => (
            <DropdownMenuItem key={s} onSelect={() => choose(s)} className="justify-between">
              <span>
                <span className="font-semibold">{STATUS_LABEL[s]}{s === "deprecated" ? "…" : ""}</span>{" "}
                <span className="text-muted-foreground">· {STATUS_MEANING[s]}</span>
              </span>
              {current === s && <Check className="size-4" aria-label="current" />}
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
          <DropdownMenuItem disabled={!current} onSelect={() => clear.mutate(asset.id)}>
            Clear status
          </DropdownMenuItem>
          <p className="px-2 pt-1 pb-1.5 text-xs text-muted-foreground">
            Status changes no access. Everyone it is shared with can still open it.
          </p>
        </DropdownMenuContent>
      </DropdownMenu>
      {(set.error || clear.error) && !deprecating && (
        <span role="alert" className="text-xs text-destructive">
          {errorText(set.error ?? clear.error)}
        </span>
      )}
      <MarkDialog
        open={deprecating}
        onOpenChange={setDeprecating}
        title="Mark as Deprecated"
        description="Readers see the note in a banner above the asset."
        action="Mark as Deprecated"
        pending={set.isPending}
        error={errorText(set.error)}
        onAction={() =>
          set.mutate(
            { id: asset.id, status: "deprecated", note: note.trim() || undefined },
            { onSuccess: () => setDeprecating(false) },
          )
        }
      >
        <label className="flex flex-col gap-1.5 text-sm">
          Note (optional)
          <Textarea
            value={note}
            onChange={(e) => setNote(e.target.value)}
            rows={2}
            maxLength={500}
            placeholder="Replaced by the Q4 revenue report"
            className="field-sizing-fixed resize-none"
          />
        </label>
      </MarkDialog>
    </>
  );
}
