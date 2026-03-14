import { Sun, Moon, Monitor } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { useTheme, type Theme } from "@/lib/use-theme"

const LABELS: Record<Theme, string> = {
  system: "Theme: System",
  light: "Theme: Light",
  dark: "Theme: Dark",
}

export function ThemeToggle() {
  const { theme, resolvedTheme, cycleTheme } = useTheme()

  const Icon =
    theme === "system" ? Monitor : resolvedTheme === "dark" ? Moon : Sun

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={LABELS[theme]}
          onClick={cycleTheme}
        >
          <Icon className="size-5" />
        </Button>
      </TooltipTrigger>
      <TooltipContent>{LABELS[theme]}</TooltipContent>
    </Tooltip>
  )
}
