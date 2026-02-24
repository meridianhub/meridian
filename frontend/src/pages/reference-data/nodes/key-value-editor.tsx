import * as React from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Plus, Trash2 } from 'lucide-react'

export interface KeyValueEditorProps {
  value: Record<string, string>
  onChange: (value: Record<string, string>) => void
}

interface KVPair {
  key: string
  val: string
  /** Stable identity for React key - set once on creation, never changes */
  uid: string
}

function pairsFromRecord(record: Record<string, string>): KVPair[] {
  return Object.entries(record).map(([key, val], i) => ({
    uid: `init-${i}-${key}`,
    key,
    val,
  }))
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

let globalUid = 0
function nextUid(): string {
  return `kve-${++globalUid}`
}

export function KeyValueEditor({ value, onChange }: KeyValueEditorProps) {
  const [pairs, setPairs] = React.useState<KVPair[]>(() => pairsFromRecord(value))

  // Sync pairs when external value prop reference changes (e.g., dialog reset).
  // We use the value reference as the trigger and do a functional update to
  // avoid listing `pairs` as a dependency (which would cause infinite loops).
  const prevValueRef = React.useRef(value)
  React.useEffect(() => {
    if (prevValueRef.current !== value) {
      prevValueRef.current = value
      setPairs((currentPairs) => {
        const currentJson = JSON.stringify(toRecord(currentPairs), Object.keys(toRecord(currentPairs)).sort())
        const newJson = JSON.stringify(value, Object.keys(value).sort())
        return currentJson === newJson ? currentPairs : pairsFromRecord(value)
      })
    }
  }, [value])

  function handleAdd() {
    const uid = nextUid()
    const updated = [...pairs, { uid, key: '', val: '' }]
    setPairs(updated)
    onChange(toRecord(updated))
  }

  function handleRemove(uid: string) {
    const updated = pairs.filter((p) => p.uid !== uid)
    setPairs(updated)
    onChange(toRecord(updated))
  }

  function handleKeyChange(uid: string, key: string) {
    const updated = pairs.map((p) => (p.uid === uid ? { ...p, key } : p))
    setPairs(updated)
    onChange(toRecord(updated))
  }

  function handleValChange(uid: string, val: string) {
    const updated = pairs.map((p) => (p.uid === uid ? { ...p, val } : p))
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
          <div key={p.uid} className="flex items-center gap-2">
            <div className="flex-1 space-y-0.5">
              <Input
                value={p.key}
                onChange={(e) => handleKeyChange(p.uid, e.target.value)}
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
              onChange={(e) => handleValChange(p.uid, e.target.value)}
              placeholder="value"
              aria-label={`Attribute value ${index + 1}`}
              className="flex-1"
            />
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-9 w-9 p-0 shrink-0"
              onClick={() => handleRemove(p.uid)}
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
