import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useCookbook } from './use-cookbook'

vi.mock('virtual:cookbook-data', () => ({
  default: {
    name: 'meridian-cookbook',
    items: [
      {
        name: 'energy-trading',
        type: 'registry:pattern',
        title: 'Energy Trading',
        description: 'Buy and sell electricity',
        categories: ['energy'],
        meta: { complexity: 7 },
      },
      {
        name: 'balance-card',
        type: 'registry:ui',
        title: 'Balance Card',
      },
    ],
  },
}))

describe('useCookbook', () => {
  it('returns items from virtual cookbook data', () => {
    const { result } = renderHook(() => useCookbook())
    expect(result.current.items).toHaveLength(2)
    expect(result.current.items[0].name).toBe('energy-trading')
    expect(result.current.items[1].name).toBe('balance-card')
  })

  it('returns isLoading as false (static data)', () => {
    const { result } = renderHook(() => useCookbook())
    expect(result.current.isLoading).toBe(false)
  })

  it('includes correct types for items', () => {
    const { result } = renderHook(() => useCookbook())
    expect(result.current.items[0].type).toBe('registry:pattern')
    expect(result.current.items[1].type).toBe('registry:ui')
  })
})
