import { Component, ErrorInfo, ReactNode } from 'react'
import { AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface Props {
  children: ReactNode
  fallback?: ReactNode
  onError?: (error: Error, errorInfo: ErrorInfo) => void
}

interface State {
  hasError: boolean
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = {
      hasError: false,
      error: null,
    }
  }

  static getDerivedStateFromError(error: Error): State {
    return {
      hasError: true,
      error,
    }
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('Error boundary caught:', error, errorInfo)
    this.props.onError?.(error, errorInfo)
  }

  handleRetry = () => {
    this.setState({
      hasError: false,
      error: null,
    })
  }

  handleDashboard = () => {
    window.location.href = '/'
  }

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) {
        return this.props.fallback
      }

      return (
        <div className="flex items-center justify-center min-h-screen bg-background">
          <div className="max-w-md w-full space-y-4">
            <div className="flex items-center gap-3">
              <AlertTriangle className="size-8 text-destructive" />
              <h1 className="text-2xl font-bold">Something went wrong</h1>
            </div>

            <p className="text-muted-foreground">
              An unexpected error occurred.
            </p>

            {this.state.error && (
              <div className="rounded-lg bg-muted p-3 text-sm font-mono text-muted-foreground break-words">
                {this.state.error.message}
              </div>
            )}

            <div className="flex gap-2 pt-4">
              <Button onClick={this.handleRetry} variant="default">
                Retry
              </Button>
              <Button onClick={this.handleDashboard} variant="outline">
                Go to Dashboard
              </Button>
            </div>
          </div>
        </div>
      )
    }

    return this.props.children
  }
}

interface PageErrorBoundaryProps {
  children: ReactNode
  onError?: (error: Error, errorInfo: ErrorInfo) => void
}

export function PageErrorBoundary({
  children,
  onError,
}: PageErrorBoundaryProps) {
  return (
    <ErrorBoundary onError={onError}>
      {children}
    </ErrorBoundary>
  )
}
