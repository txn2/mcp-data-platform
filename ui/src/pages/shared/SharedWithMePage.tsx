import { File } from "lucide-react";
import { useSharedWithMe } from "@/api/portal/hooks";

interface Props {
  onNavigate: (path: string) => void;
}

export function SharedWithMePage({ onNavigate }: Props) {
  const { data, isLoading } = useSharedWithMe();

  const items = data?.data ?? [];

  return (
    <div className="space-y-4">
      {isLoading ? (
        <div className="flex items-center justify-center py-12 text-muted-foreground">
          Loading...
        </div>
      ) : items.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <File className="h-12 w-12 mb-2 opacity-30" />
          <p className="text-sm">No shared assets</p>
          <p className="text-xs mt-1">
            Assets shared with you will appear here.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {items.map((item) => (
            <button
              key={item.share_id}
              type="button"
              onClick={() => onNavigate(`/assets/${item.asset.id}`)}
              className="flex flex-col items-start rounded-lg border bg-card p-4 text-left transition-colors hover:bg-accent/50 hover:border-primary/30"
            >
              <span className="text-sm font-medium truncate w-full mb-1">
                {item.asset.name}
              </span>
              {item.asset.description && (
                <p className="text-xs text-muted-foreground mb-2 line-clamp-2">
                  {item.asset.description}
                </p>
              )}
              <div className="flex items-center justify-between w-full text-xs text-muted-foreground mt-auto">
                <span>Shared by {item.shared_by}</span>
                <span>{new Date(item.shared_at).toLocaleDateString()}</span>
              </div>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
