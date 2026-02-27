import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { ErrorBoundary, PageErrorBoundary, RouteErrorBoundary, RouteErrorBoundaryInner } from '@/components/error-boundary'

// Component that throws an error
function ThrowError() {
  throw new Error('Test error')
}

// Component that doesn't throw
function SafeComponent() {
  return <div>Safe content</div>
}

describe('ErrorBoundary', () => {
  beforeEach(() => {
    // Suppress console.error during tests
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  describe('catches render errors', () => {
    it('renders default fallback UI when error occurs', () => {
      render(
        <ErrorBoundary>
          <ThrowError />
        </ErrorBoundary>
      )

      expect(screen.getByText('Something went wrong')).toBeInTheDocument()
      expect(
        screen.getByText('An unexpected error occurred.')
      ).toBeInTheDocument()
    })

    it('renders children when no error occurs', () => {
      render(
        <ErrorBoundary>
          <SafeComponent />
        </ErrorBoundary>
      )

      expect(screen.getByText('Safe content')).toBeInTheDocument()
    })
  })

  describe('retry functionality', () => {
    it('retry button remounts children', async () => {
      const user = userEvent.setup()
      const { rerender } = render(
        <ErrorBoundary>
          <ThrowError />
        </ErrorBoundary>
      )

      // Error should be displayed
      expect(screen.getByText('Something went wrong')).toBeInTheDocument()

      // Now rerender with safe component
      rerender(
        <ErrorBoundary>
          <SafeComponent />
        </ErrorBoundary>
      )

      const retryButton = screen.getByRole('button', { name: /retry/i })
      await user.click(retryButton)

      expect(screen.getByText('Safe content')).toBeInTheDocument()
    })
  })

  describe('custom fallback prop', () => {
    it('renders custom fallback instead of default', () => {
      render(
        <ErrorBoundary fallback={<div>Custom error UI</div>}>
          <ThrowError />
        </ErrorBoundary>
      )

      expect(screen.getByText('Custom error UI')).toBeInTheDocument()
    })
  })

  describe('error callback', () => {
    it('calls onError callback when error occurs', () => {
      const mockOnError = vi.fn()

      render(
        <ErrorBoundary onError={mockOnError}>
          <ThrowError />
        </ErrorBoundary>
      )

      expect(mockOnError).toHaveBeenCalled()
      const [error, errorInfo] = mockOnError.mock.calls[0]
      expect(error.message).toBe('Test error')
      expect(errorInfo).toHaveProperty('componentStack')
    })
  })

  describe('navigation action', () => {
    it('has go to dashboard button', () => {
      render(
        <ErrorBoundary>
          <ThrowError />
        </ErrorBoundary>
      )

      const dashboardButton = screen.getByRole('button', {
        name: /go to dashboard/i,
      })
      expect(dashboardButton).toBeInTheDocument()
    })
  })
})

describe('PageErrorBoundary', () => {
  beforeEach(() => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('renders error boundary for page-level usage', () => {
    render(
      <PageErrorBoundary>
        <ThrowError />
      </PageErrorBoundary>
    )

    expect(screen.getByText('Something went wrong')).toBeInTheDocument()
  })

  it('accepts onError callback', () => {
    const mockOnError = vi.fn()

    render(
      <PageErrorBoundary onError={mockOnError}>
        <ThrowError />
      </PageErrorBoundary>
    )

    expect(mockOnError).toHaveBeenCalled()
  })

  it('renders children when no error', () => {
    render(
      <PageErrorBoundary>
        <SafeComponent />
      </PageErrorBoundary>
    )

    expect(screen.getByText('Safe content')).toBeInTheDocument()
  })
})

describe('RouteErrorBoundary', () => {
  beforeEach(() => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  it('renders inline error instead of full-page crash', () => {
    render(
      <MemoryRouter>
        <RouteErrorBoundary>
          <ThrowError />
        </RouteErrorBoundary>
      </MemoryRouter>
    )

    expect(screen.getByText('Failed to load page')).toBeInTheDocument()
    expect(
      screen.getByText('This page encountered an error. Other pages should still work normally.')
    ).toBeInTheDocument()
  })

  it('shows error message', () => {
    render(
      <MemoryRouter>
        <RouteErrorBoundary>
          <ThrowError />
        </RouteErrorBoundary>
      </MemoryRouter>
    )

    expect(screen.getByText('Test error')).toBeInTheDocument()
  })

  it('renders children when no error', () => {
    render(
      <MemoryRouter>
        <RouteErrorBoundary>
          <SafeComponent />
        </RouteErrorBoundary>
      </MemoryRouter>
    )

    expect(screen.getByText('Safe content')).toBeInTheDocument()
  })

  it('retry button resets error state', async () => {
    const user = userEvent.setup()
    const { rerender } = render(
      <MemoryRouter>
        <RouteErrorBoundaryInner>
          <ThrowError />
        </RouteErrorBoundaryInner>
      </MemoryRouter>
    )

    expect(screen.getByText('Failed to load page')).toBeInTheDocument()

    rerender(
      <MemoryRouter>
        <RouteErrorBoundaryInner>
          <SafeComponent />
        </RouteErrorBoundaryInner>
      </MemoryRouter>
    )

    const retryButton = screen.getByRole('button', { name: /retry/i })
    await user.click(retryButton)

    expect(screen.getByText('Safe content')).toBeInTheDocument()
  })

  it('does not show Go to Dashboard button (stays in layout)', () => {
    render(
      <MemoryRouter>
        <RouteErrorBoundary>
          <ThrowError />
        </RouteErrorBoundary>
      </MemoryRouter>
    )

    expect(screen.queryByRole('button', { name: /go to dashboard/i })).not.toBeInTheDocument()
  })

  it('resets error state when route changes', () => {
    const { rerender } = render(
      <RouteErrorBoundaryInner resetKey="/parties">
        <ThrowError />
      </RouteErrorBoundaryInner>
    )

    expect(screen.getByText('Failed to load page')).toBeInTheDocument()

    // Simulate navigation by changing resetKey and providing safe children
    rerender(
      <RouteErrorBoundaryInner resetKey="/accounts">
        <SafeComponent />
      </RouteErrorBoundaryInner>
    )

    expect(screen.getByText('Safe content')).toBeInTheDocument()
  })
})
