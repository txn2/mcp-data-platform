import { Button } from "@/components/ui/button";

// The source editor's controls that only an automation carries or that only a
// library carries (#1941), split out of ScriptSourceEditor for its line budget.

// RunButtons are the two executions an automation offers beside its code: a
// dry run of what is on screen, and a run of the saved version, which only a
// script the run gate would admit gets.
export function RunButtons({
  disabled,
  running,
  queueing,
  runnable,
  onDryRun,
  onRun,
}: {
  disabled: boolean;
  running: boolean;
  queueing: boolean;
  runnable: boolean;
  onDryRun: () => void;
  onRun: () => void;
}) {
  return (
    <>
      <Button size="sm" variant="outline" disabled={disabled} onClick={onDryRun}>
        {running ? "Running..." : "Dry run"}
      </Button>
      {runnable && (
        <Button size="sm" variant="outline" disabled={disabled} onClick={onRun}>
          {queueing ? "Queueing..." : "Run"}
        </Button>
      )}
    </>
  );
}

// LibrarySaveNotice is SaveNotice for a library (#1941): saving makes a new
// version other scripts can load, and a script already loading an older one
// keeps it until its own source names the new one.
export function LibrarySaveNotice({ version }: { version: number }) {
  return (
    <p className="text-xs text-muted-foreground">
      This is a library: other scripts load it by version, and nothing runs it
      on its own. Saving makes a new version after v{version}; a script keeps
      loading the version its source names until that source is changed.
      Validate checks the edit first.
    </p>
  );
}
