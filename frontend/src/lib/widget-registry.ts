// Widget registry maps component names (from tenant layout config) to React component
// identifiers. Each entry represents a renderable dashboard widget.
//
// The registry is intentionally kept as a plain object (no lazy imports) so that
// widget-registry.ts can be used in tests without a full React render environment.
// Actual rendering is handled by consumers (e.g. DashboardPage) that resolve the
// component name to its concrete implementation.

export const KNOWN_WIDGETS = [
  'StatCards',
  'ActivityFeed',
  'QuickActions',
] as const

export type KnownWidgetName = (typeof KNOWN_WIDGETS)[number]

const WIDGET_SET = new Set<string>(KNOWN_WIDGETS)

export function isRegisteredWidget(componentName: string): boolean {
  return WIDGET_SET.has(componentName)
}
