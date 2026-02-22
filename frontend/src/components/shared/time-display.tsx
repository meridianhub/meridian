import { useMemo } from 'react';
import { formatDistanceToNow, format } from 'date-fns';

interface TimeDisplayProps {
  timestamp: {
    seconds: bigint | number;
    nanos?: number;
  } | null | undefined;
  format?: 'relative' | 'absolute' | 'both';
  timezone?: string;
}

export function TimeDisplay({
  timestamp,
  format: displayFormat = 'both',
  timezone,
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
        {relative} <span className="text-gray-500">({absolute} UTC)</span>
      </>
    );
  }

  if (displayFormat === 'absolute') {
    return <>{absolute}</>;
  }

  // 'both' - show relative with absolute in tooltip
  return (
    <span title={absolute}>
      {relative} <span className="text-gray-500">({absolute} UTC)</span>
    </span>
  );
}
