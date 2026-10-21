import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import {
  DEFAULT_TIME_ZONE,
  displayDateTime,
  displayDateTimeRange,
  formatDateTime,
  formatDateTimeRange,
  formatTimeZoneHint,
} from './datetime';

// 2026-10-22T22:03:12+08:00
const NEXT_RUN = Date.parse('2026-10-22T14:03:12Z');
const WINDOW_END = Date.parse('2026-10-22T14:04:59Z');

const NativeDateTimeFormat = Intl.DateTimeFormat;

// Display must never touch Intl: the affected WebView ignores `timeZone` and
// renders UTC. Any attempt to construct a formatter fails the test immediately.
beforeAll(() => {
  (Intl as unknown as { DateTimeFormat: unknown }).DateTimeFormat = function () {
    throw new Error('Intl.DateTimeFormat must not be used for display');
  };
});

afterAll(() => {
  (Intl as unknown as { DateTimeFormat: typeof Intl.DateTimeFormat }).DateTimeFormat =
    NativeDateTimeFormat;
});

describe('displayDateTime', () => {
  it('renders the server-provided text verbatim', () => {
    expect(displayDateTime('2026/10/22 22:03:12', 0)).toBe('2026/10/22 22:03:12');
    expect(displayDateTime(' 2026/10/22 22:03:12 ', undefined)).toBe('2026/10/22 22:03:12');
  });

  it('falls back to fixed-offset arithmetic for older servers', () => {
    expect(displayDateTime(undefined, NEXT_RUN, 'Asia/Shanghai')).toBe('2026/10/22 22:03:12');
    expect(displayDateTime('', NEXT_RUN, DEFAULT_TIME_ZONE)).toBe('2026/10/22 22:03:12');
  });

  it('returns empty output when neither input is usable', () => {
    expect(displayDateTime(undefined, undefined)).toBe('');
    expect(displayDateTime(null, Number.NaN)).toBe('');
  });
});

describe('formatDateTime fallback', () => {
  it('renders with a fixed +08:00 offset', () => {
    expect(formatDateTime(NEXT_RUN, 'Asia/Shanghai')).toBe('2026/10/22 22:03:12');
    expect(formatDateTime(NEXT_RUN, 'UTC')).toBe('2026/10/22 14:03:12');
    expect(formatDateTime(NEXT_RUN, 'Asia/Shanghai', 'date')).toBe('2026/10/22');
    expect(formatDateTime(NEXT_RUN, 'Asia/Shanghai', 'time')).toBe('22:03:12');
  });

  it('defaults unknown or missing zones to the configured +08:00 zone', () => {
    expect(formatDateTime(NEXT_RUN)).toBe('2026/10/22 22:03:12');
    expect(formatDateTime(NEXT_RUN, 'Not/AZone')).toBe('2026/10/22 22:03:12');
  });

  it('returns empty output for missing or invalid timestamps', () => {
    expect(formatDateTime(undefined, 'Asia/Shanghai')).toBe('');
    expect(formatDateTime(Number.NaN, 'Asia/Shanghai')).toBe('');
  });
});

describe('displayDateTimeRange', () => {
  it('keeps the compact form for a same-day server window', () => {
    expect(displayDateTimeRange(
      '2026/10/22 22:03:12',
      '2026/10/22 22:04:59',
      0,
      0,
    )).toBe('22:03:12 – 22:04:59');
  });

  it('keeps full dates when the server window crosses a day', () => {
    expect(displayDateTimeRange(
      '2026/10/21 23:59:00',
      '2026/10/22 00:01:00',
      0,
      0,
    )).toBe('2026/10/21 23:59:00 – 2026/10/22 00:01:00');
  });

  it('falls back to millis when the server sends no text', () => {
    expect(displayDateTimeRange(undefined, undefined, NEXT_RUN, WINDOW_END, 'Asia/Shanghai'))
      .toBe('22:03:12 – 22:04:59');
    const beforeMidnight = Date.parse('2026-10-21T15:59:00Z');
    const afterMidnight = Date.parse('2026-10-21T16:01:00Z');
    expect(displayDateTimeRange(undefined, undefined, beforeMidnight, afterMidnight, 'Asia/Shanghai'))
      .toBe('2026/10/21 23:59:00 – 2026/10/22 00:01:00');
  });

  it('falls back to a single timestamp without a usable end', () => {
    expect(displayDateTimeRange(undefined, undefined, NEXT_RUN, undefined, 'Asia/Shanghai'))
      .toBe('2026/10/22 22:03:12');
    expect(displayDateTimeRange('2026/10/22 22:03:12', undefined, 0, undefined))
      .toBe('2026/10/22 22:03:12');
    expect(displayDateTimeRange('2026/10/22 22:03:12', '2026/10/22 22:03:12', 0, 0))
      .toBe('2026/10/22 22:03:12');
    expect(displayDateTimeRange(undefined, undefined, undefined, WINDOW_END)).toBe('暂无');
  });
});

describe('formatDateTimeRange fallback', () => {
  it('judges the day boundary with the fixed offset', () => {
    const start = Date.parse('2026-10-22T15:59:59Z');
    const end = Date.parse('2026-10-22T16:00:05Z');
    expect(formatDateTimeRange(start, end, 'Asia/Shanghai')).toBe(
      '2026/10/22 23:59:59 – 2026/10/23 00:00:05',
    );
    expect(formatDateTimeRange(start, end, 'UTC')).toBe('15:59:59 – 16:00:05');
  });
});

describe('formatTimeZoneHint', () => {
  it('labels the configured zone', () => {
    expect(formatTimeZoneHint('Asia/Shanghai')).toBe('北京时间（Asia/Shanghai）');
    expect(formatTimeZoneHint(undefined)).toBe('北京时间（Asia/Shanghai）');
    expect(formatTimeZoneHint('UTC')).toBe('UTC');
  });
});
