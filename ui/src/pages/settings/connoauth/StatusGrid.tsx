import { AlertCircle, Check, Clock, Key, RefreshCw } from "lucide-react";
import type { ConnectionOAuthStatus } from "@/api/admin/types";
import { formatRelative } from "./format";

// StatusGrid states the four facts an operator checks before deciding whether
// a connection needs attention: is there a token, when does it lapse, when did
// it last renew, and can it renew at all.
export function StatusGrid({ status }: { status: ConnectionOAuthStatus }) {
  const items: Array<{ label: string; value: string; icon: React.ReactNode }> = [
    {
      label: "Token",
      value: status.token_acquired ? "acquired" : "not yet acquired",
      icon: status.token_acquired ? (
        <Check className="h-3 w-3 text-emerald-500" />
      ) : (
        <AlertCircle className="h-3 w-3 text-amber-500" />
      ),
    },
    {
      label: "Expires",
      value: status.expires_at ? formatRelative(status.expires_at) : "—",
      icon: <Clock className="h-3 w-3 text-muted-foreground" />,
    },
    {
      label: "Last refreshed",
      value: status.last_refreshed_at
        ? formatRelative(status.last_refreshed_at)
        : "—",
      icon: <RefreshCw className="h-3 w-3 text-muted-foreground" />,
    },
    {
      label: "Refresh token",
      value: status.has_refresh_token ? "present" : "none",
      icon: <Key className="h-3 w-3 text-muted-foreground" />,
    },
  ];
  if (status.has_refresh_token && status.refresh_expires_at) {
    items.push({
      label: "Refresh expires",
      value: formatRelative(status.refresh_expires_at),
      icon: <Clock className="h-3 w-3 text-muted-foreground" />,
    });
  }
  return (
    <div className="grid grid-cols-2 gap-2 text-xs">
      {items.map((it) => (
        <div key={it.label} className="flex items-center gap-1.5">
          {it.icon}
          <span className="text-muted-foreground">{it.label}:</span>
          <span className="font-mono">{it.value}</span>
        </div>
      ))}
    </div>
  );
}
