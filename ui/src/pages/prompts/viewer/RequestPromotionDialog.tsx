import type { Dispatch, SetStateAction } from "react";
import { Users, Globe, ArrowUpCircle } from "lucide-react";
import { ModalScroll } from "@/components/ModalShell";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { FormError } from "../primitives";

// RequestPromotionDialog is the modal an owner uses to request an admin promote
// their personal prompt to a persona or global scope. Rendered only while open
// by the parent.
export function RequestPromotionDialog({
  myPersona,
  promoteScope,
  setPromoteScope,
  promoteError,
  pending,
  onCancel,
  onSubmit,
}: {
  myPersona: string;
  promoteScope: "persona" | "global";
  setPromoteScope: Dispatch<SetStateAction<"persona" | "global">>;
  promoteError: string | null;
  pending: boolean;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  return (
    <ModalScroll onClose={onCancel} width="max-w-sm" label="Request promotion" busy={pending}>
      <div className="space-y-4 rounded-lg border bg-card p-6 shadow-lg">
        <div>
          <h3 className="text-sm font-semibold">Request promotion</h3>
          <p className="text-sm text-muted-foreground">
            An admin will review and approve. Until then your prompt stays
            personal.
          </p>
        </div>
        <div className="space-y-2">
          <label
            className={cn(
              "flex items-center gap-2 text-sm",
              myPersona ? "cursor-pointer" : "cursor-not-allowed opacity-50",
            )}
          >
            <input
              type="radio"
              name="promote-scope"
              checked={promoteScope === "persona"}
              disabled={!myPersona}
              onChange={() => setPromoteScope("persona")}
            />
            <Users className="size-4 text-purple-500" />
            My persona{" "}
            <span className="text-muted-foreground">
              ({myPersona || "you are not in a persona"})
            </span>
          </label>
          <label className="flex cursor-pointer items-center gap-2 text-sm">
            <input
              type="radio"
              name="promote-scope"
              checked={promoteScope === "global"}
              onChange={() => setPromoteScope("global")}
            />
            <Globe className="size-4 text-blue-500" /> Global (everyone)
          </label>
        </div>
        <FormError message={promoteError} />
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
          <Button onClick={onSubmit} disabled={pending}>
            <ArrowUpCircle /> {pending ? "Submitting..." : "Submit request"}
          </Button>
        </div>
      </div>
    </ModalScroll>
  );
}
