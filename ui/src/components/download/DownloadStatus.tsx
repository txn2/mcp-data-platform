import { useEffect } from "react";
import { createPortal } from "react-dom";
import { Loader2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useIsMobile } from "@/hooks/useIsMobile";
import { percent, progressDetail, type DownloadProgress } from "@/lib/download";
import { ProgressFill } from "./DownloadControl";
import type { Download } from "./useDownload";

/** How long a failure toast stays on a wide screen. */
export const FAILURE_TOAST_MS = 5000;

/**
 * Where a download is seen besides its control (#2614). On a phone, where the
 * More menu closes on select, a status line at the foot of the screen shows it
 * running and, when it fails, stays until dismissed or tried again. On a wider
 * screen a failure is a toast for five seconds, with Try again.
 */
export function DownloadStatus({ download, toastMs = FAILURE_TOAST_MS }: { download: Download; toastMs?: number }) {
  const isMobile = useIsMobile();
  if (isMobile) return <PhoneStatus download={download} />;
  return <FailureToast download={download} toastMs={toastMs} />;
}

function FailureToast({ download, toastMs }: { download: Download; toastMs: number }) {
  const { failure, dismiss, retry } = download;
  useEffect(() => {
    if (!failure) return;
    const t = setTimeout(dismiss, toastMs);
    return () => clearTimeout(t);
  }, [failure, dismiss, toastMs]);
  if (!failure) return null;
  return createPortal(
    <div
      role="status"
      aria-live="polite"
      data-testid="download-failure"
      className="fixed bottom-6 left-1/2 z-[60] flex -translate-x-1/2 items-center gap-3.5 rounded-lg bg-foreground px-3.5 py-2 text-[13px] text-background shadow-lg"
    >
      <span>{failure}</span>
      <button
        type="button"
        className="font-semibold text-[hsl(217_91%_65%)] underline-offset-2 hover:underline dark:text-primary"
        onClick={() => {
          dismiss();
          retry();
        }}
      >
        Try again
      </button>
    </div>,
    document.body,
  );
}

const card = "fixed inset-x-3 bottom-5 z-[60] flex flex-col gap-1.5 overflow-hidden rounded-xl border bg-card px-3.5 pt-3 pb-3.5 text-card-foreground shadow-lg";

function PhoneStatus({ download }: { download: Download }) {
  const { progress, failure, name, dismiss, retry } = download;
  if (progress) return createPortal(<PhoneProgress name={name} progress={progress} />, document.body);
  if (!failure) return null;
  return createPortal(
    <div role="alert" data-testid="download-status" className={`${card} border-destructive/50`}>
      <div className="flex items-start gap-2.5">
        <span className="flex-1 text-sm">{failure}</span>
        <Button variant="ghost" size="icon-sm" className="-mt-1.5 -mr-1.5" aria-label="Dismiss" onClick={dismiss}>
          <X />
        </Button>
      </div>
      <div>
        <Button
          variant="outline"
          onClick={() => {
            dismiss();
            retry();
          }}
        >
          Try again
        </Button>
      </div>
    </div>,
    document.body,
  );
}

function PhoneProgress({ name, progress }: { name: string; progress: DownloadProgress }) {
  const share = percent(progress);
  const preparing = progress.phase === "preparing";
  return (
    <div role="status" aria-live="polite" data-testid="download-status" className={card}>
      <div className="flex items-center gap-2.5">
        <Loader2 className="size-4 shrink-0 animate-spin" aria-hidden />
        <span className="min-w-0 flex-1 truncate text-sm font-medium">
          {preparing ? "Preparing" : "Downloading"} {name}
        </span>
        {share !== null && <span className="text-sm font-semibold tabular-nums">{share}%</span>}
      </div>
      <span className="pl-[26px] text-xs text-muted-foreground tabular-nums">{progressDetail(progress)}</span>
      {!preparing && (
        <span className="absolute inset-x-0 bottom-0 h-[3px] bg-muted" aria-hidden>
          <ProgressFill progress={progress} className="h-[3px]" />
        </span>
      )}
    </div>
  );
}
