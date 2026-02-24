import * as React from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Plus, Trash2 } from 'lucide-react'

export interface KeyValueEditorProps {
  value: Record<string, string>
  onChange: (value: Record<string, string>) => void
}

interface KVPair {
  id: number
  key: string
  val: string
}

function pairsFromRecord(record: Record<string, string>, startId: number): KVPair[] {
  return Object.entries(record).map(([key, val], i) => ({ id: startId + i, key, val }))
}

function toRecord(pairs: KVPair[]): Record<string, string> {
  const record: Record<string, string> = {}
  for (const p of pairs) {
    if (p.key.trim()) {
      record[p.key.trim()] = p.val
    }
  }
  return record
}

export function KeyValueEditor({ value, onChange }: KeyValueEditorProps) {
  const nextIdRef = React.useRef(0)

  const [pairs, setPairs] = React.useState<KVPair[]>(() => {
    const initial = pairsFromRecord(value, nextIdRef.current)
    nextIdRef.current += initial.length
    return initial
  })

  // Sync pairs when external value is reset (e.g., dialog reset)
  const prevValueRef = React.useRef(value)
  React.useEffect(() => {
    if (prevValueRef.current !== value) {
      prevValueRef.current = value
      // Only reset if the external value is structurally different from current internal state
      const currentRecord = toRecord(pairs)
      const currentJson = JSON.stringify(currentRecord, Object.keys(currentRecord).sort())
      const newJson = JSON.stringify(value, Object.keys(value).sort())
      if (currentJson !== newJson) {
        const newPairs = pairsFromRecord(value, nextIdRef.current)
        nextIdRef.current += newPairs.length
        setPairs(newPairs)
      }
    }
  })

  function handleAdd() {
    const id = nextIdRef.current++
    const updated = [...pairs, { id, key: '', val: '' }]
    setPairs(updated)
    onChange(toRecord(updated))
  }

  function handleRemove(id: number) {
    const updated = pairs.filter((p) => p.id !== id)
    setPairs(updated)
    onChange(toRecord(updated))
  }

  function handleKeyChange(id: number, key: string) {
    const updated = pairs.map((p) => (p.id === id ? { ...p, key } : p))
    setPairs(updated)
    onChange(toRecord(updated))
  }

  function handleValChange(id: number, val: string) {
    const updated = pairs.map((p) => (p.id === id ? { ...p, val } : p))
    setPairs(updated)
    onChange(toRecord(updated))
  }

  // Detect duplicate keys
  const keyCounts: Record<string, number> = {}
  for (const p of pairs) {
    const k = p.key.trim()
    if (k) keyCounts[k] = (keyCounts[k] ?? 0) + 1
  }

  return (
    <div className="space-y-2" data-testid="key-value-editor">
      {pairs.map((p, index) => {
        const isDuplicate = p.key.trim() && (keyCounts[p.key.trim()] ?? 0) > 1
        return (
          <div key={p.id} className="flex items-center gap-2">
            <div className="flex-1 space-y-0.5">
              <Input
                value={p.key}
                onChange={(e) => handleKeyChange(p.id, e.target.value)}
                placeholder="key"
                aria-label={`Attribute key ${index + 1}`}
                className={isDuplicate ? 'border-destructive' : ''}
              />
              {isDuplicate && (
                <p className="text-xs text-destructive">Duplicate key</p>
              )}
            </div>
            <Input
              value={p.val}
              onChange={(e) => handleValChange(p.id, e.target.value)}
              placeholder="value"
              aria-label={`Attribute value ${index + 1}`}
              className="flex-1"
            />
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-9 w-9 p-0 shrink-0"
              onClick={() => handleRemove(p.id)}
              aria-label={`Remove attribute ${index + 1}`}
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        )
      })}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={handleAdd}
        aria-label="Add attribute"
        className="flex items-center gap-1"
      >
        <Plus className="h-3.5 w-3.5" />
        Add Attribute
      </Button>
    </div>
  )
}
