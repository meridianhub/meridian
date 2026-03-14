import { describe, it, expect, beforeEach } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { TooltipProvider } from "@/components/ui/tooltip"
import { ThemeToggle } from "./theme-toggle"

function renderToggle() {
  return render(
    <TooltipProvider>
      <ThemeToggle />
    </TooltipProvider>,
  )
}

describe("ThemeToggle", () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove("dark")
  })

  it("renders with system theme by default", () => {
    renderToggle()
    expect(screen.getByRole("button", { name: /theme: system/i })).toBeInTheDocument()
  })

  it("cycles through themes: system -> light -> dark -> system", async () => {
    const user = userEvent.setup()
    renderToggle()

    const button = screen.getByRole("button", { name: /theme/i })

    // system -> light
    await user.click(button)
    expect(button).toHaveAccessibleName("Theme: Light")
    expect(localStorage.getItem("meridian:theme")).toBe("light")

    // light -> dark
    await user.click(button)
    expect(button).toHaveAccessibleName("Theme: Dark")
    expect(localStorage.getItem("meridian:theme")).toBe("dark")
    expect(document.documentElement.classList.contains("dark")).toBe(true)

    // dark -> system
    await user.click(button)
    expect(button).toHaveAccessibleName("Theme: System")
    expect(localStorage.getItem("meridian:theme")).toBe("system")
  })

  it("applies dark class when dark theme is selected", async () => {
    const user = userEvent.setup()
    renderToggle()

    const button = screen.getByRole("button", { name: /theme/i })

    // system -> light -> dark
    await user.click(button)
    await user.click(button)

    expect(document.documentElement.classList.contains("dark")).toBe(true)
  })

  it("removes dark class when light theme is selected", async () => {
    document.documentElement.classList.add("dark")
    const user = userEvent.setup()
    renderToggle()

    const button = screen.getByRole("button", { name: /theme/i })

    // system -> light
    await user.click(button)
    expect(document.documentElement.classList.contains("dark")).toBe(false)
  })

  it("restores theme from localStorage", () => {
    localStorage.setItem("meridian:theme", "dark")

    // Need to re-import the module to pick up localStorage.
    // Since the module is already loaded, we simulate by setting and rendering.
    // The useTheme hook reads localStorage on init.
    renderToggle()

    // The hook should have applied dark class
    expect(document.documentElement.classList.contains("dark")).toBe(true)
  })

  it("is keyboard accessible", async () => {
    const user = userEvent.setup()
    renderToggle()

    await user.tab()
    const button = screen.getByRole("button", { name: /theme/i })
    expect(button).toHaveFocus()

    await user.keyboard("{Enter}")
    expect(button).toHaveAccessibleName("Theme: Light")
  })
})
