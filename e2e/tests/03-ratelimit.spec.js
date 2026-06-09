const { test, expect } = require('@playwright/test');
const { PASSWORD } = require('./helpers');

// Must run last: it locks the test client's IP out of /api/login for the
// remainder of the limiter window (the server restarts on the next run).
test('lockout after repeated failures surfaces in the UI', async ({ page }) => {
  await page.goto('/login');
  await page.evaluate(async () => {
    for (let i = 0; i < 10; i++) {
      await fetch('/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: 'wrong-' + i }),
      });
    }
  });

  // Even the correct password is rejected while the IP is locked.
  await page.fill('#password', PASSWORD);
  await page.click('button[type=submit]');
  await expect(page.locator('#error')).toContainText('too many attempts');
  expect(new URL(page.url()).pathname).toBe('/login');
});
