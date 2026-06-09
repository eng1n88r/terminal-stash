const { expect } = require('@playwright/test');

const PASSWORD = 'test'; // must match APP_PASSWORD in start-server.js

async function login(page) {
  await page.goto('/login');
  await page.fill('#password', PASSWORD);
  await page.click('button[type=submit]');
  await page.waitForURL((url) => url.pathname === '/');
  await expect(page.locator('#feed')).toBeVisible();
}

module.exports = { login, PASSWORD };
