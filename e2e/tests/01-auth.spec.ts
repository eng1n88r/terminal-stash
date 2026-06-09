import { test, expect } from '@playwright/test';
import { login } from './helpers';

test('unauthenticated visit redirects to login with security headers', async ({ page }) => {
  const resp = await page.goto('/');
  expect(new URL(page.url()).pathname).toBe('/login');

  const h = resp!.headers();
  expect(h['content-security-policy']).toContain("default-src 'self'");
  expect(h['x-content-type-options']).toBe('nosniff');
  expect(h['x-frame-options']).toBe('DENY');
  expect(h['referrer-policy']).toBe('no-referrer');
});

test('wrong password shows access denied and stays on login', async ({ page }) => {
  await page.goto('/login');
  await page.fill('#password', 'definitely-wrong');
  await page.click('button[type=submit]');
  await expect(page.locator('#error')).toContainText('access denied');
  expect(new URL(page.url()).pathname).toBe('/login');
});

test('login and logout round trip', async ({ page }) => {
  await login(page);

  // Authenticated users get bounced from /login back to the app.
  await page.goto('/login');
  await page.waitForURL((url) => url.pathname === '/');

  await page.click('#logout-btn');
  await page.waitForURL((url) => url.pathname === '/login');
  const status = await page.evaluate(async () => (await fetch('/api/items')).status);
  expect(status).toBe(401);
});
