import { Skeleton } from "@/components/ui/skeleton";

interface ChartSkeletonProps {
  height?: number;
}

// The stand-in a chart shows while its series are loading. It rides ui/skeleton
// so every waiting surface in the portal pulses the same way; only the height
// is the chart's own, since that is what keeps the panel from jumping when the
// data arrives.
export function ChartSkeleton({ height = 200 }: ChartSkeletonProps) {
  return <Skeleton className="rounded-lg" style={{ height }} />;
}
