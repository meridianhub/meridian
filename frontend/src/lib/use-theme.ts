import { useCallback, useEffect, useSyncExternalStore } from "react"

export type Theme = "system" | "light" | "dark"

const STORAGE_KEY = "meridian:theme"

function getStoredTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === "light" || stored === "dark" || stored === "system") {
      return stored
    }
  } catch {
    // localStorage unavailable (SSR, iframe sandbox, etc.)
  }
  return "system"
}

function getResolvedTheme(theme: Theme): "light" | "dark" {
  if (theme === "system") {
    return window.matchMedia("(prefers-color-scheme: dark)").matches
      ? "dark"
      : "light"
  }
  return theme
}

function applyTheme(theme: Theme): void {
  const resolved = getResolvedTheme(theme)
  document.documentElement.classList.toggle("dark", resolved === "dark")
}

// Simple pub/sub so useSyncExternalStore can react to changes
let listeners: Array<() => void> = []
let currentTheme: Theme = getStoredTheme()

function subscribe(listener: () => void): () => void {
  listeners = [...listeners, listener]
  return () => {
    listeners = listeners.filter((l) => l !== listener)
  }
}

function getSnapshot(): Theme {
  return currentTheme
}

function setTheme(theme: Theme): void {
  currentTheme = theme
  try {
    localStorage.setItem(STORAGE_KEY, theme)
  } catch {
    // localStorage unavailable
  }
  applyTheme(theme)
  for (const listener of listeners) {
    listener()
  }
}

/** Reset module state for test isolation. Not for production use. */
export function _resetForTesting(): void {
  currentTheme = getStoredTheme()
  listeners = []
}

export function useTheme() {
  const theme = useSyncExternalStore(subscribe, getSnapshot)

  // Sync from localStorage on mount (covers cases where localStorage was
  // set before this module loaded, e.g. by the inline script in index.html)
  useEffect(() => {
    const stored = getStoredTheme()
    if (stored !== currentTheme) {
      currentTheme = stored
      for (const l of listeners) l()
    }
    applyTheme(currentTheme)

    const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)")
    const handleChange = () => {
      if (currentTheme === "system") {
        applyTheme("system")
        for (const listener of listeners) {
          listener()
        }
      }
    }
    mediaQuery.addEventListener("change", handleChange)
    return () => mediaQuery.removeEventListener("change", handleChange)
  }, [])

  const cycleTheme = useCallback(() => {
    const next: Record<Theme, Theme> = {
      system: "light",
      light: "dark",
      dark: "system",
    }
    setTheme(next[currentTheme])
  }, [])

  return {
    theme,
    resolvedTheme: getResolvedTheme(theme),
    setTheme,
    cycleTheme,
  }
}
