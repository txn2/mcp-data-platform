interface Props {
  title: string;
}

export function Header({ title }: Props) {
  return (
    <header className="flex items-center border-b bg-card px-6 py-3">
      <h1 className="text-lg font-semibold">{title}</h1>
    </header>
  );
}
