import type { FlowLibrary } from "@/api/portal/hooks/scriptFlow";
import { CopyButton } from "@/components/provenance/parts";

/**
 * LibraryFunctions is a library's Flow tab (#1970). A library makes no
 * platform calls, so it has no diagram; what its reader needs is each function
 * it offers, what it takes and what it does, and the line that loads them.
 */
export function LibraryFunctions({ library }: { library: FlowLibrary }) {
  return (
    <div className="space-y-4" data-testid="library-functions">
      <section className="space-y-2">
        <h4 className="text-sm font-medium">Functions</h4>
        {library.functions.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            This library defines no function a script can load. A name that begins with an
            underscore is private to the library.
          </p>
        ) : (
          <ul className="divide-y rounded-md border">
            {library.functions.map((fn) => (
              <li key={fn.name} className="space-y-1 px-3 py-2" data-testid={`library-function-${fn.name}`}>
                <code className="font-mono text-sm">
                  {fn.name}({fn.params.join(", ")})
                </code>
                {fn.doc && <p className="text-sm text-muted-foreground">{fn.doc}</p>}
              </li>
            ))}
          </ul>
        )}
      </section>
      {library.load && (
        <section className="space-y-2">
          <h4 className="text-sm font-medium">Load</h4>
          <div className="flex items-start gap-2 rounded-md border bg-muted/40 px-3 py-2">
            <code className="min-w-0 flex-1 break-all font-mono text-sm" data-testid="library-load">
              {library.load}
            </code>
            <CopyButton text={library.load} label="Copy the load line" />
          </div>
        </section>
      )}
    </div>
  );
}
