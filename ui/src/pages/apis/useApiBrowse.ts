import { useMemo } from "react";
import { useAPICatalogs } from "@/api/admin/hooks";
import {
  useAPIConnections,
  useAPIOperation,
  useAPIOperations,
  useCatalogOperations,
  useCatalogSpecOperation,
  useCatalogSpecs,
} from "@/api/apis/hooks";
import type { FailedSpec } from "@/api/apis/hooks";
import type {
  APIConnection,
  APIOperationDetail,
  APIOperationSummary,
} from "@/api/apis/types";
import type { OperationSelection } from "./OperationIndex";

// What the browser reads, per scope, behind one shape.
//
// The page is one component mounted twice (#1478), so the difference between
// the two scopes lives here rather than in the render: at /apis the source is a
// connection and the operations are the ones a persona reaches; at /admin/apis
// the source is a catalog and the operations are everything its specs declare.
// Each hook runs only the query its scope needs -- the other is disabled by an
// undefined argument -- so a portal reader never asks the admin API a question
// it would answer with 403.

export type ApisScope = "portal" | "admin";

/** SourceOption is one entry in the picker the index is drawn from. */
export interface SourceOption {
  value: string;
  label: string;
  hint?: string;
}

/** BrowseSources is the set of connections or catalogs a reader may pick. */
export interface BrowseSources {
  options: SourceOption[];
  isLoading: boolean;
}

/** plural writes the count a source is labeled by. */
function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

/** connectionOption labels a connection by how much of it the reader reaches,
 * which is the number that moves when a deny rule applies. */
function connectionOption(c: APIConnection): SourceOption {
  return { value: c.name, label: c.name, hint: plural(c.operation_count, "operation") };
}

/** useBrowseSources lists what the reader may browse in this scope. */
export function useBrowseSources(scope: ApisScope): BrowseSources {
  const isAdmin = scope === "admin";
  const connectionsQ = useAPIConnections(!isAdmin);
  const catalogsQ = useAPICatalogs(isAdmin);

  const options = useMemo(() => {
    if (isAdmin) {
      return (catalogsQ.data ?? []).map((c) => ({
        value: c.id,
        label: c.display_name || c.name,
        hint: plural(c.spec_count, "spec"),
      }));
    }
    return (connectionsQ.data?.connections ?? []).map(connectionOption);
  }, [isAdmin, catalogsQ.data, connectionsQ.data]);

  return { options, isLoading: isAdmin ? catalogsQ.isLoading : connectionsQ.isLoading };
}

/** BrowseIndex is one source's operations, plus what could not be read. */
export interface BrowseIndex {
  operations: APIOperationSummary[];
  /** The connection an invoke snippet is written against. Absent in the admin
   * scope, where a catalog need not be wired to anything yet. */
  connection?: APIConnection;
  /** Specs missing from the index, and why, so the reader is told what is
   * absent rather than shown an index that quietly runs short. */
  failedSpecs: FailedSpec[];
  isLoading: boolean;
}

/** useBrowseIndex reads the operations of one source. */
export function useBrowseIndex(scope: ApisScope, source: string): BrowseIndex {
  const isAdmin = scope === "admin";
  const connectionSource = isAdmin ? undefined : source || undefined;
  const catalogSource = isAdmin ? source || undefined : undefined;

  const operationsQ = useAPIOperations(connectionSource);
  const specsQ = useCatalogSpecs(catalogSource);
  const specNames = useMemo(
    () => (specsQ.data?.specs ?? []).map((s) => s.spec_name),
    [specsQ.data],
  );
  const catalogOps = useCatalogOperations(catalogSource, specNames);

  if (isAdmin) {
    return {
      operations: catalogOps.operations,
      failedSpecs: catalogOps.failedSpecs,
      isLoading: specsQ.isLoading || catalogOps.isLoading,
    };
  }
  return {
    operations: operationsQ.data?.operations ?? [],
    connection: operationsQ.data?.connection,
    failedSpecs: [],
    isLoading: operationsQ.isLoading,
  };
}

/** BrowseDetail is one operation in full, or why it is not shown. */
export interface BrowseDetail {
  detail?: APIOperationDetail;
  isLoading: boolean;
  error?: string;
}

/** DetailArgs are one query's arguments, all undefined when its scope is not
 * the active one, which is how the query that does not apply stays disabled. */
interface DetailArgs {
  source?: string;
  operationID?: string;
  spec?: string;
}

/** detailArgs yields the arguments for the scope that is active. */
function detailArgs(
  active: boolean,
  source: string,
  selected: OperationSelection | null,
): DetailArgs {
  if (!active || !source || !selected) return {};
  return { source, operationID: selected.operationID, spec: selected.spec || undefined };
}

/** errorMessage is what a failed read says, or nothing when it did not fail. */
function errorMessage(err: unknown): string | undefined {
  return err instanceof Error ? err.message : undefined;
}

/** useBrowseDetail reads the selected operation. */
export function useBrowseDetail(
  scope: ApisScope,
  source: string,
  selected: OperationSelection | null,
): BrowseDetail {
  const isAdmin = scope === "admin";
  const portal = detailArgs(!isAdmin, source, selected);
  const admin = detailArgs(isAdmin, source, selected);

  const portalQ = useAPIOperation(portal.source, portal.operationID, portal.spec);
  const adminQ = useCatalogSpecOperation(admin.source, admin.spec, admin.operationID);
  const query = isAdmin ? adminQ : portalQ;

  return {
    detail: query.data,
    isLoading: query.isLoading,
    error: errorMessage(query.error),
  };
}
