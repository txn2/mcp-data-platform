import type { SharedFunction } from "@/api/portal/hooks/sharedCode";
import { Alert, AlertDescription } from "@/components/ui/alert";

// SharedCodeNotice is what a save that went through says about the functions
// of the saved code a library or another script already defines (#2273). It
// never refuses anything, names scripts the reader can already read, and shows
// no code but the load line that replaces a library's copy.
export function SharedCodeNotice({ shared }: { shared: SharedFunction[] }) {
  if (shared.length === 0) return null;
  const title =
    shared.length === 1
      ? "One function in this script already exists elsewhere"
      : `${shared.length} functions in this script already exist elsewhere`;
  return (
    <Alert role="status" data-testid="shared-code-notice" className="border-primary/30 bg-primary/5">
      <AlertDescription className="space-y-3">
        <p className="font-medium text-foreground">{title}</p>
        {shared.map((m) => (
          <div key={m.function} className="space-y-1" data-testid="shared-code-match">
            {m.library ? (
              <>
                <p>
                  <code className="font-mono text-xs">{m.function}</code> is{" "}
                  <code className="font-mono text-xs">{m.library_function}</code> in the library{" "}
                  <span className="font-medium">{m.library}</span>, version {m.version}. This line replaces the copy:
                </p>
                <code className="block break-all rounded border bg-background px-2 py-1 font-mono text-xs">{m.load}</code>
              </>
            ) : (
              <p>
                <code className="font-mono text-xs">{m.function}</code> is also in{" "}
                <span className="font-medium">{(m.scripts ?? []).join(", ")}</span>. It can move into a library that each
                script loads at a pinned version; the other scripts change only with their owners&apos; agreement.
              </p>
            )}
          </div>
        ))}
        <p className="text-xs text-muted-foreground">
          The save went through. This lists scripts you can already read, by name; it never shows their code.
        </p>
      </AlertDescription>
    </Alert>
  );
}
