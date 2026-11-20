import { RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { currentBuildWatch, readerIsBusy, type BuildWatch } from "@/lib/buildWatch";

// BuildNotice is the one line a page shows when the platform was upgraded
// while it was open and reloading now would cost the reader something (#2727):
// unsaved changes or an open dialog. Otherwise the page moves to the new build
// at the next navigation and this shows nothing. It also shows when a reload
// for the new build came back on the old one, which is never retried by itself.

/** How often an open notice re-reads whether the reader is still busy. */
export const BUSY_POLL_MS = 1000;

export function BuildNotice({
  watch = currentBuildWatch(),
  isBusy = readerIsBusy,
  reload = () => window.location.reload(),
}: {
  watch?: BuildWatch;
  isBusy?: () => boolean;
  reload?: () => void;
}) {
  const [pending, setPending] = useState(watch.pendingBuild());
  // shown stays true once the notice appeared: a reader who was busy when the
  // build changed (a dialog open) sees the notice when the dialog closes,
  // rather than it vanishing the moment they stop being busy.
  const [shown, setShown] = useState(false);
  useEffect(() => watch.subscribe(() => setPending(watch.pendingBuild())), [watch]);
  useEffect(() => {
    if (!pending || shown) return;
    const read = () => {
      if (isBusy()) setShown(true);
    };
    read();
    const t = window.setInterval(read, BUSY_POLL_MS);
    return () => window.clearInterval(t);
  }, [pending, shown, isBusy]);
  if (!pending || !(shown || watch.alreadyTried())) return null;
  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-3 border-b border-blue-200 bg-blue-50 px-3 py-2 text-sm text-blue-900 sm:px-6 dark:border-blue-900 dark:bg-blue-950 dark:text-blue-100"
    >
      <RefreshCw className="h-4 w-4 shrink-0" aria-hidden />
      <span className="min-w-0 flex-[1_1_20rem]">
        The portal has been updated. Save your changes, then reload to use the new version.
      </span>
      <Button size="sm" onClick={reload}>
        Reload
      </Button>
    </div>
  );
}
