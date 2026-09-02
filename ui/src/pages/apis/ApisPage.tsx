import { useCallback, useEffect, useMemo, useState } from "react";
import { Plug, Library } from "lucide-react";
import { EmptyState } from "@/components/patterns/EmptyState";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { FailedSpec } from "@/api/apis/hooks";
import { OperationIndex, type OperationSelection } from "./OperationIndex";
import { OperationDetail } from "./OperationDetail";
import {
  useBrowseDetail,
  useBrowseIndex,
  useBrowseSources,
  type ApisScope,
  type SourceOption,
} from "./useApiBrowse";

// The operation browser (#1478), one component mounted twice.
//
// At /apis it reads the connections the caller's persona reaches, and the
// operations on them the route policy permits, so the page and
// api_discover agree on what that caller may call. At /admin/apis it
// reads catalogs instead, including ones no connection references yet, which is
// the operator's view of what has been loaded.
//
// The scope decides which source the index comes from and whether there is a
// connection to write a call snippet against. Everything below that -- the
// grouping, the filter, the pane, the deep link -- is one implementation,
// because a page that described an operation differently depending on who
// opened it would be two answers to one question.

export type { ApisScope };

/** SOURCE_PARAM is the search param the picked source travels in, named for
 * what it holds in each scope so a shared link reads as what it is. */
const SOURCE_PARAM: Record<ApisScope, string> = {
  portal: "connection",
  admin: "catalog",
};

interface UrlState {
  source: string;
  spec: string;
  operationID: string;
}

/** readUrlState recovers a deep link on first mount. */
function readUrlState(scope: ApisScope): UrlState {
  if (typeof window === "undefined") return { source: "", spec: "", operationID: "" };
  const params = new URLSearchParams(window.location.search);
  return {
    source: params.get(SOURCE_PARAM[scope]) ?? "",
    spec: params.get("spec") ?? "",
    operationID: params.get("op") ?? "",
  };
}

/**
 * writeUrlState reflects the selection back into the address bar, so a link to
 * an operation opens on that operation. replaceState rather than push: picking
 * a row is not a navigation, and a Back button that stepped through every row
 * somebody clicked would never leave the page.
 */
function writeUrlState(scope: ApisScope, state: UrlState): void {
  if (typeof window === "undefined") return;
  const params = new URLSearchParams(window.location.search);
  const set = (key: string, value: string) => {
    if (value) params.set(key, value);
    else params.delete(key);
  };
  set(SOURCE_PARAM[scope], state.source);
  set("spec", state.spec);
  set("op", state.operationID);
  const qs = params.toString();
  window.history.replaceState(
    null,
    "",
    window.location.pathname + (qs ? `?${qs}` : "") + window.location.hash,
  );
}

/** SourcePicker chooses which connection or catalog the index is drawn from. */
function SourcePicker({
  scope,
  options,
  value,
  onChange,
}: {
  scope: ApisScope;
  options: SourceOption[];
  value: string;
  onChange: (value: string) => void;
}) {
  const isAdmin = scope === "admin";
  const Icon = isAdmin ? Library : Plug;
  return (
    <div className="flex items-center gap-2">
      <Icon aria-hidden className="size-4 text-muted-foreground" />
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger
          className="h-8 w-72"
          aria-label={isAdmin ? "Choose an API catalog" : "Choose an API connection"}
        >
          <SelectValue placeholder={isAdmin ? "Choose a catalog" : "Choose a connection"} />
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
              {o.hint && <span className="ml-2 text-muted-foreground">{o.hint}</span>}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

/** COPY is what the page says in each scope when there is nothing to show. Held
 * as a table so the two readings sit side by side and neither is a fallback for
 * the other: an operator with no catalog and a caller with no connection are
 * missing different things. */
const COPY: Record<ApisScope, { noSource: string; noOperations: string }> = {
  portal: {
    noSource:
      "No API connection is available to you. An administrator configures api connections and the personas that reach them.",
    noOperations:
      "This connection exposes no operations you can reach. It may have no catalog, or its route rules may permit none of them.",
  },
  admin: {
    noSource:
      "No API catalog has been created. A catalog holds the OpenAPI specs an api connection is described by.",
    noOperations:
      "This catalog has no operations. Add a spec to it, or check that the specs it holds declare paths.",
  },
};

/** useSourceSelection keeps the picked source valid: it opens on the first
 * option when the reader named none, and falls back to it when a link names one
 * that no longer exists, rather than leaving the page blank on a stale
 * bookmark. */
function useSourceSelection(
  options: SourceOption[],
  initial: string,
  onReset: () => void,
): [string, (value: string) => void] {
  const [source, setSource] = useState(initial);

  useEffect(() => {
    if (options.length === 0) return;
    if (source && options.some((o) => o.value === source)) return;
    setSource(options[0]?.value ?? "");
    if (source) onReset();
  }, [source, options, onReset]);

  return [source, setSource];
}

/** FailedSpecsNotice names the specs missing from the index and why. The two
 * reasons are separate sentences because they send the reader to different
 * places: one says the spec's content is the problem, the other says only that
 * this read did not land. */
function FailedSpecsNotice({ specs }: { specs: FailedSpec[] }) {
  const unparseable = specs.filter((s) => s.unparseable).map((s) => s.name);
  const unread = specs.filter((s) => !s.unparseable).map((s) => s.name);
  if (specs.length === 0) return null;
  return (
    <span className="text-[11px] text-destructive">
      {unparseable.length > 0 && (
        <>
          {unparseable.join(", ")} could not be read as OpenAPI and{" "}
          {unparseable.length === 1 ? "is" : "are"} absent from this index.{" "}
        </>
      )}
      {unread.length > 0 && (
        <>
          {unread.join(", ")} could not be loaded and{" "}
          {unread.length === 1 ? "is" : "are"} absent from this index.
        </>
      )}
    </span>
  );
}

export function ApisPage({ scope = "portal" }: { scope?: ApisScope } = {}) {
  const initial = useMemo(() => readUrlState(scope), [scope]);
  const [selected, setSelected] = useState<OperationSelection | null>(
    initial.operationID ? { operationID: initial.operationID, spec: initial.spec } : null,
  );

  const clearSelection = useCallback(() => setSelected(null), []);
  const sources = useBrowseSources(scope);
  const [source, setSource] = useSourceSelection(
    sources.options,
    initial.source,
    clearSelection,
  );

  const index = useBrowseIndex(scope, source);
  const detail = useBrowseDetail(scope, source, selected);
  const copy = COPY[scope];

  useEffect(() => {
    writeUrlState(scope, {
      source,
      spec: selected?.spec ?? "",
      operationID: selected?.operationID ?? "",
    });
  }, [scope, source, selected]);

  const pickSource = useCallback(
    (value: string) => {
      setSource(value);
      setSelected(null);
    },
    [setSource],
  );

  if (sources.options.length === 0 && !sources.isLoading) {
    return (
      <EmptyState icon={scope === "admin" ? Library : Plug} className="mt-6">
        {copy.noSource}
      </EmptyState>
    );
  }

  return (
    <div className="flex h-[calc(100vh-8rem)] flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <SourcePicker
          scope={scope}
          options={sources.options}
          value={source}
          onChange={pickSource}
        />
        {index.connection?.base_url && (
          <span className="truncate font-mono text-[11px] text-muted-foreground">
            {index.connection.base_url}
          </span>
        )}
        <FailedSpecsNotice specs={index.failedSpecs} />
      </div>

      <div className="flex min-h-0 flex-1 gap-3 overflow-hidden rounded-lg border bg-card">
        <aside className="w-80 shrink-0 border-r">
          <OperationIndex
            operations={index.operations}
            selected={selected}
            onSelect={setSelected}
            loading={sources.isLoading || index.isLoading}
            emptyMessage={copy.noOperations}
          />
        </aside>
        <section className="min-w-0 flex-1 overflow-hidden">
          <OperationDetail
            detail={detail.detail}
            loading={detail.isLoading}
            error={detail.error}
            connection={index.connection}
          />
        </section>
      </div>
    </div>
  );
}
