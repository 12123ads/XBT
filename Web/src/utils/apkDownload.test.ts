import { describe, expect, it } from 'vitest';
import { APK_DOWNLOAD_URL, isAppleUserAgent, shouldShowApkDownload } from './apkDownload';

const IPHONE_UA =
  'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1';
const IPAD_UA =
  'Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1';
const IPAD_DESKTOP_UA =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15';
const LEGACY_MAC_UA =
  'Mozilla/5.0 (Macintosh; U; Intel Mac OS X 10_6_8; en-us) AppleWebKit/534.50 (KHTML, like Gecko) Version/5.1 Safari/534.50';
const ANDROID_UA =
  'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36';
const WINDOWS_UA =
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';
const LINUX_UA =
  'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';

describe('isAppleUserAgent', () => {
  it.each([
    ['iPhone', IPHONE_UA],
    ['iPad', IPAD_UA],
    ['iPad desktop UA (Macintosh)', IPAD_DESKTOP_UA],
    ['legacy Mac OS X', LEGACY_MAC_UA],
  ])('treats %s as Apple', (_label, userAgent) => {
    expect(isAppleUserAgent(userAgent)).toBe(true);
  });

  it.each([
    ['Android', ANDROID_UA],
    ['Windows', WINDOWS_UA],
    ['Linux', LINUX_UA],
  ])('does not treat %s as Apple', (_label, userAgent) => {
    expect(isAppleUserAgent(userAgent)).toBe(false);
  });
});

describe('shouldShowApkDownload', () => {
  it('hides the hint on Apple and shows it everywhere else', () => {
    expect(shouldShowApkDownload(IPHONE_UA)).toBe(false);
    expect(shouldShowApkDownload(IPAD_DESKTOP_UA)).toBe(false);
    expect(shouldShowApkDownload(ANDROID_UA)).toBe(true);
    expect(shouldShowApkDownload(WINDOWS_UA)).toBe(true);
    expect(shouldShowApkDownload(LINUX_UA)).toBe(true);
  });
});

describe('APK_DOWNLOAD_URL', () => {
  it('points at the stable version-less GitHub Release asset through a GH proxy', () => {
    expect(APK_DOWNLOAD_URL).toBe(
      'https://ghfast.top/https://github.com/12123ads/XBT/releases/latest/download/xbt-latest.apk'
    );
  });
});
