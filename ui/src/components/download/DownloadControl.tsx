import type { ComponentProps } from "react";
import { Download, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { percent, progressDetail, progressLabel, type DownloadProgress } from "@/lib/download";
import { cn } from "@/lib/utils";

type ButtonProps = ComponentProps<typeof Button>;

/**
 * A Download button that shows its download running (#2614): from the click
 * until the file is handed to the browser it is disabled, spins, says how far
 * the download has got ("Preparing…", "Downloading 42%", "Downloading
 * 12.4 MB"), fills a bar along its bottom edge with the share read, and names
 * the bytes on hover. Disabled is aria-disabled, so the hover still reads.
 */
export function DownloadControl({
  progress,
  onClick,
  disabled,
  title,
  size = "sm",
  variant = "outline",
  className,
}: {
  progress: DownloadProgress | null;
  onClick: () => void;
  /** Disabled for another reason than this download running; title says why. */
  disabled?: boolean;
  title?: string;
  size?: ButtonProps["size"];
  variant?: ButtonProps["variant"];
  className?: string;
}) {
  if (!progress) {
    return (
      <Button variant={variant} size={size} onClick={onClick} disabled={disabled} title={title ?? "Download"} className={className} data-testid="download">
        <Download />
        Download
      </Button>
    );
  }
  return (
    <Button
      variant={variant}
      size={size}
      aria-disabled="true"
      aria-busy="true"
      title={progressDetail(progress)}
      className={cn("relative cursor-progress overflow-hidden tabular-nums", className)}
      data-testid="download"
      onClick={(e) => e.preventDefault()}
    >
      <Loader2 className="animate-spin" aria-hidden />
      {progressLabel(progress)}
      <ProgressFill progress={progress} />
    </Button>
  );
}

/** The bar along a control's bottom edge: the share read, or a moving piece when the size is not known. */
export function ProgressFill({ progress, className }: { progress: DownloadProgress; className?: string }) {
  if (progress.phase === "preparing") return null;
  const share = percent(progress);
  if (share !== null) {
    return <span aria-hidden data-testid="download-fill" className={cn("absolute bottom-0 left-0 h-0.5 bg-primary transition-[width]", className)} style={{ width: `${share}%` }} />;
  }
  return (
    <span
      aria-hidden
      data-testid="download-fill"
      className={cn("absolute bottom-0 left-[35%] h-0.5 w-[30%] bg-primary animate-[download-indeterminate_1.4s_ease-in-out_infinite] motion-reduce:animate-none", className)}
    />
  );
}
