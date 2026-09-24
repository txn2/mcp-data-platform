import { useRef, useState, type DragEvent } from "react";
import { FilePlus, FolderPlus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { fromDrop, fromFileList } from "../../bulk/collect";
import { MAX_BATCH_FILES, MAX_ZIP_BYTES, type SourceFile } from "../../bulk/plan";

/**
 * Where a bulk upload's files come from: a multi-select, a folder, or a drop of
 * either onto the zone (#1862). Every route hands the same thing up, the files
 * with their paths relative to what was picked, so the list does not care
 * which one was used.
 */
export function BulkSources({
  onAdd,
  unpack,
  onUnpackChange,
  maxBytes,
  disabled,
}: {
  onAdd: (sources: SourceFile[]) => void;
  unpack: boolean;
  onUnpackChange: (unpack: boolean) => void;
  maxBytes: number;
  disabled: boolean;
}) {
  const filesRef = useRef<HTMLInputElement>(null);
  const folderRef = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);

  const drop = async (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setOver(false);
    if (disabled) return;
    onAdd(await fromDrop(e.dataTransfer));
  };

  return (
    <div className="space-y-2">
      <div
        data-testid="bulk-drop-zone"
        onDragOver={(e) => {
          e.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={drop}
        className={cn(
          "flex flex-col items-center gap-2 rounded-md border border-dashed px-4 py-5 text-center",
          over ? "border-primary bg-accent/50" : "bg-muted/40",
        )}
      >
        <p className="text-sm text-foreground">Drop files, folders or .zip archives here</p>
        <div className="flex gap-2">
          <Button type="button" variant="outline" size="sm" disabled={disabled} onClick={() => filesRef.current?.click()}>
            <FilePlus />
            Choose files
          </Button>
          <Button type="button" variant="outline" size="sm" disabled={disabled} onClick={() => folderRef.current?.click()}>
            <FolderPlus />
            Choose folder
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          Up to {MAX_BATCH_FILES.toLocaleString("en-US")} files per batch, each at most {formatBytes(maxBytes)}; an
          archive is unpacked up to {formatBytes(MAX_ZIP_BYTES)}. Subfolders become folders.
        </p>
      </div>
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
        <input
          type="checkbox"
          checked={unpack}
          disabled={disabled}
          onChange={(e) => onUnpackChange(e.target.checked)}
          data-testid="bulk-unpack"
        />
        Unpack .zip archives into their files
      </label>
      <input
        ref={filesRef}
        type="file"
        multiple
        className="hidden"
        data-testid="bulk-files-input"
        onChange={(e) => {
          onAdd(fromFileList(e.target.files ?? []));
          e.target.value = "";
        }}
      />
      <input
        ref={folderRef}
        type="file"
        multiple
        className="hidden"
        data-testid="bulk-folder-input"
        // Not in React's typings; the browsers that support a folder pick all
        // read this attribute.
        {...{ webkitdirectory: "" }}
        onChange={(e) => {
          onAdd(fromFileList(e.target.files ?? []));
          e.target.value = "";
        }}
      />
    </div>
  );
}
