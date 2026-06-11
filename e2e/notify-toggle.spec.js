const { test, expect } = require('@playwright/test');

const MOCK_NOTIFICATION_GRANTED = `
  Object.defineProperty(Notification, 'permission', {
    get() { return window.__notifyPerm || 'granted'; },
    configurable: true,
  });
  Notification.requestPermission = async () => {
    window.__notifyPerm = 'granted';
    return 'granted';
  };
  navigator.serviceWorker.register = async () => ({
    showNotification: async () => {},
  });
`;

test.describe('Notification toggle button', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(MOCK_NOTIFICATION_GRANTED);
  });

  test('can enable and then disable notifications', async ({ page }) => {
    await page.goto('/');

    const btn = page.locator('#notify-btn');
    await expect(btn).toBeVisible();

    // With granted permission and no localStorage override, defaults to on.
    await expect(btn).toHaveText('[notify: on]');
    expect(await btn.getAttribute('class')).toContain('notify-granted');

    // Click to disable.
    await btn.click();
    await expect(btn).toHaveText('[notify: off]');
    expect(await btn.getAttribute('class')).toContain('notify-default');

    const stored = await page.evaluate(() => localStorage.getItem('notify-enabled'));
    expect(stored).toBe('false');

    // Click again to re-enable.
    await btn.click();
    await expect(btn).toHaveText('[notify: on]');
    expect(await btn.getAttribute('class')).toContain('notify-granted');

    const storedAgain = await page.evaluate(() => localStorage.getItem('notify-enabled'));
    expect(storedAgain).toBe('true');
  });

  test('shows toast messages on toggle', async ({ page }) => {
    await page.goto('/');
    const btn = page.locator('#notify-btn');
    const toast = page.locator('#toast');

    // Disable notifications.
    await btn.click();
    await expect(toast).toHaveText('Notifications disabled');
    await expect(toast).not.toHaveClass(/hidden/);

    // Re-enable notifications.
    await btn.click();
    await expect(toast).toHaveText('Notifications enabled');
    await expect(toast).not.toHaveClass(/hidden/);
  });

  test('persists disabled state across page reload', async ({ page }) => {
    await page.goto('/');
    const btn = page.locator('#notify-btn');

    // Start enabled, disable.
    await expect(btn).toHaveText('[notify: on]');
    await btn.click();
    await expect(btn).toHaveText('[notify: off]');

    // Reload and verify it stays off.
    await page.reload();
    await expect(btn).toHaveText('[notify: off]');
    expect(await btn.getAttribute('class')).toContain('notify-default');
  });

  test('button title reflects current state', async ({ page }) => {
    await page.goto('/');
    const btn = page.locator('#notify-btn');

    await expect(btn).toHaveAttribute('title', /click to disable/i);

    await btn.click();
    await expect(btn).toHaveAttribute('title', /click to enable/i);
  });
});
