import { describe, it, expect } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useEventChain } from './use-event-chain'
import type { ManifestGraph } from '../lib/manifest-graph-model'

const emptyGraph: ManifestGraph = { nodes: [], edges: [] }

const graphWithInstrument: ManifestGraph = {
  nodes: [
    {
      id: 'instrument:GBP',
      type: 'instrument',
      label: 'British Pound',
      data: { code: 'GBP' },
    },
  ],
  edges: [],
}

describe('useEventChain', () => {
  it('returns null when graph is null', () => {
    const { result } = renderHook(() => useEventChain(null, 'instrument:GBP'))
    expect(result.current).toBeNull()
  })

  it('returns null when nodeId is null', () => {
    const { result } = renderHook(() => useEventChain(graphWithInstrument, null))
    expect(result.current).toBeNull()
  })

  it('returns null when both are null', () => {
    const { result } = renderHook(() => useEventChain(null, null))
    expect(result.current).toBeNull()
  })

  it('returns an EventChain when both graph and nodeId are provided', () => {
    const { result } = renderHook(() => useEventChain(graphWithInstrument, 'instrument:GBP'))
    expect(result.current).not.toBeNull()
    expect(result.current).toHaveProperty('hops')
    expect(result.current).toHaveProperty('terminationReason')
    expect(result.current).toHaveProperty('maxDepthUsed')
  })

  it('returns no_matching_sagas for instrument with no sagas in graph', () => {
    const { result } = renderHook(() => useEventChain(graphWithInstrument, 'instrument:GBP'))
    expect(result.current!.terminationReason).toBe('no_matching_sagas')
    expect(result.current!.hops).toHaveLength(0)
  })

  it('returns no_matching_sagas for non-existent node', () => {
    const { result } = renderHook(() => useEventChain(emptyGraph, 'instrument:FAKE'))
    expect(result.current!.terminationReason).toBe('no_matching_sagas')
  })

  it('memoizes result when inputs do not change', () => {
    const { result, rerender } = renderHook(() =>
      useEventChain(graphWithInstrument, 'instrument:GBP'),
    )
    const first = result.current
    rerender()
    expect(result.current).toBe(first)
  })
})
