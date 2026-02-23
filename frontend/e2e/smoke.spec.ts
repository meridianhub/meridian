import { test, expect } from '@playwright/test'

test.describe('Smoke Tests', () => {
  test('homepage loads', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveTitle(/Meridian/)
  })

  test('API health check accessible', async ({ request }) => {
    const response = await request.get('http://localhost:8090/healthz')
    expect(response.ok()).toBeTruthy()
  })
})
