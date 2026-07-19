import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import { login, rm } from './helpers';

test('text snippet: send, render, copy, delete', async ({ page }) => {
  await login(page);
  const tag = `text-${Date.now()}`;

  await page.fill('#text', tag);
  await expect(page.locator('.composer')).toHaveClass(/has-content/);
  await page.click('#send-btn');
  const item = page.locator('.item', { hasText: tag });
  await expect(item).toBeVisible();
  await expect(page.locator('#text')).toHaveValue('');
  await expect(page.locator('.composer')).not.toHaveClass(/has-content/);

  await item.locator('.act-copy').click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(tag);

  await rm(item);
  await expect(item).toHaveCount(0);
});

test('second tab stays in sync over SSE', async ({ page, context }) => {
  await login(page);
  const page2 = await context.newPage();
  await page2.goto('/');
  await expect(page2.locator('#feed')).toBeVisible();

  const tag = `sse-${Date.now()}`;
  await page.fill('#text', tag);
  await page.click('#send-btn');
  const mirrored = page2.locator('.item', { hasText: tag });
  await expect(mirrored).toBeVisible();

  // the DELETE only reaches the server once the 4s undo window lapses
  await rm(page.locator('.item', { hasText: tag }));
  await expect(mirrored).toHaveCount(0, { timeout: 10_000 });
});

test('rm can be undone before it commits', async ({ page }) => {
  await login(page);
  const tag = `undo-${Date.now()}`;

  await page.fill('#text', tag);
  await page.click('#send-btn');
  const item = page.locator('.item', { hasText: tag });
  await expect(item).toBeVisible();

  // first click only arms the confirm — the item must survive it
  await item.locator('.act-del').click();
  await expect(item.locator('.act-del')).toHaveText('sure?');
  await expect(item).toBeVisible();

  await item.locator('.act-del').click();
  await expect(item).toHaveCount(0);
  await page.click('#flash .flash-act');
  await expect(item).toBeVisible();

  // the server never saw the DELETE, so the item survives a reload
  await page.reload();
  await expect(page.locator('.item', { hasText: tag })).toBeVisible();
});

test('file upload, download, delete', async ({ page }, testInfo) => {
  await login(page);
  const payload = `file payload ${Date.now()}`;
  const filePath = testInfo.outputPath('upload-me.txt');
  fs.writeFileSync(filePath, payload);

  await page.setInputFiles('#file-input', filePath);
  const item = page.locator('.item', { hasText: 'upload-me.txt' });
  await expect(item).toBeVisible();

  const href = await item.locator('a[download]').getAttribute('href');
  const resp = await page.request.get(href!);
  expect(resp.status()).toBe(200);
  expect(await resp.text()).toBe(payload);
  expect(resp.headers()['content-disposition']).toContain('attachment');
  expect(resp.headers()['x-content-type-options']).toBe('nosniff');

  await rm(item);
  await expect(item).toHaveCount(0);
});

test('traversal-shaped ids are rejected', async ({ page }) => {
  await login(page);
  expect((await page.request.get('/api/files/..%2f..%2fstash.db')).status()).toBe(404);
  expect((await page.request.delete('/api/items/..%2f..%2fstash.db')).status()).toBe(404);
});

test('theme picker applies and persists a palette across reload', async ({ page }) => {
  await login(page);
  await page.fill('#text', 'theme repaint check');
  await page.click('#send-btn');
  const snippet = page.locator('.item', { hasText: 'theme repaint check' }).locator('pre');
  await expect(snippet).toBeVisible();
  await page.click('#theme-btn');
  await page.click('.theme-option[data-theme="amber"]');
  const theme = () => page.evaluate(() => document.documentElement.getAttribute('data-theme'));
  expect(await theme()).toBe('amber');
  await expect(snippet).toHaveCSS('color', 'rgb(232, 232, 232)');
  expect(await snippet.evaluate((el) => el.style.color)).toBe('');
  await page.reload();
  expect(await theme()).toBe('amber');
});

test('no CSP violations or page errors during a session', async ({ page }) => {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(String(e)));

  await login(page);
  await page.fill('#text', `csp-check-${Date.now()}`);
  await page.click('#send-btn');
  await page.reload();
  await expect(page.locator('#feed')).toBeVisible();

  const csp = errors.filter((e) => /content security policy/i.test(e));
  expect(csp).toEqual([]);
  const unexpected = errors.filter((e) => !/Failed to load resource/.test(e));
  expect(unexpected).toEqual([]);
});
