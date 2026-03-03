import type { Provenance } from "@/api/types";

interface Props {
  provenance: Provenance;
}

export function ProvenancePanel({ provenance }: Props) {
  const calls = provenance.tool_calls ?? [];
  if (calls.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">No provenance data available.</p>
    );
  }

  return (
    <div className="space-y-3">
      <h3 className="text-sm font-medium">Provenance</h3>
      <div className="relative pl-4 border-l-2 border-primary/20 space-y-3">
        {calls.map((call, i) => (
          <div key={i} className="relative">
            <div className="absolute -left-[calc(0.5rem+1px)] top-1.5 h-2 w-2 rounded-full bg-primary" />
            <div className="text-sm">
              <span className="font-mono text-xs bg-muted px-1.5 py-0.5 rounded">
                {call.tool_name}
              </span>
              {call.summary && (
                <p className="text-muted-foreground mt-0.5">{call.summary}</p>
              )}
              <p className="text-xs text-muted-foreground mt-0.5">
                {new Date(call.timestamp).toLocaleString()}
              </p>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
