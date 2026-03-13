import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Wand2, Sparkles, FileCode } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

// Generator service is not yet wired into the API clients.
// When it is, replace this constant with a check against the client.
const GENERATOR_AVAILABLE = false

export function EconomyCreatePage() {
  const navigate = useNavigate()
  const [prompt, setPrompt] = useState('')

  const promptEmpty = prompt.trim().length === 0
  const generatorDisabled = !GENERATOR_AVAILABLE || promptEmpty

  const generatorTooltip = !GENERATOR_AVAILABLE
    ? 'Coming soon'
    : promptEmpty
      ? 'Enter a description first'
      : undefined

  return (
    <div className="flex min-h-full items-start justify-center p-8">
      <div className="w-full max-w-2xl space-y-6">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold">Create Your Economy</h1>
          <p className="text-sm text-muted-foreground">
            Describe your business or start with a blank template
          </p>
        </div>

        <textarea
          aria-label="Business description"
          className="w-full rounded-lg border border-input bg-background px-3 py-2 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring resize-none"
          rows={5}
          placeholder="e.g. An energy trading platform that manages kWh contracts and carbon credits for renewable energy co-ops..."
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
        />

        <div className="grid grid-cols-3 gap-4">
          {/* Build My Economy */}
          <OptionCard
            icon={<Wand2 className="size-5" />}
            title="Build My Economy"
            description="Interactive refinement with AI guidance"
            disabled={generatorDisabled}
            tooltip={generatorTooltip}
            onClick={() => {
              // Future: call generator service and navigate with result
            }}
          />

          {/* I'm Feeling Lucky */}
          <OptionCard
            icon={<Sparkles className="size-5 text-warning" />}
            title="I'm Feeling Lucky"
            description="Single-pass AI generation"
            disabled={generatorDisabled}
            tooltip={generatorTooltip}
            onClick={() => {
              // Future: call generator service in single-pass mode
            }}
          />

          {/* Start from Scratch */}
          <OptionCard
            icon={<FileCode className="size-5 text-success" />}
            title="Start from Scratch"
            description="Open the editor with a blank template"
            disabled={false}
            onClick={() => navigate('/economy/edit')}
          />
        </div>
      </div>
    </div>
  )
}

interface OptionCardProps {
  icon: React.ReactNode
  title: string
  description: string
  disabled: boolean
  tooltip?: string
  onClick: () => void
}

function OptionCard({ icon, title, description, disabled, tooltip, onClick }: OptionCardProps) {
  const card = (
    <Card
      className={disabled ? 'opacity-50' : 'hover:border-primary transition-colors'}
    >
      <CardHeader>
        <div className="flex items-center gap-2">
          {icon}
          <CardTitle className="text-sm">{title}</CardTitle>
        </div>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        <Button
          variant="outline"
          size="sm"
          className="w-full"
          disabled={disabled}
          onClick={onClick}
        >
          {title}
        </Button>
      </CardContent>
    </Card>
  )

  if (tooltip) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          {/* span needed so tooltip works on disabled button parent */}
          <span className="block">{card}</span>
        </TooltipTrigger>
        <TooltipContent>{tooltip}</TooltipContent>
      </Tooltip>
    )
  }

  return card
}
