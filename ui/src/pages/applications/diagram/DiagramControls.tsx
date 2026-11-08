import { useId } from "react";
import type { ApplicationRelease } from "@/api/portal/hooks/applications";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { versionChoices } from "./diagramModel";

// DiagramControls pick what the Diagram tab draws (#2540): the version (the
// current release, the latest saved versions, or a release), and whether to
// mark what changed since a release.

interface Props {
  releases: ApplicationRelease[];
  version: string;
  comparing: boolean;
  compareWith: string;
  onVersion: (v: string) => void;
  onCompare: (on: boolean) => void;
  onCompareWith: (v: string) => void;
}

export function DiagramControls({ releases, version, comparing, compareWith, onVersion, onCompare, onCompareWith }: Props) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
      <Select value={version} onValueChange={onVersion}>
        <SelectTrigger size="sm" aria-label="Version" className="w-44">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {versionChoices(releases).map((c) => (
            <SelectItem key={c.value} value={c.value}>
              {c.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Switch label="Show changes since" checked={comparing} disabled={releases.length === 0} onChange={onCompare} />
      {comparing && (
        <Select value={compareWith} onValueChange={onCompareWith}>
          <SelectTrigger size="sm" aria-label="Changes since release" className="w-36">
            <SelectValue placeholder="Release" />
          </SelectTrigger>
          <SelectContent>
            {releases.map((r) => (
              <SelectItem key={r.number} value={String(r.number)}>
                Release {r.number}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
    </div>
  );
}

// Switch is one accessible role="switch" button, the portal's hand-built
// toggle (see ConfigToggle in the settings pages).
function Switch({
  label,
  checked,
  disabled,
  onChange,
}: {
  label: string;
  checked: boolean;
  disabled: boolean;
  onChange: (v: boolean) => void;
}) {
  const id = useId();
  return (
    <div className="flex items-center gap-2">
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={cn(
          "relative inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
          disabled ? "cursor-not-allowed opacity-60" : "cursor-pointer",
          checked ? "bg-primary" : "bg-muted",
        )}
      >
        <span
          className={cn(
            "pointer-events-none block h-4 w-4 rounded-full bg-card shadow-sm transition-transform dark:bg-background",
            checked ? "translate-x-4" : "translate-x-0",
          )}
        />
      </button>
      <Label htmlFor={id} className="text-sm font-normal">
        {label}
      </Label>
    </div>
  );
}
