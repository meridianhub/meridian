import { PageErrorBoundary } from '@/components/error-boundary'

export interface Route {
  path: string
  name: string
  component: React.ComponentType
}

const routes: Route[] = [
  {
    path: '/',
    name: 'Dashboard',
    component: () => <div>Dashboard</div>,
  },
]

export function getRoutes(): Route[] {
  return routes
}

export function wrapRouteWithErrorBoundary(
  component: React.ComponentType
): React.ComponentType {
  return function WrappedRoute() {
    const Component = component
    return (
      <PageErrorBoundary>
        <Component />
      </PageErrorBoundary>
    )
  }
}

export function createRouteHandler(
  route: Route
): { path: string; name: string; component: React.ComponentType } {
  return {
    path: route.path,
    name: route.name,
    component: wrapRouteWithErrorBoundary(route.component),
  }
}

export function getRouteHandlers() {
  return getRoutes().map(createRouteHandler)
}
