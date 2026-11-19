import { Star } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useIsFavorite, useSetFavorite, type LibraryKind } from "@/api/portal/hooks/library";
import { cn } from "@/lib/utils";

/**
 * StarButton adds an asset or collection to the reader's favorites (#2504),
 * the same control everywhere one is shown. Starring is private and grants
 * nothing. "icon" is the round button over a card's tile; "button" is the
 * labelled one in a viewer's action row and a collection's header.
 */
export function StarButton({
  kind,
  id,
  variant = "button",
  className,
}: {
  kind: LibraryKind;
  id: string;
  variant?: "icon" | "button";
  className?: string;
}) {
  const starred = useIsFavorite(kind, id);
  const setFavorite = useSetFavorite();
  const label = starred ? "Remove from favorites" : "Add to favorites";
  const icon = (
    <Star
      aria-hidden
      className={cn("size-4", starred && "fill-amber-500 text-amber-600")}
    />
  );
  const toggle = (e: React.MouseEvent) => {
    // A star on a card sits inside the card's own link.
    e.preventDefault();
    e.stopPropagation();
    setFavorite.mutate({ kind, id, on: !starred });
  };
  if (variant === "icon") {
    return (
      <button
        type="button"
        aria-label={label}
        aria-pressed={starred}
        title={label}
        disabled={setFavorite.isPending}
        onClick={toggle}
        className={cn(
          "flex size-9 items-center justify-center rounded-full border bg-background/90 shadow-sm transition-colors hover:bg-accent",
          className,
        )}
      >
        {icon}
      </button>
    );
  }
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      aria-pressed={starred}
      aria-label={label}
      disabled={setFavorite.isPending}
      onClick={toggle}
      className={className}
    >
      {icon}
      {starred ? "Starred" : "Star"}
    </Button>
  );
}
