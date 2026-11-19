import { useFavorites } from "@/api/portal/hooks/library";

/**
 * FavoritesNav lists the reader's favorites in the rail (#2504), under the
 * portal sections; on a phone it sits at the bottom of the menu drawer, which
 * renders the same rail. A favorite the reader can no longer open is not in
 * the list the server returns, so it is simply absent. Nothing renders when
 * there are none.
 */
export function FavoritesNav({ onNavigate }: { onNavigate: (path: string) => void }) {
  const { data } = useFavorites(10);
  if (!data || data.length === 0) return null;
  return (
    <div className="mt-3 space-y-0.5 border-t pt-2" data-testid="sidebar-favorites">
      <p className="px-3 py-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Favorites</p>
      {data.map((f) => (
        <button
          key={`${f.kind}:${f.id}`}
          type="button"
          onClick={() => onNavigate(f.kind === "collection" ? `/collections/${f.id}` : `/assets/${f.id}`)}
          className="block w-full truncate rounded-md px-3 py-1.5 text-left text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          title={f.name}
        >
          {f.name}
        </button>
      ))}
    </div>
  );
}
