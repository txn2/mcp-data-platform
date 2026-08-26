import { useMemo, useState } from "react";
import {
  useAddAssetRef,
  type RefAudience,
  type RefTarget,
  type RefTargetKind,
} from "@/api/portal/hooks/assetRefs";
import { useAssets, useSearchAssets, useSharedWithMe } from "@/api/portal/hooks/assets";
import { useResources } from "@/api/resources/hooks";
import type { Resource } from "@/api/resources/types";
import type { Asset } from "@/api/portal/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScopeChip } from "./ResourceRefScopeChip";

// PICKER_PAGE_SIZE is how many candidates the picker asks for. It is the
// server's own list cap rather than a smaller number, so the one page it shows
// is as much as a single read can carry; anything past it is reached by
// searching, and the picker says when there is anything past it.
const PICKER_PAGE_SIZE = 100;

// candidate is one thing the picker can offer, in the two facts a row shows:
// what it is called, and one line of context under it.
interface candidate {
  id: string;
  name: string;
  detail: React.ReactNode;
}

// RefPicker chooses what to reference from the things this reader can open,
// stating what the reference gives away before it is made.
//
// It offers two kinds because a reference has two (#1488): an uploaded file,
// and another asset whose current content the reference resolves to. They are
// one picker with a tab rather than two controls, because the author is
// answering one question -- what does this content name? -- and the grant they
// make is the same either way.
//
// It does not pre-filter by scope or ownership. The server owns the read rule,
// and showing a candidate with its scope chip plus the server's own refusal
// teaches the rule better than silently hiding it.
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
  /** The targets this asset already references, which are not offered again. */
  referenced: RefTarget[];
  audience: RefAudience;
  notice: string;
  full: boolean;
  onClose: () => void;
  onError: (msg: string | null) => void;
}) {
  const [kind, setKind] = useState<RefTargetKind>("resource");
  const [query, setQuery] = useState("");
  const add = useAddAssetRef(assetId);
  const offered = useOffered({ kind, query, assetId, referenced });

  return (
    <div className="space-y-2 rounded-lg border bg-muted/30 p-3" data-testid="asset-ref-picker">
      <AudienceNotice audience={audience} notice={notice} />

      {full ? (
        <p className="text-xs text-muted-foreground">
          This asset already references the maximum number of things. Remove one to add another.
        </p>
      ) : (
        <>
          <Tabs
            value={kind}
            onValueChange={(v) => {
              setKind(v as RefTargetKind);
              setQuery("");
              onError(null);
            }}
          >
            <TabsList className="h-7">
              <TabsTrigger value="resource" className="text-xs">
                Files
              </TabsTrigger>
              <TabsTrigger value="asset" className="text-xs">
                Assets
              </TabsTrigger>
            </TabsList>
          </Tabs>

          <div className="flex items-center gap-2">
            <Input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={kind === "asset" ? "Search assets" : "Search resources"}
              aria-label={kind === "asset" ? "Search assets" : "Search resources"}
              className="h-8 min-w-0 flex-1 text-xs md:text-xs"
            />
            <Button variant="outline" size="sm" onClick={onClose}>
              Done
            </Button>
          </div>

          <CandidateList
            kind={kind}
            candidates={offered.candidates}
            isLoading={offered.loading}
            shown={offered.shown}
            total={offered.total}
            pending={add.isPending}
            onPick={(id) => {
              onError(null);
              add.mutate(
                { kind, id },
                {
                  onError: (err) => onError(err instanceof Error ? err.message : "Add failed"),
                },
              );
            }}
          />
        </>
      )}
    </div>
  );
}

// offered is one tab's answer: what this reader may pick, how much of the
// library that was, and whether it has arrived.
interface offered {
  candidates: candidate[];
  shown: number;
  total: number;
  loading: boolean;
}

// useOffered reads the tab's library and narrows it to what may be picked.
//
// Both libraries are read on every render rather than one per tab, which is
// what lets the tab switch answer immediately from cache; the tab decides which
// answer is shown, not which question is asked.
function useOffered(args: {
  kind: RefTargetKind;
  query: string;
  assetId: string;
  referenced: RefTarget[];
}): offered {
  const taken = useMemo(
    () => new Set(args.referenced.map((r) => `${r.kind}:${r.id}`)),
    [args.referenced],
  );
  const resources = useResourceOffered(args.query, taken);
  const assets = useAssetOffered(args.query, args.assetId, taken);
  return args.kind === "asset" ? assets : resources;
}

// useResourceOffered is the uploaded-file library, minus what this asset
// already references: referencing something twice is not a thing an asset can
// do, and the server refuses it.
function useResourceOffered(query: string, taken: Set<string>): offered {
  const { data, isLoading } = useResources({
    q: query.trim() || undefined,
    limit: PICKER_PAGE_SIZE,
  });
  const rows = useMemo(() => data?.resources ?? [], [data]);
  const candidates = useMemo(
    () => rows.filter((r) => !taken.has(`resource:${r.id}`)).map(resourceCandidate),
    [rows, taken],
  );
  return { candidates, shown: rows.length, total: data?.total ?? 0, loading: isLoading };
}

// useAssetOffered is the asset library on the same terms, minus the asset being
// edited: an asset that referenced itself would resolve to the content the
// reference was written in, and the server refuses that too.
//
// It reads what the reader may OPEN rather than only what they own, because
// that is what the add accepts: the owned list and the shared-with-me list, in
// that order. A picker built on the owned list alone would tell someone to ask
// for a share and then never offer the asset once they had one.
//
// Typing switches to the ranked search, which is the asset library's own read
// and covers both. Searching a library of any size by substring in the browser
// would find the wrong things and miss the right ones.
function useAssetOffered(query: string, assetId: string, taken: Set<string>): offered {
  const trimmed = query.trim();
  const listed = useAssets({ limit: PICKER_PAGE_SIZE });
  const shared = useSharedWithMe({ limit: PICKER_PAGE_SIZE });
  const searched = useSearchAssets(trimmed, { limit: PICKER_PAGE_SIZE });

  // The ranked search answers with scored wrappers and the two lists with
  // assets, so each is unwrapped where it is read rather than at the row. The
  // two lists are deduplicated by id: an asset can appear in both -- a share
  // addressed to its own owner is enough -- and the same asset offered twice
  // is two rows that add the same reference.
  const rows = useMemo<Asset[]>(() => {
    if (trimmed) return (searched.data?.data ?? []).map((hit) => hit.asset);
    const own = listed.data?.data ?? [];
    const withMe = (shared.data?.data ?? []).map((s) => s.asset);
    return dedupeByID([...own, ...withMe]);
  }, [trimmed, searched.data, listed.data, shared.data]);

  const candidates = useMemo(
    () =>
      rows
        .filter((a) => a.id !== assetId && !taken.has(`asset:${a.id}`))
        .map(assetCandidate),
    [rows, assetId, taken],
  );

  const total = trimmed
    ? (searched.data?.total ?? 0)
    : (listed.data?.total ?? 0) + (shared.data?.total ?? 0);
  const loading = trimmed ? searched.isLoading : listed.isLoading || shared.isLoading;
  return { candidates, shown: rows.length, total, loading };
}

// dedupeByID keeps the first occurrence of each asset, so one thing listed
// twice is offered once.
function dedupeByID(assets: Asset[]): Asset[] {
  const seen = new Set<string>();
  return assets.filter((a) => {
    if (seen.has(a.id)) return false;
    seen.add(a.id);
    return true;
  });
}

// resourceCandidate renders one uploaded file's row: its scope and its type.
function resourceCandidate(r: Resource): candidate {
  return {
    id: r.id,
    name: r.display_name || r.filename,
    detail: (
      <>
        <ScopeChip scope={r.scope} scopeId={r.scope_id} />
        <span className="truncate">{r.mime_type}</span>
      </>
    ),
  };
}

// assetCandidate renders one asset's row. The content type is what tells an
// author whether the thing they are about to reference is the data file they
// meant or the report that reads it.
function assetCandidate(a: Asset): candidate {
  return {
    id: a.id,
    name: a.name,
    detail: (
      <>
        <span className="truncate">{a.content_type}</span>
        {a.owner_email && <span className="truncate">{a.owner_email}</span>}
      </>
    ),
  };
}

// CandidateList is the picker's result set in each state it can be in: loading,
// empty, and listed with or without a note that it was cut at one page.
//
// The cut is stated rather than left implicit. A library larger than one page
// holds things this list cannot reach, and "nothing matches" told to someone
// whose file is on page two is simply false.
function CandidateList({
  kind,
  candidates,
  isLoading,
  shown,
  total,
  pending,
  onPick,
}: {
  kind: RefTargetKind;
  candidates: candidate[];
  isLoading: boolean;
  shown: number;
  total: number;
  pending: boolean;
  onPick: (targetId: string) => void;
}) {
  const cut = total > shown;
  const noun = kind === "asset" ? "assets" : "resources";
  if (isLoading) {
    return <p className="text-xs text-muted-foreground">Loading {noun}...</p>;
  }
  return (
    <>
      {candidates.length === 0 && (
        <p className="text-xs text-muted-foreground">
          {cut
            ? `No match among the ${noun} shown. Narrow the search to reach the rest.`
            : emptyMessage(kind)}
        </p>
      )}

      {cut && candidates.length > 0 && (
        <p className="text-xs text-muted-foreground" data-testid="asset-ref-picker-cut">
          Showing {shown} of {total}. Search to reach the rest.
        </p>
      )}

      <ul className="max-h-64 space-y-1 overflow-y-auto">
        {candidates.map((c) => (
          <li key={c.id}>
            <Button
              variant="ghost"
              disabled={pending}
              onClick={() => onPick(c.id)}
              // Two lines rather than one row: the sidebar is narrow, and a
              // scope chip reading "persona: data-engineer" beside a mime
              // type squeezed the file's own name to nothing.
              className="h-auto w-full flex-col items-start gap-1 px-2 py-1.5 font-normal"
            >
              <span className="w-full truncate text-left text-xs">{c.name}</span>
              <span className="flex w-full items-center gap-2 text-xs text-muted-foreground">
                {c.detail}
              </span>
            </Button>
          </li>
        ))}
      </ul>
    </>
  );
}

// emptyMessage says where the missing thing would come from, rather than only
// that there is none.
function emptyMessage(kind: RefTargetKind): string {
  return kind === "asset"
    ? "No assets to reference. Save one first, or ask its owner to share it with you."
    : "No resources match. Upload files on the Resources page first.";
}

// AudienceNotice states what referencing something means, in this asset's
// terms: the rule the platform applies, plus who this asset is currently shared
// with.
function AudienceNotice({ audience, notice }: { audience: RefAudience; notice: string }) {
  return (
    <div className="space-y-1 text-xs" data-testid="asset-ref-audience">
      <p className="text-muted-foreground">{notice}</p>
      <p className="font-medium">{audienceSentence(audience)}</p>
      {/*
        The one thing adding a reference does NOT do. The declaration and the
        markup are separate, so something added here shows up nowhere until its
        reference is written into the content -- which the row's copy control is
        for.
      */}
      <p className="text-muted-foreground">
        Adding a reference does not change this asset&apos;s content. Copy the reference from the
        list and write it where the file should appear.
      </p>
    </div>
  );
}

// audienceSentence names this asset's current audience, which is the audience a
// referenced target inherits.
function audienceSentence(audience: RefAudience): string {
  if (audience.public) {
    return "This asset has a public link, so anyone holding it can load what you add here.";
  }
  if (audience.shared_with_users) {
    return "This asset is shared with other people, who can load what you add here.";
  }
  return "This asset is not shared with anyone yet. If you share it later, what you add here goes with it.";
}
