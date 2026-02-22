import { useMemo } from 'react';
import { formatDistanceToNow, format } from 'date-fns';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';

export interface TimeDisplayProps {
  timestamp: {
    seconds: bigint | number;
    nanos?: number;
  } | null | undefined;
  format?: 'relative' | 'absolute' | 'both';
  timezone?: string;
}

/**
 * TimeDisplay component renders protobuf Timestamp values with relative and absolute formats.
 * Supports timezone preferences and handles bigint/number seconds conversion.
 *
 * @param timestamp - Protobuf timestamp or null/undefined
 * @param format - Display format: 'relative' (e.g., "2 hours ago"), 'absolute' (ISO-like), or 'both'
 * @param timezone - Optional timezone preference (future enhancement)
 */
export function TimeDisplay({
  timestamp,
  format: displayFormat = 'both',
  timezone: _timezone,
}: TimeDisplayProps) {
  const date = useMemo(() => {
    if (!timestamp) return null;

    const seconds =
      typeof timestamp.seconds === 'bigint'
        ? Number(timestamp.seconds)
        : timestamp.seconds;

    return new Date(seconds * 1000 + Math.floor((timestamp.nanos || 0) / 1_000_000));
  }, [timestamp]);

  if (!date) return <>—</>;

  const relative = formatDistanceToNow(date, { addSuffix: true });
  const absolute = format(date, 'yyyy-MM-dd HH:mm:ss');

  if (displayFormat === 'relative') {
    return (
      <>
        {relative} <span className="text-xs text-gray-500">({absolute} UTC)</span>
      </>
    );
  }

  if (displayFormat === 'absolute') {
    return <>{absolute} UTC</>;
  }

  // 'both' - show relative with absolute in tooltip
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="cursor-help">
          {relative}
          <span className="text-xs text-gray-500 ml-1">({absolute} UTC)</span>
        </span>
      </TooltipTrigger>
      <TooltipContent>
        <p className="text-sm">{absolute} UTC</p>
      </TooltipContent>
    </Tooltip>
  );
}
