import { describe, it, expect } from 'vitest'
import type { ManifestEdge } from '../../lib/manifest-graph-model'
import { buildReactFlowEdges, pickHandles } from './graph-layout'

describe('pickHandles', () => {
  it('connects right-to-left when the target is mostly to the right', () => {
    expect(pickHandles({ x: 0, y: 0 }, { x: 100, y: 10 })).toEqual({
      sourceHandle: 'right',
      targetHandle: 'left-target',
    })
  })

  it('connects left-to-right when the target is mostly to the left', () => {
    expect(pickHandles({ x: 100, y: 0 }, { x: 0, y: 10 })).toEqual({
      sourceHandle: 'left',
      targetHandle: 'right-target',
    })
  })

  it('connects bottom-to-top when the target is mostly below', () => {
    expect(pickHandles({ x: 0, y: 0 }, { x: 10, y: 100 })).toEqual({
      sourceHandle: 'bottom',
      targetHandle: 'top-target',
    })
  })

  it('connects top-to-bottom when the target is mostly above', () => {
    expect(pickHandles({ x: 0, y: 100 }, { x: 10, y: 0 })).toEqual({
      sourceHandle: 'top',
      targetHandle: 'bottom-target',
    })
  })
})

describe('buildReactFlowEdges', () => {
  const edge = (relationship: ManifestEdge['relationship']): ManifestEdge => ({
    id: `e-${relationship}`,
    source: 'a',
    target: 'b',
    relationship,
  })

  it('preserves id, source, target, and relationship data', () => {
    const [rf] = buildReactFlowEdges([edge('allowed_by')])
    expect(rf).toMatchObject({ id: 'e-allowed_by', source: 'a', target: 'b', data: { relationship: 'allowed_by' } })
  })

  it('applies the styled stroke for known relationships', () => {
    const [rf] = buildReactFlowEdges([edge('allowed_by')])
    expect(rf.style).toMatchObject({ stroke: 'var(--graph-instrument)', strokeWidth: 2 })
  })

  it('adds an arrow marker for allowed_by and converts_to edges', () => {
    const [allowed, convertsTo] = buildReactFlowEdges([edge('allowed_by'), edge('converts_to')])
    expect(allowed.markerEnd).toMatchObject({ type: 'arrowclosed' })
    expect(convertsTo.markerEnd).toMatchObject({ type: 'arrowclosed' })
  })

  it('omits the arrow marker for converts_from edges', () => {
    const [rf] = buildReactFlowEdges([edge('converts_from')])
    expect(rf.markerEnd).toBeUndefined()
  })

  it('falls back to an empty style for relationships without a defined style', () => {
    const [rf] = buildReactFlowEdges([edge('belongs_to')])
    expect(rf.style).toEqual({})
    expect(rf.markerEnd).toBeUndefined()
  })
})
