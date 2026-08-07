import { useMemo, useState } from "react";
import { BadgeCheck, History, X } from "lucide-react";
import type { Prompt, PromptVersion } from "@/api/admin/types";
import { usePromptVersions } from "@/api/portal/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { diffLines, diffStats } from "@/lib/textDiff";
import { cn } from "@/lib/utils";
import { ListSkeleton } from "../primitives";

// VersionHistory renders the prompt's version list with per-version approval
// provenance and a line diff between any version and the current content
// (#1010). Server access mirrors prompt visibility; a 403 (e.g. a prompt
// shared person-to-person) simply hides the section.
export function VersionHistory({ prompt }: { prompt: Prompt }) {
  const { data, isLoading, isError } = usePromptVersions(prompt.id);
  const [diffVersion, setDiffVersion] = useState<number | null>(null);

  const versions = useMemo(() => data?.data ?? [], [data]);
  const pendingDraft = useMemo(
    () => versions.find((v) => v.status === "draft" && v.version > (prompt.version ?? 0)),
    [versions, prompt.version],
  );
  const selected = useMemo(
    () => (diffVersion !== null ? versions.find((v) => v.version === diffVersion) : undefined),
    [versions, diffVersion],
  );

  if (isError || (!isLoading && versions.length === 0)) {
    return null;
  }

  return (
    <SectionCard
      title={
        <span className="flex items-center gap-2">
          <History className="size-4 text-muted-foreground" />
          Version history
        </span>
      }
    >
      <div className="space-y-3">
        {pendingDraft && (
          <Alert variant="warning" data-testid="pending-draft-banner">
            <AlertDescription className="text-xs">
              Draft v{pendingDraft.version} by {pendingDraft.author} is pending review. Readers are
              served the approved v{prompt.version ?? 1} until an admin approves it.
            </AlertDescription>
          </Alert>
        )}

        {isLoading ? (
          <ListSkeleton rows={3} />
        ) : (
          <ul className="divide-y rounded-lg border">
            {versions.map((v) => (
              <VersionRow
                key={v.id}
                version={v}
                isCurrent={v.version === prompt.version}
                isDiffOpen={diffVersion === v.version}
                onToggleDiff={() =>
                  setDiffVersion((cur) => (cur === v.version ? null : v.version))
                }
              />
            ))}
          </ul>
        )}

        {selected && (
          <VersionDiff
            from={selected}
            currentContent={prompt.content}
            currentVersion={prompt.version ?? 0}
            onClose={() => setDiffVersion(null)}
          />
        )}
      </div>
    </SectionCard>
  );
}

const versionStatusVariants: Record<PromptVersion["status"], "success" | "warning" | "muted" | "danger"> = {
  applied: "success",
  draft: "warning",
  superseded: "muted",
  rejected: "danger",
};

function VersionRow({
  version: v,
  isCurrent,
  isDiffOpen,
  onToggleDiff,
}: {
  version: PromptVersion;
  isCurrent: boolean;
  isDiffOpen: boolean;
  onToggleDiff: () => void;
}) {
  return (
    <li className="flex flex-wrap items-center gap-2 px-4 py-2 text-sm">
      <span className="w-8 font-mono text-xs font-semibold">v{v.version}</span>
      <Badge variant={versionStatusVariants[v.status]} className="text-[11px]">{v.status}</Badge>
      {isCurrent && <Badge variant="info" className="text-[11px]">current</Badge>}
      <span className="text-xs text-muted-foreground">
        {v.author || "unknown"} · {new Date(v.created_at).toLocaleDateString()}
      </span>
      {v.approved_by && (
        <span
          className="inline-flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400"
          title={v.approved_at ? `Approved ${new Date(v.approved_at).toLocaleString()}` : "Approved"}
        >
          <BadgeCheck className="size-3.5" />
          approved by {v.approved_by}
        </span>
      )}
      {/* Non-privileged viewers receive pending drafts as content-less stubs
          (the server redacts never-served content); a diff of a stub would be
          meaningless, so the button requires content. */}
      {v.content !== "" && (
        <Button
          variant="outline"
          size="xs"
          onClick={onToggleDiff}
          className={cn("ml-auto", isDiffOpen && "bg-accent")}
        >
          {isDiffOpen ? "Hide diff" : "Diff vs current"}
        </Button>
      )}
    </li>
  );
}

function VersionDiff({
  from,
  currentContent,
  currentVersion,
  onClose,
}: {
  from: PromptVersion;
  currentContent: string;
  currentVersion: number;
  onClose: () => void;
}) {
  const { lines, stats } = useMemo(() => {
    const l = diffLines(from.content, currentContent);
    return { lines: l, stats: diffStats(l) };
  }, [from.content, currentContent]);

  return (
    <div data-testid="version-diff" className="overflow-hidden rounded-lg border">
      <div className="flex items-center gap-2 border-b bg-muted/40 px-4 py-2 text-xs text-muted-foreground">
        <span className="font-medium text-foreground">
          v{from.version} → v{currentVersion} (current)
        </span>
        <span className="text-emerald-600 dark:text-emerald-400">+{stats.added}</span>
        <span className="text-destructive">-{stats.removed}</span>
        {stats.added === 0 && stats.removed === 0 && <span>content identical</span>}
        <Button
          variant="ghost"
          size="icon-xs"
          onClick={onClose}
          aria-label="Close diff"
          className="ml-auto"
        >
          <X />
        </Button>
      </div>
      <pre className="max-h-96 overflow-auto bg-muted/20 px-0 py-2 font-mono text-xs leading-5">
        {lines.map((l, i) => (
          <div
            key={i}
            className={cn(
              "px-4 break-words whitespace-pre-wrap",
              l.kind === "added" && "bg-green-500/10 text-green-600 dark:text-green-400",
              l.kind === "removed" && "bg-red-500/10 text-red-500 dark:text-red-400",
            )}
          >
            <span className="inline-block w-4 text-muted-foreground/70 select-none">
              {l.kind === "added" ? "+" : l.kind === "removed" ? "-" : " "}
            </span>
            {l.text}
          </div>
        ))}
      </pre>
    </div>
  );
}
