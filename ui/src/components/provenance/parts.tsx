import { useState } from "react";
import {
  Database,
  Globe,
  Search,
  FileText,
  Info,
  Terminal,
  Copy,
  Check,
  History,
  type LucideIcon,
} from "lucide-react";
import { Button } from "@/components/ui/button";

/** Map tool name prefixes to icons for provenance display. */
const TOOL_ICONS: Record<string, LucideIcon> = {
  trino_: Database,
  datahub_: Search,
  s3_: FileText,
  api_: Globe,
  platform_: Info,
};

export function getToolIcon(toolName: string): LucideIcon {
  for (const [prefix, icon] of Object.entries(TOOL_ICONS)) {
    if (toolName.startsWith(prefix)) return icon;
  }
  return Terminal;
}

export function relativeTime(timestamp: string): string {
  const now = Date.now();
  const then = new Date(timestamp).getTime();
  const diff = Math.max(0, now - then);
  const seconds = Math.floor(diff / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

export function truncate(text: string, max = 120): string {
  return text.length > max ? text.slice(0, max) + "..." : text;
}

/** Copy control shared by the call detail and the call reference. */
export function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    const done = () => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    };
    const writeFallback = () => {
      const el = document.createElement("textarea");
      el.value = text;
      document.body.appendChild(el);
      el.select();
      document.execCommand("copy");
      document.body.removeChild(el);
      done();
    };

    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(done, writeFallback);
    } else {
      writeFallback();
    }
  };

  return (
    <Button
      type="button"
      variant="ghost"
      size="xs"
      onClick={handleCopy}
      className="text-muted-foreground"
      title={label}
      aria-label={label}
    >
      {copied ? (
        <>
          <Check className="text-emerald-600 dark:text-emerald-400" />
          Copied
        </>
      ) : (
        <>
          <Copy />
          Copy
        </>
      )}
    </Button>
  );
}

/** Walks from what the asset captured to the whole session that made it. */
export function OpenSessionButton({ onClick }: { onClick: () => void }) {
  return (
    <Button
      type="button"
      variant="outline"
      size="xs"
      onClick={onClick}
      className="w-full text-muted-foreground"
    >
      <History />
      Open session
    </Button>
  );
}
