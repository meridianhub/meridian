import { describe, it, expect } from 'vitest'
import type { ManifestGraph, ManifestNode, ManifestEdge } from './manifest-graph-model'
import { computeManifestDiff } from './manifest-diff'

function makeNode(overrides: Partial<ManifestNode> & { id: string }): ManifestNode {
  return {
    type: 'instrument',
    label: overrides.id,
    data: {},
    ...overrides,
  }
}

function makeEdge(overrides: Partial<ManifestEdge> & { id: string }): ManifestEdge {
  return {
    source: 'a',
    target: 'b',
    relationship: 'allowed_by',
    ...overrides,
  }
}

function makeGraph(nodes: ManifestNode[], edges: ManifestEdge[] = []): ManifestGraph {
  return { nodes, edges }
}

describe('computeManifestDiff', () => {
  it('detects added nodes', () => {
    const before = makeGraph([makeNode({ id: 'n1' })])
    const after = makeGraph([makeNode({ id: 'n1' }), makeNode({ id: 'n2' })])

    const diff = computeManifestDiff(before, after)

    expect(diff.addedNodes).toHaveLength(1)
    expect(diff.addedNodes[0].id).toBe('n2')
    expect(diff.removedNodes).toHaveLength(0)
    expect(diff.modifiedNodes).toHaveLength(0)
  })

  it('detects removed nodes', () => {
    const before = makeGraph([makeNode({ id: 'n1' }), makeNode({ id: 'n2' })])
    const after = makeGraph([makeNode({ id: 'n1' })])

    const diff = computeManifestDiff(before, after)

    expect(diff.removedNodes).toHaveLength(1)
    expect(diff.removedNodes[0].id).toBe('n2')
    expect(diff.addedNodes).toHaveLength(0)
  })

  it('detects modified nodes by label change', () => {
    const before = makeGraph([makeNode({ id: 'n1', label: 'Old Label' })])
    const after = makeGraph([makeNode({ id: 'n1', label: 'New Label' })])

    const diff = computeManifestDiff(before, after)

    expect(diff.modifiedNodes).toHaveLength(1)
    expect(diff.modifiedNodes[0].before.label).toBe('Old Label')
    expect(diff.modifiedNodes[0].after.label).toBe('New Label')
  })

  it('detects modified nodes by data change', () => {
    const before = makeGraph([makeNode({ id: 'n1', data: { foo: 'bar' } })])
    const after = makeGraph([makeNode({ id: 'n1', data: { foo: 'baz' } })])

    const diff = computeManifestDiff(before, after)

    expect(diff.modifiedNodes).toHaveLength(1)
  })

  it('detects added edges', () => {
    const n1 = makeNode({ id: 'n1' })
    const n2 = makeNode({ id: 'n2' })
    const edge = makeEdge({ id: 'e1', source: 'n1', target: 'n2' })

    const before = makeGraph([n1, n2])
    const after = makeGraph([n1, n2], [edge])

    const diff = computeManifestDiff(before, after)

    expect(diff.addedEdges).toHaveLength(1)
    expect(diff.addedEdges[0].id).toBe('e1')
    expect(diff.removedEdges).toHaveLength(0)
  })

  it('detects removed edges', () => {
    const n1 = makeNode({ id: 'n1' })
    const n2 = makeNode({ id: 'n2' })
    const edge = makeEdge({ id: 'e1', source: 'n1', target: 'n2' })

    const before = makeGraph([n1, n2], [edge])
    const after = makeGraph([n1, n2])

    const diff = computeManifestDiff(before, after)

    expect(diff.removedEdges).toHaveLength(1)
    expect(diff.removedEdges[0].id).toBe('e1')
    expect(diff.addedEdges).toHaveLength(0)
  })

  it('returns empty diff for identical graphs', () => {
    const n1 = makeNode({ id: 'n1', label: 'Test', data: { code: 'A' } })
    const e1 = makeEdge({ id: 'e1', source: 'n1', target: 'n1' })
    const graph = makeGraph([n1], [e1])

    const diff = computeManifestDiff(graph, graph)

    expect(diff.addedNodes).toHaveLength(0)
    expect(diff.removedNodes).toHaveLength(0)
    expect(diff.modifiedNodes).toHaveLength(0)
    expect(diff.addedEdges).toHaveLength(0)
    expect(diff.removedEdges).toHaveLength(0)
  })

  it('handles empty graphs', () => {
    const diff = computeManifestDiff(makeGraph([]), makeGraph([]))

    expect(diff.addedNodes).toHaveLength(0)
    expect(diff.removedNodes).toHaveLength(0)
    expect(diff.modifiedNodes).toHaveLength(0)
    expect(diff.addedEdges).toHaveLength(0)
    expect(diff.removedEdges).toHaveLength(0)
  })

  it('handles complex diff with adds, removes, and modifications', () => {
    const before = makeGraph(
      [
        makeNode({ id: 'n1', label: 'Keep' }),
        makeNode({ id: 'n2', label: 'Remove' }),
        makeNode({ id: 'n3', label: 'Modify Old' }),
      ],
      [makeEdge({ id: 'e1', source: 'n1', target: 'n2' })],
    )
    const after = makeGraph(
      [
        makeNode({ id: 'n1', label: 'Keep' }),
        makeNode({ id: 'n3', label: 'Modify New' }),
        makeNode({ id: 'n4', label: 'Added' }),
      ],
      [makeEdge({ id: 'e2', source: 'n1', target: 'n4' })],
    )

    const diff = computeManifestDiff(before, after)

    expect(diff.addedNodes).toHaveLength(1)
    expect(diff.addedNodes[0].id).toBe('n4')
    expect(diff.removedNodes).toHaveLength(1)
    expect(diff.removedNodes[0].id).toBe('n2')
    expect(diff.modifiedNodes).toHaveLength(1)
    expect(diff.modifiedNodes[0].after.label).toBe('Modify New')
    expect(diff.addedEdges).toHaveLength(1)
    expect(diff.removedEdges).toHaveLength(1)
  })
})
