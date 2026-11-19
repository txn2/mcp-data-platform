import { useState } from "react";
import { Check, ChevronDown } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { endOfDayUTC, useUnverifyAsset, useVerifyAsset } from "@/api/portal/hooks/assetMarks";
import type { Asset, AssetVerification } from "@/api/portal/types";
import { MarkDialog, errorText } from "./MarkDialog";

type VerifiableAsset = Pick<Asset, "id" | "name" | "current_version" | "verification">;

/**
 * Whether the signed-in person may remove a verification: a holder of the
 * permission (an administrator holds it), or the person who verified it.
 */
export function mayRemoveVerification(canVerify: boolean, verifiedBy: string | undefined, email: string | undefined): boolean {
  if (canVerify) return true;
  return !!verifiedBy && !!email && verifiedBy.toLowerCase() === email.toLowerCase();
}

/** The date part (YYYY-MM-DD) of an expiry, for the dialog's field. */
function expiryDate(iso: string | undefined): string {
  return iso ? iso.slice(0, 10) : "";
}

/**
 * The Verify control (#2503). A holder of Can verify assets (or an
 * administrator) sees Verify on an unverified asset and a Verified menu on a
 * verified one: Verify version N, Change note or expiry, Remove verification.
 * The person who verified, without the permission any longer, sees the menu
 * with Remove only. Anyone else sees no control; the badge is the header's.
 */
export function VerifyControls({ asset, canVerify, email }: { asset: VerifiableAsset; canVerify: boolean; email?: string }) {
  const unverify = useUnverifyAsset();
  const [dialog, setDialog] = useState<{ keep: boolean } | null>(null);
  const v = asset.verification;
  const canRemove = !!v && mayRemoveVerification(canVerify, v.verified_by, email);

  if (!canVerify && !canRemove) return null;

  return (
    <>
      {v ? (
        <VerifiedMenu
          version={asset.current_version}
          canVerify={canVerify}
          onVerify={(keep) => setDialog({ keep })}
          onRemove={() => unverify.mutate(asset.id)}
        />
      ) : (
        <Button variant="outline" size="sm" onClick={() => setDialog({ keep: false })} data-testid="asset-verify">
          Verify
        </Button>
      )}
      {unverify.error && (
        <span role="alert" className="text-xs text-destructive">
          {errorText(unverify.error)}
        </span>
      )}
      {dialog && <VerifyDialog asset={asset} keep={dialog.keep ? v : undefined} onClose={() => setDialog(null)} />}
    </>
  );
}

function VerifiedMenu({
  version,
  canVerify,
  onVerify,
  onRemove,
}: {
  version: number;
  canVerify: boolean;
  onVerify: (keep: boolean) => void;
  onRemove: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" data-testid="asset-verified-menu">
          <Check className="size-3.5" strokeWidth={3} aria-hidden />
          Verified
          <ChevronDown className="size-3.5" aria-hidden />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        {canVerify && <DropdownMenuItem onSelect={() => onVerify(false)}>Verify version {version}</DropdownMenuItem>}
        {canVerify && <DropdownMenuItem onSelect={() => onVerify(true)}>Change note or expiry</DropdownMenuItem>}
        <DropdownMenuItem className="text-destructive" onSelect={onRemove}>
          Remove verification
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * The Verify dialog: an optional note, and an expiry of Never or a date, which
 * is the end of that day in UTC. `keep` seeds it from the verification being
 * changed.
 */
function VerifyDialog({ asset, keep, onClose }: { asset: VerifiableAsset; keep?: AssetVerification; onClose: () => void }) {
  const verify = useVerifyAsset();
  const [note, setNote] = useState(keep?.note ?? "");
  const [date, setDate] = useState(expiryDate(keep?.expires_at));
  const [expires, setExpires] = useState<"never" | "on">(keep?.expires_at ? "on" : "never");
  const dateRequired = expires === "on" && !endOfDayUTC(date);

  return (
    <MarkDialog
      open
      onOpenChange={(open) => !open && onClose()}
      title={`Verify "${asset.name}"`}
      description={`Readers see that you checked version ${asset.current_version}. A newer version keeps the badge with "Changed since verified".`}
      action="Verify"
      pending={verify.isPending}
      error={dateRequired ? null : errorText(verify.error)}
      onAction={() => {
        if (dateRequired) return;
        verify.mutate(
          { id: asset.id, note: note.trim() || undefined, expires_at: expires === "on" ? endOfDayUTC(date) : null },
          { onSuccess: onClose },
        );
      }}
    >
      <label className="flex flex-col gap-1.5 text-sm">
        Note (optional)
        <Textarea
          value={note}
          onChange={(e) => setNote(e.target.value)}
          rows={2}
          maxLength={500}
          placeholder="Checked against the finance close."
          className="field-sizing-fixed resize-none"
        />
      </label>
      <fieldset className="space-y-2 text-sm">
        <legend className="mb-1.5">Expires</legend>
        <label className="flex items-center gap-2">
          <input type="radio" name="verify-expires" checked={expires === "never"} onChange={() => setExpires("never")} />
          Never
        </label>
        <label className="flex items-center gap-2">
          <input type="radio" name="verify-expires" checked={expires === "on"} onChange={() => setExpires("on")} />
          On
          <Input
            type="date"
            aria-label="Expiry date"
            value={date}
            onChange={(e) => {
              setDate(e.target.value);
              setExpires("on");
            }}
            className="h-8 w-40"
          />
        </label>
        {dateRequired && <p className="text-xs text-destructive">Choose the date it expires, or Never.</p>}
      </fieldset>
      <p className="text-xs text-muted-foreground">
        You and the owner get a reminder 7 days before it expires. When it passes, the badge is removed.
      </p>
    </MarkDialog>
  );
}
