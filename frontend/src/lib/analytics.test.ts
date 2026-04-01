import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { track } from './analytics'

describe('track', () => {
  beforeEach(() => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('logs to console in dev mode', () => {
    // import.meta.env.DEV is true in vitest by default
    track('economy.platform_badge_visible', {
      page: 'sagas',
      platform_count: 3,
      tenant_count: 1,
    })
    expect(console.log).toHaveBeenCalledWith(
      '[Analytics]',
      'economy.platform_badge_visible',
      { page: 'sagas', platform_count: 3, tenant_count: 1 },
    )
  })

  it('accepts economy.platform_resource_clicked event', () => {
    track('economy.platform_resource_clicked', {
      resource_type: 'saga',
      resource_code: 'current_account_withdrawal',
      page: 'sagas',
    })
    expect(console.log).toHaveBeenCalledWith(
      '[Analytics]',
      'economy.platform_resource_clicked',
      { resource_type: 'saga', resource_code: 'current_account_withdrawal', page: 'sagas' },
    )
  })

  it('accepts economy.override_intent event', () => {
    track('economy.override_intent', {
      source_saga_name: 'current_account_withdrawal',
      navigation_path: '/starlark-config/new',
    })
    expect(console.log).toHaveBeenCalledWith(
      '[Analytics]',
      'economy.override_intent',
      { source_saga_name: 'current_account_withdrawal', navigation_path: '/starlark-config/new' },
    )
  })

  it('accepts economy.empty_state_shown event', () => {
    track('economy.empty_state_shown', { page: 'overview', hasManifest: false })
    expect(console.log).toHaveBeenCalledWith(
      '[Analytics]',
      'economy.empty_state_shown',
      { page: 'overview', hasManifest: false },
    )
  })
})
