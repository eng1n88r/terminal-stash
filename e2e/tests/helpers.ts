import { expect, type Locator, type Page } from '@playwright/test';

export const PASSWORD = 'test'; // must match APP_PASSWORD in start-server.js

// rm is a two-step confirm: first click arms the button, second click deletes.
export async function rm(item: Locator): Promise<void> {
  await item.locator('.act-del').click();
  await item.locator('.act-del').click();
}

export async function login(page: Page): Promise<void> {
  await page.goto('/login');
  await page.fill('#password', PASSWORD);
  await page.click('button[type=submit]');
  await page.waitForURL((url) => url.pathname === '/');
  await expect(page.locator('#feed')).toBeVisible();
}
