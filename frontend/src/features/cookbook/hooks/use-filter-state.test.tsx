import { describe, it, expect, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { ReactNode } from 'react'
import { useFilterState } from './use-filter-state'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({})),
}))

function wrapper({ children }: { children: ReactNode }) {
  return <MemoryRouter>{children}</MemoryRouter>
}

function wrapperWithParams(search: string) {
  return ({ children }: { children: ReactNode }) => (
    <MemoryRouter initialEntries={[`/?${search}`]}>{children}</MemoryRouter>
  )
}

describe('useFilterState', () => {
  it('returns empty filters by default', () => {
    const { result } = renderHook(() => useFilterState(), { wrapper })
    const [state] = result.current
    expect(state.search).toBe('')
    expect(state.type).toBe('')
    expect(state.category).toBe('')
    expect(state.industry).toBe('')
    expect(state.kind).toBe('')
  })

  it('reads initial state from URL search params', () => {
    const { result } = renderHook(() => useFilterState(), {
      wrapper: wrapperWithParams('search=energy&type=pattern'),
    })
    const [state] = result.current
    expect(state.search).toBe('energy')
    expect(state.type).toBe('pattern')
    expect(state.category).toBe('')
    expect(state.industry).toBe('')
  })

  it('updates filters via the setter function', () => {
    const { result } = renderHook(() => useFilterState(), { wrapper })

    act(() => {
      const [, update] = result.current
      update({ search: 'test', type: 'ui' })
    })

    const [state] = result.current
    expect(state.search).toBe('test')
    expect(state.type).toBe('ui')
  })

  it('removes empty values from URL params', () => {
    const { result } = renderHook(() => useFilterState(), {
      wrapper: wrapperWithParams('search=hello&type=pattern'),
    })

    act(() => {
      const [, update] = result.current
      update({ search: '' })
    })

    const [state] = result.current
    expect(state.search).toBe('')
    expect(state.type).toBe('pattern')
  })

  it('supports partial updates', () => {
    const { result } = renderHook(() => useFilterState(), { wrapper })

    act(() => {
      result.current[1]({ category: 'energy' })
    })

    act(() => {
      result.current[1]({ industry: 'utilities' })
    })

    const [state] = result.current
    expect(state.category).toBe('energy')
    expect(state.industry).toBe('utilities')
  })

  it('clears all filters at once', () => {
    const { result } = renderHook(() => useFilterState(), {
      wrapper: wrapperWithParams('search=x&type=pattern&category=energy&industry=fin'),
    })

    act(() => {
      result.current[1]({ search: '', type: '', category: '', industry: '', kind: '' })
    })

    const [state] = result.current
    expect(state.search).toBe('')
    expect(state.type).toBe('')
    expect(state.category).toBe('')
    expect(state.industry).toBe('')
  })
})
