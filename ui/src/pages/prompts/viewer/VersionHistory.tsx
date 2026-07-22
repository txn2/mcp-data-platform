import { useMemo, useState } from "react";
import { BadgeCheck, History, X } from "lucide-react";
import type { Prompt, PromptVersion } from "@/api/admin/types";
import { usePromptVersions } from "@/api/portal/hooks";
import { diffLines, diffStats } from "@/lib/textDiff";
import { cn } from "@/lib/utils";

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
    <div className="rounded-lg border bg-card">
      <div className="flex items-center gap-2 border-b px-4 py-2.5 text-sm font-semibold">
        <History className="h-4 w-4 text-muted-foreground" />
        Version history
        {isLoading && <span className="text-xs font-normal text-muted-foreground">Loading...</span>}
      </div>

      {pendingDraft && (
        <div
          data-testid="pending-draft-banner"
          className="mx-4 mt-3 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-400"
        >
          Draft v{pendingDraft.version} by {pendingDraft.author} is pending review. Readers are
          served the approved v{prompt.version ?? 1} until an admin approves it.
        </div>
      )}

      {!isLoading && (
        <ul className="divide-y">
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
  );
}

const versionStatusStyles: Record<PromptVersion["status"], string> = {
  applied: "bg-green-500/10 text-green-500 border-green-500/20",
  draft: "bg-amber-500/10 text-amber-400 border-amber-500/20",
  superseded: "bg-zinc-500/10 text-zinc-400 border-zinc-500/20",
  rejected: "bg-red-500/10 text-red-400 border-red-500/20",
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
      <span className="font-mono text-xs font-semibold w-8">v{v.version}</span>
      <span className={cn("inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium", versionStatusStyles[v.status])}>
        {v.status}
      </span>
      {isCurrent && (
        <span className="inline-flex items-center rounded-full border border-blue-500/20 bg-blue-500/10 px-2 py-0.5 text-[11px] font-medium text-blue-400">
          current
        </span>
      )}
      <span className="text-xs text-muted-foreground">
        {v.author || "unknown"} · {new Date(v.created_at).toLocaleDateString()}
      </span>
      {v.approved_by && (
        <span className="inline-flex items-center gap-1 text-xs text-green-500" title={v.approved_at ? `Approved ${new Date(v.approved_at).toLocaleString()}` : "Approved"}>
          <BadgeCheck className="h-3.5 w-3.5" />
          approved by {v.approved_by}
        </span>
      )}
      {/* Non-privileged viewers receive pending drafts as content-less stubs
          (the server redacts never-served content); a diff of a stub would be
          meaningless, so the button requires content. */}
      {v.content !== "" && (
        <button
          onClick={onToggleDiff}
          className={cn(
            "ml-auto rounded-md border px-2 py-1 text-xs font-medium hover:bg-accent",
            isDiffOpen && "bg-accent",
          )}
        >
          {isDiffOpen ? "Hide diff" : "Diff vs current"}
        </button>
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
    <div data-testid="version-diff" className="border-t">
      <div className="flex items-center gap-2 px-4 py-2 text-xs text-muted-foreground">
        <span className="font-medium text-foreground">
          v{from.version} → v{currentVersion} (current)
        </span>
        <span className="text-green-500">+{stats.added}</span>
        <span className="text-red-400">-{stats.removed}</span>
        {stats.added === 0 && stats.removed === 0 && <span>content identical</span>}
        <button onClick={onClose} className="ml-auto rounded-md p-1 hover:bg-accent" aria-label="Close diff">
          <X className="h-3.5 w-3.5" />
        </button>
      </div>
      <pre className="max-h-96 overflow-auto bg-muted/20 px-0 py-2 text-xs font-mono leading-5">
        {lines.map((l, i) => (
          <div
            key={i}
            className={cn(
              "px-4 whitespace-pre-wrap break-words",
              l.kind === "added" && "bg-green-500/10 text-green-600 dark:text-green-400",
              l.kind === "removed" && "bg-red-500/10 text-red-500 dark:text-red-400",
            )}
          >
            <span className="select-none inline-block w-4 text-muted-foreground/70">
              {l.kind === "added" ? "+" : l.kind === "removed" ? "-" : " "}
            </span>
            {l.text}
          </div>
        ))}
      </pre>
    </div>
  );
}
