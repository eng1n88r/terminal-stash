import { expect, type Page } from '@playwright/test';

export const PASSWORD = 'test'; // must match APP_PASSWORD in start-server.js

export async function login(page: Page): Promise<void> {
  await page.goto('/login');
  await page.fill('#password', PASSWORD);
  await page.click('button[type=submit]');
  await page.waitForURL((url) => url.pathname === '/');
  await expect(page.locator('#feed')).toBeVisible();
}
