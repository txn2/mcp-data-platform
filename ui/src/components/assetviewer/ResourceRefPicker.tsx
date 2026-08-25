import { useMemo, useState } from "react";
import { useAddAssetResource, type RefAudience } from "@/api/portal/hooks/assetResources";
import { useResources } from "@/api/resources/hooks";
import type { Resource } from "@/api/resources/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScopeChip } from "./ResourceRefScopeChip";

// PICKER_PAGE_SIZE is how many candidates the picker asks for. It is the
// server's own list cap rather than a smaller number, so the one page it shows
// is as much as a single read can carry; anything past it is reached by
// searching, and the picker says when there is anything past it.
const PICKER_PAGE_SIZE = 100;

// RefPicker chooses a file from the resources this reader can open, stating
// what the reference gives away before it is made.
//
// It does not pre-filter by scope. The server owns the read rule, and showing a
// candidate with its scope chip plus the server's own refusal teaches the rule
// better than silently hiding it.
export function RefPicker({
  assetId,
  referenced,
  audience,
  notice,
  full,
  onClose,
  onError,
}: {
  assetId: string;
  referenced: string[];
  audience: RefAudience;
  notice: string;
  full: boolean;
  onClose: () => void;
  onError: (msg: string | null) => void;
}) {
  const [query, setQuery] = useState("");
  const { data, isLoading } = useResources({
    q: query.trim() || undefined,
    limit: PICKER_PAGE_SIZE,
  });
  const add = useAddAssetResource(assetId);

  // A file this asset already references is not offered again: referencing it
  // twice is not a thing an asset can do, and the server refuses it.
  const candidates = useMemo(
    () => (data?.resources ?? []).filter((r) => !referenced.includes(r.id)),
    [data, referenced],
  );
  // The picker shows one page. A library larger than it holds files this list
  // cannot reach, and CandidateList says so rather than letting a reader
  // conclude the file is not there.
  const shown = data?.resources.length ?? 0;

  return (
    <div className="space-y-2 rounded-lg border bg-muted/30 p-3" data-testid="asset-resource-picker">
      <AudienceNotice audience={audience} notice={notice} />

      {full ? (
        <p className="text-xs text-muted-foreground">
          This asset already references the maximum number of files. Remove one to add another.
        </p>
      ) : (
        <>
          <div className="flex items-center gap-2">
            <Input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search resources"
              aria-label="Search resources"
              className="h-8 min-w-0 flex-1 text-xs md:text-xs"
            />
            <Button variant="outline" size="sm" onClick={onClose}>
              Done
            </Button>
          </div>

          <CandidateList
            candidates={candidates}
            isLoading={isLoading}
            shown={shown}
            total={data?.total ?? 0}
            pending={add.isPending}
            onPick={(id) => {
              onError(null);
              add.mutate(id, {
                onError: (err) => onError(err instanceof Error ? err.message : "Add failed"),
              });
            }}
          />
        </>
      )}
    </div>
  );
}

// CandidateList is the picker's result set in each state it can be in: loading,
// empty, and listed with or without a note that it was cut at one page.
//
// The cut is stated rather than left implicit. A library larger than one page
// holds files this list cannot reach, and "no resources match" told to someone
// whose file is on page two is simply false.
function CandidateList({
  candidates,
  isLoading,
  shown,
  total,
  pending,
  onPick,
}: {
  candidates: Resource[];
  isLoading: boolean;
  shown: number;
  total: number;
  pending: boolean;
  onPick: (resourceId: string) => void;
}) {
  const cut = total > shown;
  if (isLoading) {
    return <p className="text-xs text-muted-foreground">Loading resources...</p>;
  }
  return (
    <>
      {candidates.length === 0 && (
        <p className="text-xs text-muted-foreground">
          {cut
            ? "No match among the files shown. Narrow the search to reach the rest."
            : "No resources match. Upload files on the Resources page first."}
        </p>
      )}

      {cut && candidates.length > 0 && (
        <p className="text-xs text-muted-foreground" data-testid="asset-resource-picker-cut">
          Showing {shown} of {total}. Search to reach the rest.
        </p>
      )}

      <ul className="max-h-64 space-y-1 overflow-y-auto">
        {candidates.map((r) => (
          <li key={r.id}>
            <Button
              variant="ghost"
              disabled={pending}
              onClick={() => onPick(r.id)}
              // Two lines rather than one row: the sidebar is narrow, and a
              // scope chip reading "persona: data-engineer" beside a mime
              // type squeezed the file's own name to nothing.
              className="h-auto w-full flex-col items-start gap-1 px-2 py-1.5 font-normal"
            >
              <span className="w-full truncate text-left text-xs">
                {r.display_name || r.filename}
              </span>
              <span className="flex w-full items-center gap-2 text-xs text-muted-foreground">
                <ScopeChip scope={r.scope} scopeId={r.scope_id} />
                <span className="truncate">{r.mime_type}</span>
              </span>
            </Button>
          </li>
        ))}
      </ul>
    </>
  );
}

// AudienceNotice states what referencing a file means, in this asset's terms:
// the rule the platform applies, plus who this asset is currently shared with.
function AudienceNotice({ audience, notice }: { audience: RefAudience; notice: string }) {
  return (
    <div className="space-y-1 text-xs" data-testid="asset-resource-audience">
      <p className="text-muted-foreground">{notice}</p>
      <p className="font-medium">{audienceSentence(audience)}</p>
      {/*
        The one thing adding a reference does NOT do. The declaration and the
        markup are separate, so a file added here shows up nowhere until its URI
        is written into the content -- which the row's copy control is for.
      */}
      <p className="text-muted-foreground">
        Adding a file does not change this asset&apos;s content. Copy its URI from the list and
        write it where the file should appear.
      </p>
    </div>
  );
}

// audienceSentence names this asset's current audience, which is the audience a
// referenced file inherits.
function audienceSentence(audience: RefAudience): string {
  if (audience.public) {
    return "This asset has a public link, so anyone holding it can load the files you add here.";
  }
  if (audience.shared_with_users) {
    return "This asset is shared with other people, who can load the files you add here.";
  }
  return "This asset is not shared with anyone yet. If you share it later, the files you add here go with it.";
}
