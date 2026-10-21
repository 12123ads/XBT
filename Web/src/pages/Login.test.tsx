// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Login from './Login';
import { APK_DOWNLOAD_URL } from '../utils/apkDownload';

const IPHONE_UA =
  'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1';
const IPAD_UA =
  'Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1';
const MAC_UA =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15';
const ANDROID_UA =
  'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36';
const DESKTOP_CHROME_UA =
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';

const setUserAgent = (userAgent: string) => {
  Object.defineProperty(window.navigator, 'userAgent', {
    configurable: true,
    get: () => userAgent,
  });
};

const renderLogin = () =>
  render(
    <MemoryRouter>
      <Login />
    </MemoryRouter>
  );

afterEach(() => {
  cleanup();
});

describe('Login APK download hint', () => {
  it.each([
    ['iPhone', IPHONE_UA],
    ['iPad', IPAD_UA],
    ['Macintosh', MAC_UA],
  ])('is absent on %s', (_label, userAgent) => {
    setUserAgent(userAgent);
    renderLogin();

    expect(screen.queryByText(/点此下载/)).toBeNull();
    expect(screen.queryByRole('link')).toBeNull();
  });

  it.each([
    ['Android', ANDROID_UA],
    ['desktop Chrome', DESKTOP_CHROME_UA],
  ])('links to the stable version-less APK on %s', (_label, userAgent) => {
    setUserAgent(userAgent);
    renderLogin();

    const link = screen.getByRole('link', { name: '点此下载' });
    expect(link.getAttribute('href')).toBe(APK_DOWNLOAD_URL);

    const hint = screen.getByText(/安卓 App 体验更好/);
    expect(hint.textContent).toBe('安卓 App 体验更好，点此下载');
    expect(hint.textContent).not.toMatch(/\d/);
  });
});
