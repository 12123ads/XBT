import { describe, expect, it } from 'vitest';
import {
  DEFAULT_TIME_ZONE,
  formatDateTime,
  formatDateTimeRange,
  formatTimeZoneHint,
  resolveTimeZone,
} from './datetime';

// 2026-10-22T22:03:12+08:00
const NEXT_RUN = Date.parse('2026-10-22T14:03:12Z');
const WINDOW_END = Date.parse('2026-10-22T14:04:59Z');

describe('formatDateTime', () => {
  it('renders the same instant in the requested time zone', () => {
    expect(formatDateTime(NEXT_RUN, 'Asia/Shanghai')).toBe('2026/10/22 22:03:12');
    expect(formatDateTime(NEXT_RUN, 'UTC')).toBe('2026/10/22 14:03:12');
  });

  it('supports date and time styles', () => {
    expect(formatDateTime(NEXT_RUN, 'Asia/Shanghai', 'date')).toBe('2026/10/22');
    expect(formatDateTime(NEXT_RUN, 'Asia/Shanghai', 'time')).toBe('22:03:12');
  });

  it('falls back to the configured default instead of the client zone', () => {
    expect(resolveTimeZone(undefined)).toBe(DEFAULT_TIME_ZONE);
    expect(resolveTimeZone('Not/AZone')).toBe(DEFAULT_TIME_ZONE);
    expect(formatDateTime(NEXT_RUN, 'Not/AZone')).toBe('2026/10/22 22:03:12');
    expect(formatDateTime(NEXT_RUN)).toBe('2026/10/22 22:03:12');
  });

  it('returns empty output for missing or invalid timestamps', () => {
    expect(formatDateTime(undefined, 'Asia/Shanghai')).toBe('');
    expect(formatDateTime(Number.NaN, 'Asia/Shanghai')).toBe('');
  });
});

describe('formatDateTimeRange', () => {
  it('omits dates when both ends are the same day in the configured zone', () => {
    expect(formatDateTimeRange(NEXT_RUN, WINDOW_END, 'Asia/Shanghai')).toBe('22:03:12 – 22:04:59');
  });

  it('judges the day boundary in the configured zone', () => {
    // 2026-10-22T15:59:59Z = 23:59:59 Shanghai, 2026-10-22T16:00:05Z = 00:00:05 next day.
    // In UTC both ends are still 2026-10-22.
    const start = Date.parse('2026-10-22T15:59:59Z');
    const end = Date.parse('2026-10-22T16:00:05Z');
    expect(formatDateTimeRange(start, end, 'Asia/Shanghai')).toBe(
      '2026/10/22 23:59:59 – 2026/10/23 00:00:05',
    );
    expect(formatDateTimeRange(start, end, 'UTC')).toBe('15:59:59 – 16:00:05');
  });

  it('falls back to a single timestamp without a usable end', () => {
    expect(formatDateTimeRange(NEXT_RUN, undefined, 'Asia/Shanghai')).toBe('2026/10/22 22:03:12');
    expect(formatDateTimeRange(NEXT_RUN, NEXT_RUN, 'Asia/Shanghai')).toBe('2026/10/22 22:03:12');
    expect(formatDateTimeRange(undefined, WINDOW_END, 'Asia/Shanghai')).toBe('暂无');
  });
});

describe('formatTimeZoneHint', () => {
  it('labels the configured zone', () => {
    expect(formatTimeZoneHint('Asia/Shanghai')).toBe('北京时间（Asia/Shanghai）');
    expect(formatTimeZoneHint(undefined)).toBe('北京时间（Asia/Shanghai）');
    expect(formatTimeZoneHint('UTC')).toBe('UTC');
  });
});
