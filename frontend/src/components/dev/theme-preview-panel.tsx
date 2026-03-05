import { useState, useCallback } from 'react'
import { Palette, X, RotateCcw, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

// CSS variables to expose for editing
const COLOR_VARS = [
  { label: 'Primary', variable: '--primary' },
  { label: 'Background', variable: '--background' },
  { label: 'Foreground', variable: '--foreground' },
  { label: 'Card', variable: '--card' },
  { label: 'Muted', variable: '--muted' },
  { label: 'Accent', variable: '--accent' },
  { label: 'Destructive', variable: '--destructive' },
  { label: 'Border', variable: '--border' },
] as const

const FONT_OPTIONS = [
  { label: 'System Default', value: '' },
  { label: 'Inter', value: 'Inter, sans-serif' },
  { label: 'Geist', value: 'Geist, sans-serif' },
  { label: 'Mono', value: 'ui-monospace, monospace' },
  { label: 'Serif', value: 'Georgia, serif' },
]

function getCSSVariable(variable: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(variable).trim()
}

function setCSSVariable(variable: string, value: string) {
  document.documentElement.style.setProperty(variable, value)
}

function removeCSSVariable(variable: string) {
  document.documentElement.style.removeProperty(variable)
}

/**
 * Collapsible dev-mode panel for previewing CSS variable theme overrides.
 * Only renders when import.meta.env.DEV is true.
 */
export function ThemePreviewPanel() {
  const [isOpen, setIsOpen] = useState(false)
  const [overrides, setOverrides] = useState<Record<string, string>>({})
  const [fontFamily, setFontFamily] = useState('')
  // Lazily capture initial CSS variable values once on mount
  const [initialValues] = useState<Record<string, string>>(() => {
    const initial: Record<string, string> = {}
    for (const { variable } of COLOR_VARS) {
      initial[variable] = getCSSVariable(variable)
    }
    return initial
  })

  const handleColorChange = useCallback((variable: string, value: string) => {
    setOverrides((prev) => ({ ...prev, [variable]: value }))
    setCSSVariable(variable, value)
  }, [])

  const handleFontChange = useCallback((value: string) => {
    setFontFamily(value)
    if (value) {
      setCSSVariable('--font-family', value)
      document.body.style.fontFamily = value
    } else {
      removeCSSVariable('--font-family')
      document.body.style.fontFamily = ''
    }
  }, [])

  const handleReset = useCallback(() => {
    for (const { variable } of COLOR_VARS) {
      removeCSSVariable(variable)
    }
    removeCSSVariable('--font-family')
    document.body.style.fontFamily = ''
    setOverrides({})
    setFontFamily('')
  }, [])

  const getCurrentValue = useCallback(
    (variable: string): string => {
      return overrides[variable] ?? initialValues[variable] ?? ''
    },
    [overrides, initialValues],
  )

  const hasOverrides = Object.keys(overrides).length > 0 || fontFamily !== ''

  return (
    <div
      className={cn(
        'fixed bottom-4 right-0 z-50 flex items-stretch transition-transform duration-300',
        isOpen ? 'translate-x-0' : 'translate-x-[calc(100%-2.5rem)]',
      )}
      data-testid="theme-preview-panel"
    >
      {/* Toggle button */}
      <button
        onClick={() => setIsOpen((v) => !v)}
        className={cn(
          'flex h-10 w-10 shrink-0 items-center justify-center self-center rounded-l-md border border-r-0 bg-background text-muted-foreground shadow-md hover:text-foreground',
          hasOverrides && 'text-primary',
        )}
        aria-label={isOpen ? 'Close theme panel' : 'Open theme panel'}
        aria-expanded={isOpen}
      >
        {isOpen ? (
          <ChevronRight className="size-4" />
        ) : (
          <Palette className="size-4" />
        )}
      </button>

      {/* Panel body */}
      <div
        className="w-72 rounded-l-md border bg-background shadow-xl"
        aria-hidden={!isOpen}
        inert={!isOpen ? true : undefined}
      >
        <div className="flex items-center justify-between border-b px-4 py-2">
          <span className="text-sm font-semibold">Theme Preview</span>
          <div className="flex items-center gap-1">
            {hasOverrides && (
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={handleReset}
                title="Reset to defaults"
                aria-label="Reset to defaults"
              >
                <RotateCcw className="size-3" />
              </Button>
            )}
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={() => setIsOpen(false)}
              aria-label="Close preview panel"
            >
              <X className="size-3" />
            </Button>
          </div>
        </div>

        <div className="max-h-[calc(100vh-8rem)] overflow-y-auto p-4 space-y-4">
          {/* Color variables */}
          <div className="space-y-2">
            <p className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
              Colors
            </p>
            {COLOR_VARS.map(({ label, variable }) => (
              <div key={variable} className="flex items-center gap-2">
                <label className="w-24 shrink-0 text-xs text-foreground">{label}</label>
                <div className="flex flex-1 items-center gap-1.5">
                  <input
                    type="color"
                    aria-label={`${label} color`}
                    className="h-6 w-6 shrink-0 cursor-pointer rounded border border-input"
                    value={toHexFallback(getCurrentValue(variable))}
                    onChange={(e) => handleColorChange(variable, e.target.value)}
                  />
                  <Input
                    className="h-6 px-2 text-xs font-mono"
                    value={getCurrentValue(variable)}
                    onChange={(e) => handleColorChange(variable, e.target.value)}
                    placeholder="oklch(...)"
                  />
                </div>
              </div>
            ))}
          </div>

          {/* Font family */}
          <div className="space-y-2">
            <p className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
              Font Family
            </p>
            <select
              className="w-full rounded-md border border-input bg-background px-2 py-1.5 text-xs focus:outline-none focus:ring-1 focus:ring-ring"
              value={fontFamily}
              onChange={(e) => handleFontChange(e.target.value)}
              aria-label="Font family"
            >
              {FONT_OPTIONS.map(({ label, value }) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
            {fontFamily && (
              <p className="text-xs text-muted-foreground" style={{ fontFamily }}>
                The quick brown fox jumps over the lazy dog.
              </p>
            )}
          </div>

          {hasOverrides && (
            <div className="rounded-md border border-dashed border-muted-foreground/40 bg-muted/30 p-2">
              <p className="text-xs text-muted-foreground">
                Overrides are temporary and reset on page reload.
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/**
 * Attempt to convert an oklch/hsl/named colour to a hex value for the
 * native colour picker. Falls back to #000000 when conversion is not possible
 * (e.g. oklch values which the picker can't natively render).
 */
function toHexFallback(value: string): string {
  if (!value) return '#000000'
  // Already a hex value
  if (/^#[0-9a-fA-F]{3,8}$/.test(value)) return value
  // For oklch/complex values, return a neutral fallback so the picker stays usable
  return '#000000'
}
