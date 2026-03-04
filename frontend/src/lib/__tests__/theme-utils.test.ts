import { describe, it, expect, beforeEach, afterEach } from "vitest"
import { applyTenantTheme, resetTheme, updateFavicon } from "../theme-utils"

describe("theme-utils", () => {
  beforeEach(() => {
    // Clean slate for each test
    document.documentElement.removeAttribute("data-tenant-theme")
    document.documentElement.style.cssText = ""
    // Remove any favicon links added during tests
    document.querySelectorAll("link[rel~='icon']").forEach((el) => el.remove())
  })

  afterEach(() => {
    resetTheme()
  })

  describe("applyTenantTheme", () => {
    it("applies primaryColor as --primary CSS variable", () => {
      applyTenantTheme({ primaryColor: "#007bff" })
      expect(document.documentElement.style.getPropertyValue("--primary")).toBe("#007bff")
    })

    it("applies fontFamily as --font-family CSS variable", () => {
      applyTenantTheme({ primaryColor: "#000", fontFamily: "Inter" })
      expect(document.documentElement.style.getPropertyValue("--font-family")).toBe("Inter")
    })

    it("does not set --font-family when fontFamily is absent", () => {
      applyTenantTheme({ primaryColor: "#000" })
      expect(document.documentElement.style.getPropertyValue("--font-family")).toBe("")
    })

    it("records applied properties in data attribute", () => {
      applyTenantTheme({ primaryColor: "#ff0000", fontFamily: "Roboto" })
      const attr = document.documentElement.getAttribute("data-tenant-theme")
      expect(attr).toContain("--primary")
      expect(attr).toContain("--font-family")
    })

    it("sets favicon when faviconUrl is provided", () => {
      applyTenantTheme({ primaryColor: "#000", faviconUrl: "https://example.com/favicon.ico" })
      const link = document.querySelector<HTMLLinkElement>("link[rel~='icon']")
      expect(link?.href).toBe("https://example.com/favicon.ico")
    })

    it("does not create favicon element when faviconUrl is absent", () => {
      applyTenantTheme({ primaryColor: "#000" })
      const link = document.querySelector("link[rel~='icon']")
      expect(link).toBeNull()
    })

    it("overwrites a previous theme application", () => {
      applyTenantTheme({ primaryColor: "#ff0000" })
      applyTenantTheme({ primaryColor: "#00ff00" })
      expect(document.documentElement.style.getPropertyValue("--primary")).toBe("#00ff00")
    })
  })

  describe("resetTheme", () => {
    it("removes --primary CSS variable", () => {
      applyTenantTheme({ primaryColor: "#007bff" })
      resetTheme()
      expect(document.documentElement.style.getPropertyValue("--primary")).toBe("")
    })

    it("removes --font-family CSS variable", () => {
      applyTenantTheme({ primaryColor: "#000", fontFamily: "Inter" })
      resetTheme()
      expect(document.documentElement.style.getPropertyValue("--font-family")).toBe("")
    })

    it("removes the data-tenant-theme attribute", () => {
      applyTenantTheme({ primaryColor: "#007bff" })
      resetTheme()
      expect(document.documentElement.getAttribute("data-tenant-theme")).toBeNull()
    })

    it("is a no-op when no theme was applied", () => {
      expect(() => resetTheme()).not.toThrow()
      expect(document.documentElement.getAttribute("data-tenant-theme")).toBeNull()
    })

    it("restores the original favicon href", () => {
      // Set up a starting favicon
      const link = document.createElement("link")
      link.rel = "icon"
      link.href = "https://example.com/original.ico"
      document.head.appendChild(link)

      applyTenantTheme({ primaryColor: "#000", faviconUrl: "https://example.com/tenant.ico" })
      expect(link.href).toBe("https://example.com/tenant.ico")

      resetTheme()
      expect(link.href).toBe("https://example.com/original.ico")
    })
  })

  describe("updateFavicon", () => {
    it("creates a favicon link element if none exists", () => {
      updateFavicon("https://example.com/favicon.ico")
      const link = document.querySelector<HTMLLinkElement>("link[rel~='icon']")
      expect(link).not.toBeNull()
      expect(link?.href).toBe("https://example.com/favicon.ico")
    })

    it("updates an existing favicon link element", () => {
      const link = document.createElement("link")
      link.rel = "icon"
      link.href = "https://example.com/old.ico"
      document.head.appendChild(link)

      updateFavicon("https://example.com/new.ico")
      expect(link.href).toBe("https://example.com/new.ico")
    })

    it("records the original href in data-default-href", () => {
      const link = document.createElement("link")
      link.rel = "icon"
      link.href = "https://example.com/original.ico"
      document.head.appendChild(link)

      updateFavicon("https://example.com/new.ico")
      expect(link.dataset.defaultHref).toBe("https://example.com/original.ico")
    })

    it("does not overwrite data-default-href on subsequent calls", () => {
      const link = document.createElement("link")
      link.rel = "icon"
      link.href = "https://example.com/original.ico"
      document.head.appendChild(link)

      updateFavicon("https://example.com/first.ico")
      updateFavicon("https://example.com/second.ico")

      // defaultHref should still point to the original
      expect(link.dataset.defaultHref).toBe("https://example.com/original.ico")
      expect(link.href).toBe("https://example.com/second.ico")
    })
  })
})
