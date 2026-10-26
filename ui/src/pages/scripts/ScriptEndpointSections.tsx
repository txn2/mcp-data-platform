import { ScriptEndpointCard } from "./ScriptEndpointCard";
import { ScriptSubmissions } from "./ScriptSubmissions";

// ScriptEndpointSections are what a script's endpoint adds to its page: the
// binding (#2171) and, for a form, its stored submissions (#2180).
export function ScriptEndpointSections({ scriptId }: { scriptId: string }) {
  return (
    <>
      <ScriptEndpointCard key={`endpoint-${scriptId}`} scriptId={scriptId} />
      <ScriptSubmissions key={`submissions-${scriptId}`} scriptId={scriptId} />
    </>
  );
}
