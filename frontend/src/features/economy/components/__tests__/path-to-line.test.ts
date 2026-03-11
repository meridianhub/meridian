import { describe, it, expect } from 'vitest'
import { pathToLine } from '../manifest-editor'

const sampleYaml = `name: test-economy
instruments:
  - code: GBP
    dimension: CURRENCY
  - code: kWh
    dimension: ENERGY
account_types:
  - code: CUSTOMER
    behavior_class: CUSTOMER
sagas:
  - name: payment
    script: |
      def run():
        pass`

describe('pathToLine', () => {
  it('finds top-level key', () => {
    expect(pathToLine(sampleYaml, 'name')).toBe(1)
  })

  it('finds nested array element key', () => {
    expect(pathToLine(sampleYaml, 'instruments[0].code')).toBe(3)
  })

  it('finds second array element', () => {
    expect(pathToLine(sampleYaml, 'instruments[1].code')).toBe(5)
  })

  it('finds second top-level section', () => {
    expect(pathToLine(sampleYaml, 'account_types')).toBe(7)
  })

  it('returns null for empty path', () => {
    expect(pathToLine(sampleYaml, '')).toBeNull()
  })

  it('returns null for non-existent path', () => {
    expect(pathToLine(sampleYaml, 'nonexistent')).toBeNull()
  })

  it('returns null for empty source', () => {
    expect(pathToLine('', 'instruments[0].code')).toBeNull()
  })

  it('handles path with only array key', () => {
    expect(pathToLine(sampleYaml, 'instruments')).toBe(2)
  })
})
