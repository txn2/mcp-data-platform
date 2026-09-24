import { useCallback, useMemo, useRef, useState } from "react";
import { useInvalidateResources } from "@/api/resources/hooks";
import type { ScopeTarget } from "../scopes";
import { expandArchives, type Unzip, fflateUnzip } from "./collect";
import { describe } from "./names";
import { planItems, sendProblem, targetPath, type BulkItem, type SourceFile } from "./plan";
import { runPool } from "./pool";
import type { ItemStatus } from "./progress";
import { sendFile, sendForm, type SendResult } from "./send";
import { typeFromName } from "./webImage";

/** How many files are in flight at once. */
export const BULK_CONCURRENCY = 4;

/** The settings every file of the batch shares. */
export interface BatchSettings {
  target: ScopeTarget;
  base: string;
  tags: string[];
  template: string;
}

/** The request one file is sent with, injectable for a test. */
export type Sender = (form: FormData, onProgress: (fraction: number) => void) => Promise<SendResult>;

const READY: ItemStatus = { state: "ready" };

/**
 * useBulkUpload holds a bulk upload: the files picked, the name each will be
 * listed under, where each stands, and the run over them (#1862).
 *
 * The list is re-planned from every source picked so far, so adding a folder
 * after a handful of files keeps the handful, and a duplicate address across
 * the two picks is caught. An edited display name is kept by the item's key.
 */
export function useBulkUpload(maxBytes: number, send: Sender = sendFile, unzip: Unzip = fflateUnzip) {
  const invalidate = useInvalidateResources();
  const [sources, setSources] = useState<SourceFile[]>([]);
  const [names, setNames] = useState<Record<string, string>>({});
  const [statuses, setStatuses] = useState<Record<string, ItemStatus>>({});
  const [running, setRunning] = useState(false);
  const [ran, setRan] = useState(false);
  const stopRef = useRef(false);

  const items = useMemo<BulkItem[]>(
    () =>
      planItems(sources, maxBytes).map((item) =>
        names[item.key] === undefined ? item : { ...item, displayName: names[item.key] ?? item.displayName },
      ),
    [sources, maxBytes, names],
  );

  const statusOf = useCallback((key: string): ItemStatus => statuses[key] ?? READY, [statuses]);

  const add = useCallback(
    async (picked: SourceFile[], unpack: boolean) => {
      const expanded = await expandArchives(picked, unpack, unzip);
      setSources((prev) => [...prev, ...expanded]);
    },
    [unzip],
  );

  const rename = useCallback((key: string, name: string) => {
    setNames((prev) => ({ ...prev, [key]: name }));
  }, []);

  const clear = useCallback(() => {
    setSources([]);
    setNames({});
    setStatuses({});
    setRan(false);
  }, []);

  const setStatus = useCallback((key: string, status: ItemStatus) => {
    setStatuses((prev) => ({ ...prev, [key]: status }));
  }, []);

  const sendOne = useCallback(
    async (item: BulkItem, settings: BatchSettings) => {
      const form = sendForm({
        target: settings.target,
        path: targetPath(settings.base, item.relDir),
        displayName: item.displayName,
        description: describe(settings.template, item.displayName.trim()),
        tags: settings.tags,
        filename: item.filename,
        file: item.file,
        mimeType: typeFromName(item.filename),
      });
      setStatus(item.key, { state: "sending", progress: 0 });
      const result = await send(form, (progress) => setStatus(item.key, { state: "sending", progress }));
      setStatus(
        item.key,
        result.ok && result.outcome
          ? { state: "done", outcome: result.outcome }
          : { state: "failed", error: result.error ?? "The upload failed." },
      );
    },
    [send, setStatus],
  );

  /**
   * run sends every item that has not already been stored, or only the failed
   * ones when `onlyFailed` is set. An item the settings make unsendable is
   * marked refused with the reason and never sent.
   */
  const run = useCallback(
    async (settings: BatchSettings, onlyFailed = false) => {
      const queue: BulkItem[] = [];
      for (const item of items) {
        const state = statusOf(item.key).state;
        if (state === "done" || (onlyFailed && state !== "failed")) continue;
        const problem = sendProblem(item, settings.base, describe(settings.template, item.displayName.trim()));
        if (problem) setStatus(item.key, { state: "refused", error: problem });
        else queue.push(item);
      }
      stopRef.current = false;
      setRunning(true);
      // A stop lets the requests in flight finish and starts no more, so every
      // item is either settled or still ready when the pool returns.
      await runPool(queue, BULK_CONCURRENCY, (item) => sendOne(item, settings), () => stopRef.current);
      setRunning(false);
      setRan(true);
      await invalidate();
    },
    [items, statusOf, setStatus, sendOne, invalidate],
  );

  const stop = useCallback(() => {
    stopRef.current = true;
  }, []);

  return { items, statusOf, statuses, running, ran, add, rename, clear, run, stop };
}
