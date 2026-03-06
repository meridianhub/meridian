import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import cookbookBundler from './cookbook-bundler'

function createTempCookbook() {
  const dir = join(tmpdir(), `cookbook-test-${Date.now()}`)
  mkdirSync(join(dir, 'patterns', 'energy-settlement'), { recursive: true })
  mkdirSync(join(dir, 'ui', 'activity-feed'), { recursive: true })
  return dir
}

describe('cookbook-bundler', () => {
  let tempDir: string

  beforeEach(() => {
    tempDir = createTempCookbook()
  })

  afterEach(() => {
    rmSync(tempDir, { recursive: true, force: true })
  })

  it('resolves the virtual module ID', () => {
    const plugin = cookbookBundler({ cookbookDir: tempDir })
    const resolveId = plugin.resolveId as (id: string) => string | undefined
    expect(resolveId('virtual:cookbook-data')).toBe('\0virtual:cookbook-data')
    expect(resolveId('some-other-module')).toBeUndefined()
  })

  it('loads empty items when registry.json is missing', () => {
    const plugin = cookbookBundler({ cookbookDir: tempDir })
    const load = plugin.load as (id: string) => string | undefined
    const result = load('\0virtual:cookbook-data')
    expect(result).toContain('export default')
    const data = JSON.parse(result!.replace('export default ', '').replace(';', ''))
    expect(data.items).toEqual([])
  })

  it('loads registry items and merges pattern.json metadata', () => {
    writeFileSync(
      join(tempDir, 'registry.json'),
      JSON.stringify({
        name: 'test-cookbook',
        items: [
          { name: 'energy-settlement', type: 'registry:pattern', title: 'Energy' },
          { name: 'activity-feed', type: 'registry:ui', title: 'Activity Feed' },
        ],
      }),
    )

    writeFileSync(
      join(tempDir, 'patterns', 'energy-settlement', 'pattern.json'),
      JSON.stringify({
        name: 'energy-settlement',
        type: 'registry:pattern',
        title: 'Energy',
        description: 'Converts kWh into value',
        categories: ['energy'],
        meta: { complexity: 3, design_pattern: 'cross-instrument-valuation' },
        files: [{ path: 'patterns/energy-settlement/manifest-fragment.yaml', type: 'registry:file' }],
      }),
    )

    writeFileSync(
      join(tempDir, 'ui', 'activity-feed', 'component.json'),
      JSON.stringify({
        name: 'activity-feed',
        type: 'registry:ui',
        title: 'Activity Feed',
        description: 'Displays events',
        meta: { feature_module: 'dashboard' },
      }),
    )

    const plugin = cookbookBundler({ cookbookDir: tempDir })
    const load = plugin.load as (id: string) => string | undefined
    const result = load('\0virtual:cookbook-data')
    const data = JSON.parse(result!.replace('export default ', '').replace(';', ''))

    expect(data.name).toBe('test-cookbook')
    expect(data.items).toHaveLength(2)

    const pattern = data.items[0]
    expect(pattern.description).toBe('Converts kWh into value')
    expect(pattern.meta.complexity).toBe(3)
    expect(pattern.files).toHaveLength(1)

    const ui = data.items[1]
    expect(ui.description).toBe('Displays events')
    expect(ui.meta.feature_module).toBe('dashboard')
  })

  it('handles missing pattern.json gracefully', () => {
    writeFileSync(
      join(tempDir, 'registry.json'),
      JSON.stringify({
        name: 'test-cookbook',
        items: [
          { name: 'no-detail', type: 'registry:pattern', title: 'No Detail' },
        ],
      }),
    )

    const plugin = cookbookBundler({ cookbookDir: tempDir })
    const load = plugin.load as (id: string) => string | undefined
    const result = load('\0virtual:cookbook-data')
    const data = JSON.parse(result!.replace('export default ', '').replace(';', ''))

    expect(data.items).toHaveLength(1)
    expect(data.items[0].name).toBe('no-detail')
    expect(data.items[0].meta).toBeUndefined()
  })
})
