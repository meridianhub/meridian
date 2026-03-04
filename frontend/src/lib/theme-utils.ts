import type { TenantThemeConfig } from "@/lib/tenant-ui-config"

const TENANT_THEME_ATTR = "data-tenant-theme"

// Maps TenantThemeConfig properties to CSS custom property names used by shadcn/ui
const COLOR_PROPERTY_MAP: Record<string, string> = {
  primaryColor: "--primary",
  fontFamily: "--font-family",
}

/**
 * Applies tenant theme CSS variables to the document root element.
 * Marks overridden variables with a data attribute for cleanup.
 */
export function applyTenantTheme(theme: TenantThemeConfig): void {
  const root = document.documentElement

  // Clear any previously applied variables so stale properties don't linger
  // when switching between themes that have different sets of properties.
  const previouslyApplied = root.getAttribute(TENANT_THEME_ATTR)
  if (previouslyApplied) {
    for (const prop of previouslyApplied.split(",")) {
      if (prop) {
        root.style.removeProperty(prop)
      }
    }
  }

  const applied: string[] = []

  if (theme.primaryColor) {
    root.style.setProperty(COLOR_PROPERTY_MAP.primaryColor, theme.primaryColor)
    applied.push(COLOR_PROPERTY_MAP.primaryColor)
  }

  if (theme.fontFamily) {
    root.style.setProperty(COLOR_PROPERTY_MAP.fontFamily, theme.fontFamily)
    applied.push(COLOR_PROPERTY_MAP.fontFamily)
  }

  // Record which variables were applied so resetTheme knows what to remove
  root.setAttribute(TENANT_THEME_ATTR, applied.join(","))

  if (theme.faviconUrl) {
    updateFavicon(theme.faviconUrl)
  }
}

/**
 * Removes all CSS variable overrides applied by applyTenantTheme and
 * restores the default favicon.
 */
export function resetTheme(): void {
  const root = document.documentElement

  const applied = root.getAttribute(TENANT_THEME_ATTR)
  if (applied) {
    for (const prop of applied.split(",")) {
      if (prop) {
        root.style.removeProperty(prop)
      }
    }
    root.removeAttribute(TENANT_THEME_ATTR)
  }

  restoreDefaultFavicon()
}

/**
 * Updates the favicon link element to point to the provided URL.
 */
export function updateFavicon(url: string): void {
  let link = document.querySelector<HTMLLinkElement>("link[rel~='icon']")
  if (!link) {
    link = document.createElement("link")
    link.rel = "icon"
    document.head.appendChild(link)
  }

  if (!link.dataset.defaultHref) {
    link.dataset.defaultHref = link.href
  }

  link.href = url
}

function restoreDefaultFavicon(): void {
  const link = document.querySelector<HTMLLinkElement>("link[rel~='icon']")
  if (link && link.dataset.defaultHref !== undefined) {
    link.href = link.dataset.defaultHref
    delete link.dataset.defaultHref
  }
}
