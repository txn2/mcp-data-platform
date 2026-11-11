import { useCallback, useRef, useState } from "react";
import { failureMessage, fetchWithProgress, saveBlob, type DownloadProgress } from "@/lib/download";

/**
 * A download's work (#2614): given where to report progress, it saves the file
 * and returns null, or returns what to tell the reader about why it did not.
 */
export type DownloadJob = (report: (p: DownloadProgress) => void) => Promise<string | null>;

/** One page's download: at most one runs at a time. */
export interface Download {
  /** The file the running or failed download saves. */
  name: string;
  /** What the running download is for, the name unless the caller says otherwise. */
  key: string;
  /** How far the running download has got, null when none runs. */
  progress: DownloadProgress | null;
  busy: boolean;
  /** Why the last download failed, null when it did not. */
  failure: string | null;
  /** Starts a download, unless one is running: a second click starts nothing. */
  run: (name: string, job: DownloadJob, key?: string) => Promise<void>;
  /** Starts the failed download over. */
  retry: () => void;
  dismiss: () => void;
}

/** The download state of one page (#2614). */
export function useDownload(): Download {
  const [progress, setProgress] = useState<DownloadProgress | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  // A ref, not the state: a second click lands before the first one's state
  // has rendered.
  const running = useRef(false);
  const last = useRef<{ name: string; job: DownloadJob; key: string } | null>(null);

  const run = useCallback(async (file: string, job: DownloadJob, id: string = file) => {
    if (running.current) return;
    running.current = true;
    last.current = { name: file, job, key: id };
    setName(file);
    setKey(id);
    setFailure(null);
    try {
      const why = await job(setProgress);
      if (why) setFailure(why);
    } catch {
      setFailure(failureMessage(null));
    } finally {
      running.current = false;
      setProgress(null);
    }
  }, []);

  const retry = useCallback(() => {
    const l = last.current;
    if (l) void run(l.name, l.job, l.key);
  }, [run]);
  const dismiss = useCallback(() => setFailure(null), []);

  return { name, key, progress, busy: progress !== null, failure, run, retry, dismiss };
}

/** The job of a plain download: read what open answers, with its progress, and save it under name. */
export function saveDownload(open: () => Promise<Response>, name: string): DownloadJob {
  return async (report) => {
    const got = await fetchWithProgress(open, report);
    if (!got.ok) return failureMessage(got.status);
    saveBlob(got.blob, name);
    return null;
  };
}
