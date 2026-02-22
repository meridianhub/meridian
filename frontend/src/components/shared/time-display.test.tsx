import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
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

  it('renders em dash for null timestamp', () => {
    const { container } = render(<TimeDisplay timestamp={null} />);
    expect(container.textContent).toContain('—');
  });

  it('renders em dash for undefined timestamp', () => {
    const { container } = render(<TimeDisplay timestamp={undefined} />);
    expect(container.textContent).toContain('—');
  });

  it('renders relative format by default', () => {
    render(<TimeDisplay timestamp={mockTimestamp} />);
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
    render(<TimeDisplay timestamp={mockTimestamp} format="both" />);
    expect(screen.getByText(/ago/)).toBeInTheDocument();
    expect(screen.getByText(/2025-02-22/)).toBeInTheDocument();
  });

  it('shows absolute time in output', () => {
    const { container } = render(
      <TimeDisplay timestamp={mockTimestamp} format="relative" />
    );
    // Relative format should still display the absolute time
    expect(container.textContent).toMatch(/\d{4}-\d{2}-\d{2}/);
  });
});
