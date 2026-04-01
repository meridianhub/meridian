// ---------------------------------------------------------------------------
// Analytics utility
// ---------------------------------------------------------------------------
// Lightweight event tracking shim. In dev mode, events are logged to console.
// Swap the commented-out line to wire up a real analytics provider.
// ---------------------------------------------------------------------------

export interface PlatformBadgeVisibleEvent {
  page: string
  platform_count: number
  tenant_count: number
}

export interface PlatformResourceClickedEvent {
  resource_type: string
  resource_code: string
  page: string
}

export interface OverrideIntentEvent {
  source_saga_name: string
  navigation_path: string
}

export interface EmptyStateShownEvent {
  page: string
  hasManifest: boolean
}

export type AnalyticsEvents = {
  'economy.platform_badge_visible': PlatformBadgeVisibleEvent
  'economy.platform_resource_clicked': PlatformResourceClickedEvent
  'economy.override_intent': OverrideIntentEvent
  'economy.empty_state_shown': EmptyStateShownEvent
}

export function track<E extends keyof AnalyticsEvents>(
  event: E,
  properties?: AnalyticsEvents[E],
): void {
  if (import.meta.env.DEV) {
    console.log('[Analytics]', event, properties)
  }
  // Future: window.analytics?.track(event, properties)
}
