import { useMemo } from 'react';
import { formatDistanceToNow } from 'date-fns';
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
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentionally depend on primitive fields, not object reference
  }, [timestamp?.seconds, timestamp?.nanos]);

  if (!date) return <>—</>;

  const relative = formatDistanceToNow(date, { addSuffix: true });
  // Format using UTC time string to ensure consistency across timezones
  const iso = date.toISOString();
  const absolute = iso.replace('T', ' ').slice(0, 19);

  if (displayFormat === 'relative') {
    return <>{relative}</>;
  }

  if (displayFormat === 'absolute') {
    return <>{absolute} UTC</>;
  }

  // 'both' format: show relative time with absolute time in tooltip on hover
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="cursor-help">
          {relative}
          <span className="text-xs text-muted-foreground ml-1">({absolute} UTC)</span>
        </span>
      </TooltipTrigger>
      <TooltipContent>
        <p className="text-sm">{absolute} UTC</p>
      </TooltipContent>
    </Tooltip>
  );
}
