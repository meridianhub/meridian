import { type Page, expect } from '@playwright/test'
import { navigateTo } from '../fixtures'

/**
 * Navigate to the parties list page and click on the first row,
 * returning the party ID extracted from the resulting URL.
 *
 * Uses navigateTo() (client-side routing) instead of page.goto() to preserve
 * the memory-only dev auth token injected by injectDevAuth().
 */
export async function navigateToFirstPartyDetail(page: Page): Promise<string> {
  await navigateTo(page, '/parties')
  await expect(page.locator('table tbody tr').first()).toBeVisible()
  await page.locator('table tbody tr').first().click()
  await expect(page).toHaveURL(/\/parties\/([a-zA-Z0-9-]+)/)
  const url = page.url()
  return url.split('/parties/')[1]
}

/**
 * Click a tab by name and wait for network idle.
 */
export async function switchToTab(page: Page, tabName: string): Promise<void> {
  await page.getByRole('tab', { name: tabName }).click()
  await page.waitForLoadState('networkidle')
}
