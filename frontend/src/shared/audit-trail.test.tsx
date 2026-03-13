import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AuditTrail } from './audit-trail';
import { TooltipProvider } from '@/components/ui/tooltip';

// Mock the API context to provide a fake audit client
const mockListAuditEntries = vi.fn();

vi.mock('@/api/context', () => ({
  useApiClients: () => ({
    audit: {
      listAuditEntries: mockListAuditEntries,
    },
  }),
  useClients: () => ({
    audit: {
      listAuditEntries: mockListAuditEntries,
    },
  }),
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
    },
  });
}

function renderWithQuery(ui: React.ReactElement, queryClient = makeQueryClient()) {
  return render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>{ui}</TooltipProvider>
    </QueryClientProvider>,
  );
}

const INSERT_ENTRY = {
  entryId: 'id-1',
  operation: 1, // INSERT enum value
  changedBy: 'alice',
  timestamp: { seconds: 1_700_000_000, nanos: 0 },
  oldValues: null,
  newValues: { fields: { id: { stringValue: 'abc' }, status: { stringValue: 'ACTIVE' } } },
};

const UPDATE_ENTRY = {
  entryId: 'id-2',
  operation: 2, // UPDATE enum value
  changedBy: 'bob',
  timestamp: { seconds: 1_700_001_000, nanos: 0 },
  oldValues: { fields: { id: { stringValue: 'abc' }, status: { stringValue: 'ACTIVE' } } },
  newValues: { fields: { id: { stringValue: 'abc' }, status: { stringValue: 'FROZEN' } } },
};

const DELETE_ENTRY = {
  entryId: 'id-3',
  operation: 3, // DELETE enum value
  changedBy: 'charlie',
  timestamp: { seconds: 1_700_002_000, nanos: 0 },
  oldValues: { fields: { id: { stringValue: 'abc' }, status: { stringValue: 'FROZEN' } } },
  newValues: null,
};

// ---------------------------------------------------------------------------
// AuditTrail component tests
// ---------------------------------------------------------------------------

describe('AuditTrail', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('loading state', () => {
    it('renders loading skeleton while fetching', () => {
      mockListAuditEntries.mockReturnValue(new Promise(() => {})); // Never resolves
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      expect(screen.getByTestId('audit-trail-skeleton')).toBeInTheDocument();
    });
  });

  describe('empty state', () => {
    beforeEach(() => {
      mockListAuditEntries.mockResolvedValue({ entries: [] });
    });

    it('renders empty state when no entries exist', async () => {
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      await waitFor(() =>
        expect(screen.getByTestId('audit-trail-empty')).toBeInTheDocument(),
      );
    });

    it('empty state shows descriptive message', async () => {
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      await waitFor(() =>
        expect(screen.getByRole('heading', { name: /no audit/i })).toBeInTheDocument(),
      );
    });
  });

  describe('entry rendering', () => {
    beforeEach(() => {
      mockListAuditEntries.mockResolvedValue({
        entries: [INSERT_ENTRY, UPDATE_ENTRY, DELETE_ENTRY],
      });
    });

    it('renders entries in timeline order (most-recent first)', async () => {
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      await waitFor(() =>
        expect(screen.getAllByTestId('audit-entry')).toHaveLength(3),
      );
      const entries = screen.getAllByTestId('audit-entry');
      expect(entries[0]).toHaveTextContent(/delete/i);
      expect(entries[1]).toHaveTextContent(/update/i);
      expect(entries[2]).toHaveTextContent(/insert/i);
    });

    it('shows operation type for each entry', async () => {
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      await waitFor(() =>
        expect(screen.getAllByTestId('audit-entry')).toHaveLength(3),
      );
      expect(screen.getByText(/insert/i)).toBeInTheDocument();
      expect(screen.getByText(/update/i)).toBeInTheDocument();
      expect(screen.getByText(/delete/i)).toBeInTheDocument();
    });

    it('shows changedBy for each entry', async () => {
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      await waitFor(() =>
        expect(screen.getAllByTestId('audit-entry')).toHaveLength(3),
      );
      expect(screen.getByText(/alice/i)).toBeInTheDocument();
      expect(screen.getByText(/bob/i)).toBeInTheDocument();
      expect(screen.getByText(/charlie/i)).toBeInTheDocument();
    });
  });

  describe('error state', () => {
    it('renders error state with retry button on failures', async () => {
      mockListAuditEntries.mockRejectedValue(new Error('Network error'));
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      await waitFor(() =>
        expect(screen.getByTestId('audit-trail-error')).toBeInTheDocument(),
      );
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument();
    });
  });

  describe('query cache', () => {
    it('uses staleTime: 0 to always refetch audit logs', async () => {
      mockListAuditEntries.mockResolvedValue({ entries: [] });

      const qc = new QueryClient({
        defaultOptions: {
          queries: { retry: false, gcTime: 5 * 60 * 1000, staleTime: 0 },
        },
      });

      const { unmount } = renderWithQuery(
        <AuditTrail entityType="customers" entityId="id-1" />,
        qc,
      );
      await waitFor(() => expect(mockListAuditEntries).toHaveBeenCalledTimes(1));
      unmount();

      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />, qc);
      await waitFor(() => expect(mockListAuditEntries).toHaveBeenCalledTimes(2));
    });
  });
});

// ---------------------------------------------------------------------------
// JsonDiffViewer sub-component tests
// ---------------------------------------------------------------------------

import { JsonDiffViewer } from './audit-trail';

describe('JsonDiffViewer', () => {
  it('shows inserted values in success color for INSERT (null oldValue, object newValue)', () => {
    const { container } = render(
      <JsonDiffViewer oldValue={null} newValue={{ id: 'abc', status: 'ACTIVE' }} />,
    );
    expect(screen.getByTestId('diff-inserted')).toBeInTheDocument();
    const inserted = container.querySelector('[data-testid="diff-inserted"]');
    expect(inserted?.className).toMatch(/success/i);
  });

  it('shows deleted values in destructive color for DELETE (object oldValue, null newValue)', () => {
    const { container } = render(
      <JsonDiffViewer oldValue={{ id: 'abc', status: 'ACTIVE' }} newValue={null} />,
    );
    expect(screen.getByTestId('diff-deleted')).toBeInTheDocument();
    const deleted = container.querySelector('[data-testid="diff-deleted"]');
    expect(deleted?.className).toMatch(/destructive/i);
  });

  it('shows before/after diff for UPDATE (both oldValue and newValue present)', () => {
    render(
      <JsonDiffViewer
        oldValue={{ id: 'abc', status: 'ACTIVE' }}
        newValue={{ id: 'abc', status: 'FROZEN' }}
      />,
    );
    expect(screen.getByTestId('diff-before')).toBeInTheDocument();
    expect(screen.getByTestId('diff-after')).toBeInTheDocument();
  });

  it('renders JSON content of inserted values', () => {
    render(
      <JsonDiffViewer oldValue={null} newValue={{ id: 'abc', status: 'ACTIVE' }} />,
    );
    expect(screen.getByTestId('diff-inserted')).toHaveTextContent('ACTIVE');
  });

  it('renders JSON content of deleted values', () => {
    render(
      <JsonDiffViewer oldValue={{ id: 'abc', status: 'FROZEN' }} newValue={null} />,
    );
    expect(screen.getByTestId('diff-deleted')).toHaveTextContent('FROZEN');
  });

  it('shows old and new JSON content in UPDATE diff', () => {
    render(
      <JsonDiffViewer
        oldValue={{ id: 'abc', status: 'ACTIVE' }}
        newValue={{ id: 'abc', status: 'FROZEN' }}
      />,
    );
    expect(screen.getByTestId('diff-before')).toHaveTextContent('ACTIVE');
    expect(screen.getByTestId('diff-after')).toHaveTextContent('FROZEN');
  });

  it('handles null/null gracefully without crashing', () => {
    expect(() =>
      render(<JsonDiffViewer oldValue={null} newValue={null} />),
    ).not.toThrow();
  });
});
