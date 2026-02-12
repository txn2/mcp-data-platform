interface ChartSkeletonProps {
  height?: number;
}

export function ChartSkeleton({ height = 200 }: ChartSkeletonProps) {
  return (
    <div
      className="animate-pulse rounded-lg bg-muted"
      style={{ height }}
    />
  );
}
