import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { http, HttpResponse } from 'msw';
import { server } from '@/test/msw-handlers';
import { AuditTrail } from './audit-trail';
import { TooltipProvider } from '@/components/ui/tooltip';

vi.mock('@/hooks/use-authenticated-fetch', () => ({
  useAuthenticatedFetch: () => fetch,
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

// Minimal audit entry shape matching our stub types.
// Timestamps use plain numbers (not BigInt) because MSW uses JSON.stringify
// under the hood and BigInt is not JSON-serializable.
const INSERT_ENTRY = {
  entryId: 'id-1',
  operation: 'INSERT',
  changedBy: 'alice',
  timestamp: { seconds: 1_700_000_000, nanos: 0 },
  oldValues: null,
  newValues: { id: 'abc', name: 'Test', status: 'ACTIVE' },
};

const UPDATE_ENTRY = {
  entryId: 'id-2',
  operation: 'UPDATE',
  changedBy: 'bob',
  timestamp: { seconds: 1_700_001_000, nanos: 0 },
  oldValues: { id: 'abc', name: 'Test', status: 'ACTIVE' },
  newValues: { id: 'abc', name: 'Test', status: 'FROZEN' },
};

const DELETE_ENTRY = {
  entryId: 'id-3',
  operation: 'DELETE',
  changedBy: 'charlie',
  timestamp: { seconds: 1_700_002_000, nanos: 0 },
  oldValues: { id: 'abc', name: 'Test', status: 'FROZEN' },
  newValues: null,
};

// ---------------------------------------------------------------------------
// AuditTrail component tests
// ---------------------------------------------------------------------------

describe('AuditTrail', () => {
  describe('loading state', () => {
    it('renders loading skeleton while fetching', () => {
      // MSW will hold the request open — we just check initial render
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      expect(screen.getByTestId('audit-trail-skeleton')).toBeInTheDocument();
    });
  });

  describe('empty state', () => {
    beforeEach(() => {
      server.use(
        http.post('*/meridian.audit.v1.AuditService/*', () =>
          HttpResponse.json({ entries: [] }),
        ),
      );
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
      server.use(
        http.post('*/meridian.audit.v1.AuditService/*', () =>
          HttpResponse.json({ entries: [INSERT_ENTRY, UPDATE_ENTRY, DELETE_ENTRY] }),
        ),
      );
    });

    it('renders entries in timeline order (most-recent first)', async () => {
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      await waitFor(() =>
        expect(screen.getAllByTestId('audit-entry')).toHaveLength(3),
      );
      const entries = screen.getAllByTestId('audit-entry');
      // DELETE_ENTRY has the latest timestamp — should be first
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

  describe('stub fallback', () => {
    it('renders stub banner when audit service is unavailable (501)', async () => {
      server.use(
        http.post('*/meridian.audit.v1.AuditService/*', () =>
          HttpResponse.json({}, { status: 501 }),
        ),
      );
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      await waitFor(() =>
        expect(screen.getByTestId('audit-trail-stub')).toBeInTheDocument(),
      );
    });

    it('renders error state for non-stub failures (500)', async () => {
      server.use(
        http.post('*/meridian.audit.v1.AuditService/*', () =>
          HttpResponse.json({}, { status: 500 }),
        ),
      );
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />);
      await waitFor(() =>
        expect(screen.getByTestId('audit-trail-error')).toBeInTheDocument(),
      );
    });
  });

  describe('query cache', () => {
    it('uses staleTime: 0 to always refetch audit logs', async () => {
      // We verify this by checking that the component issues a request on each
      // mount even when data might be cached. MSW intercepts all calls so we
      // can count them via a spy.
      //
      // We must use a non-zero gcTime so the cache persists across unmount/remount.
      // With gcTime: 0, the cache is evicted immediately on unmount regardless of
      // staleTime, which would make this test pass even if staleTime were Infinity.
      const fetchSpy = vi.fn(() => HttpResponse.json({ entries: [] }));
      server.use(
        http.post('*/meridian.audit.v1.AuditService/*', fetchSpy),
      );

      const qc = new QueryClient({
        defaultOptions: {
          queries: { retry: false, gcTime: 5 * 60 * 1000, staleTime: 0 },
        },
      });

      const { unmount } = renderWithQuery(
        <AuditTrail entityType="customers" entityId="id-1" />,
        qc,
      );
      // Wait for first fetch to complete
      await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(1));
      unmount();

      // Second mount — cache entry is present but staleTime: 0 means it's
      // immediately stale, so the component triggers a refetch on mount.
      renderWithQuery(<AuditTrail entityType="customers" entityId="id-1" />, qc);
      await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(2));
    });
  });
});

// ---------------------------------------------------------------------------
// JsonDiffViewer sub-component tests (exported for direct testing)
// ---------------------------------------------------------------------------

import { JsonDiffViewer } from './audit-trail';

describe('JsonDiffViewer', () => {
  it('shows inserted values in green for INSERT (null oldValue, object newValue)', () => {
    const { container } = render(
      <JsonDiffViewer oldValue={null} newValue={{ id: 'abc', status: 'ACTIVE' }} />,
    );
    expect(screen.getByTestId('diff-inserted')).toBeInTheDocument();
    // Green styling
    const inserted = container.querySelector('[data-testid="diff-inserted"]');
    expect(inserted?.className).toMatch(/green/i);
  });

  it('shows deleted values in red for DELETE (object oldValue, null newValue)', () => {
    const { container } = render(
      <JsonDiffViewer oldValue={{ id: 'abc', status: 'ACTIVE' }} newValue={null} />,
    );
    expect(screen.getByTestId('diff-deleted')).toBeInTheDocument();
    const deleted = container.querySelector('[data-testid="diff-deleted"]');
    expect(deleted?.className).toMatch(/red/i);
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
