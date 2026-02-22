import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { TooltipProvider } from '@/components/ui/tooltip';
import { TimeDisplay } from './time-display';

describe('TimeDisplay', () => {
  const mockDate = new Date('2025-02-22T10:00:00Z');
  const mockSeconds = Math.floor(mockDate.getTime() / 1000);
  const mockTimestamp = {
    seconds: BigInt(mockSeconds),
    nanos: 0,
  };

  beforeEach(() => {
    // Mock the current time for consistent testing
    vi.useFakeTimers();
    vi.setSystemTime(mockDate);
  });

  afterEach(() => {
    // Restore real timers after each test to prevent state leakage
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
  });

  // Helper to render with TooltipProvider
  const renderWithTooltip = (component: React.ReactElement) =>
    render(<TooltipProvider>{component}</TooltipProvider>);

  it('renders em dash for null timestamp', () => {
    const { container } = render(<TimeDisplay timestamp={null} />);
    expect(container.textContent).toContain('—');
  });

  it('renders em dash for undefined timestamp', () => {
    const { container } = render(<TimeDisplay timestamp={undefined} />);
    expect(container.textContent).toContain('—');
  });

  it('renders relative format by default', () => {
    renderWithTooltip(<TimeDisplay timestamp={mockTimestamp} />);
    expect(screen.getByText(/ago/)).toBeInTheDocument();
  });

  it('renders absolute format when specified', () => {
    render(<TimeDisplay timestamp={mockTimestamp} format="absolute" />);
    expect(screen.getByText(/2025-02-22/)).toBeInTheDocument();
  });

  it('converts bigint seconds correctly', () => {
    const timestampWithBigint = {
      seconds: BigInt(mockSeconds),
      nanos: 0,
    };
    render(<TimeDisplay timestamp={timestampWithBigint} format="absolute" />);
    expect(screen.getByText(/2025-02-22/)).toBeInTheDocument();
  });

  it('converts number seconds correctly', () => {
    const timestampWithNumber = {
      seconds: mockSeconds,
      nanos: 0,
    };
    render(<TimeDisplay timestamp={timestampWithNumber} format="absolute" />);
    expect(screen.getByText(/2025-02-22/)).toBeInTheDocument();
  });

  it('includes nanoseconds in date calculation', () => {
    const timestampWithNanos = {
      seconds: BigInt(Math.floor(mockDate.getTime() / 1000)),
      nanos: 500_000_000, // 0.5 seconds
    };
    render(<TimeDisplay timestamp={timestampWithNanos} format="absolute" />);
    expect(screen.getByText(/2025-02-22/)).toBeInTheDocument();
  });

  it('renders both formats when "both" is specified', () => {
    renderWithTooltip(<TimeDisplay timestamp={mockTimestamp} format="both" />);
    expect(screen.getByText(/ago/)).toBeInTheDocument();
    expect(screen.getByText(/2025-02-22/)).toBeInTheDocument();
  });

  it('shows only relative time for relative format', () => {
    const { container } = render(
      <TimeDisplay timestamp={mockTimestamp} format="relative" />
    );
    // Relative format should only show relative time
    expect(container.textContent).toMatch(/ago/);
    expect(container.textContent).not.toMatch(/UTC/);
  });

  it('includes "UTC" in absolute format output', () => {
    render(<TimeDisplay timestamp={mockTimestamp} format="absolute" />);
    expect(screen.getByText(/UTC/)).toBeInTheDocument();
  });

  it('includes cursor-help styling for both format', () => {
    const { container } = renderWithTooltip(
      <TimeDisplay timestamp={mockTimestamp} format="both" />
    );
    const span = container.querySelector('span.cursor-help');
    expect(span).toBeInTheDocument();
  });
});
