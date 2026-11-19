/** EveryoneSwitch is the administrator's "Everyone's" toggle on Trash, Unused
 *  and Possible duplicates (#2509): on, the list spans every owner and adds an
 *  Owner column. Only rendered for administrators; the server refuses anyone
 *  else who asks. */
export function EveryoneSwitch({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (on: boolean) => void;
}) {
  return (
    <label className="flex items-center gap-2 whitespace-nowrap text-sm">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className="size-4" />
      {label} <span className="text-muted-foreground">(administrators)</span>
    </label>
  );
}
