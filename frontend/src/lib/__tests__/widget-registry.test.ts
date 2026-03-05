import { describe, it, expect } from 'vitest'
import { KNOWN_WIDGETS, isRegisteredWidget } from '../widget-registry'

describe('widget-registry', () => {
  it('KNOWN_WIDGETS contains expected entries', () => {
    expect(KNOWN_WIDGETS).toContain('StatCards')
    expect(KNOWN_WIDGETS).toContain('ActivityFeed')
    expect(KNOWN_WIDGETS).toContain('QuickActions')
  })

  it('isRegisteredWidget returns true for known widgets', () => {
    for (const name of KNOWN_WIDGETS) {
      expect(isRegisteredWidget(name)).toBe(true)
    }
  })

  it('isRegisteredWidget returns false for unknown component names', () => {
    expect(isRegisteredWidget('UnknownWidget')).toBe(false)
    expect(isRegisteredWidget('')).toBe(false)
    expect(isRegisteredWidget('statcards')).toBe(false) // case-sensitive
  })
})
