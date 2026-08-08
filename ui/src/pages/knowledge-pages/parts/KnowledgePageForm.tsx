import { ArrowLeft } from "lucide-react";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { DuplicateGate } from "./DuplicateGate";
import { useKnowledgePageEditor } from "./useKnowledgePageEditor";

export function KnowledgePageForm({ id, onDone }: { id?: string; onDone: (id: string | null) => void }) {
  const editor = useKnowledgePageEditor(id, onDone);
  const { title, setTitle, summary, setSummary, body, setBody, tags, setTags } = editor.fields;

  if (editor.status === "loading") {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }
  if (editor.status === "failed") {
    return (
      <div className="space-y-3">
        <Alert variant="destructive">
          <AlertDescription>Failed to load this page.</AlertDescription>
        </Alert>
        <Button variant="link" size="sm" className="px-0" onClick={() => onDone(id ?? null)}>
          Go back
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <Button variant="ghost" size="sm" onClick={() => onDone(id ?? null)}>
          <ArrowLeft /> Cancel
        </Button>
        <Button size="sm" onClick={editor.submit} disabled={editor.pending}>
          {editor.saveLabel}
        </Button>
      </div>

      {editor.error && (
        <Alert variant="destructive">
          <AlertDescription>{editor.error}</AlertDescription>
        </Alert>
      )}

      {editor.dup && (
        <DuplicateGate
          dup={editor.dup}
          pending={editor.pending}
          onOpenCandidate={onDone}
          onCreateAnyway={editor.createAnyway}
          onDismiss={editor.dismissDup}
        />
      )}

      {/* Persistent labels so each field stays identifiable once populated (the
          edit case), not just while the placeholder shows (#708). */}
      <div className="space-y-1.5">
        <Label htmlFor="kp-title">Title</Label>
        <Input
          id="kp-title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Title"
          className="h-11 text-lg font-medium md:text-lg"
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="kp-summary">Summary (optional)</Label>
        {/* Multi-line so a two-sentence summary is fully readable without
            horizontal scroll (#708). field-sizing-fixed because ui/textarea
            otherwise sizes to its content and ignores the stated rows. */}
        <Textarea
          id="kp-summary"
          value={summary}
          onChange={(e) => setSummary(e.target.value)}
          rows={3}
          placeholder="A sentence or two summarizing the page"
          className="field-sizing-fixed min-h-0 resize-y"
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="kp-tags">Tags (comma-separated, optional)</Label>
        <Input
          id="kp-tags"
          value={tags}
          onChange={(e) => setTags(e.target.value)}
          placeholder="retail, pricing, seasonal"
        />
      </div>
      <MarkdownEditor
        value={body}
        onChange={setBody}
        minHeight="420px"
        placeholder="Write the knowledge page in markdown..."
      />
    </div>
  );
}
