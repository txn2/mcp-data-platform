import { useState } from "react";
import type { ScriptBehaviorDifference } from "@/api/portal/hooks/scripts";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

// ScriptChangeDialog is where the person saving an edit that changes what the
// automation does says what it will now do differently (#1942).
//
// The save was refused because the edit, replayed through the script's recent
// runs or read for what it reaches, does something the saved version does not.
// The person saving is the one agreeing: they read each difference, write the
// change in their own words, and save again. The summary is kept on the version
// with who saved it and when. Nobody else approves it.
export function ScriptChangeDialog({
  differences,
  saving,
  onSave,
  onCancel,
}: {
  differences: ScriptBehaviorDifference[];
  saving: boolean;
  onSave: (summary: string) => void;
  onCancel: () => void;
}) {
  const [summary, setSummary] = useState("");
  const written = summary.trim();
  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>This edit changes what the automation does</DialogTitle>
          <DialogDescription>
            Replaying its recent runs through the edit, or reading what the edit
            reaches, shows it doing something the saved version does not. Say
            what it will now do differently, and saving records that you agreed
            to it.
          </DialogDescription>
        </DialogHeader>
        <ul className="max-h-60 list-disc space-y-1 overflow-y-auto pl-5 text-sm">
          {differences.map((d, i) => (
            <li key={`${d.run ?? ""}-${d.kind}-${d.subject}-${i}`}>
              {d.detail}
              {d.run && (
                <span className="text-muted-foreground"> (run {d.run})</span>
              )}
            </li>
          ))}
        </ul>
        <div className="space-y-1">
          <Label htmlFor="change-summary">What will it do differently?</Label>
          <Textarea
            id="change-summary"
            value={summary}
            onChange={(e) => setSummary(e.target.value)}
            placeholder="The weekly file no longer carries the order count."
            rows={3}
          />
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onCancel} disabled={saving}>
            Cancel
          </Button>
          <Button
            onClick={() => onSave(written)}
            disabled={written === "" || saving}
          >
            {saving ? "Saving..." : "I agree, save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
