export const DEFAULT_TIME_ZONE = 'Asia/Shanghai';

export type DateTimeStyle = 'dateTime' | 'date' | 'time';

type DateTimeParts = {
  year: string;
  month: string;
  day: string;
  hour: string;
  minute: string;
  second: string;
};

const formatterCache = new Map<string, Intl.DateTimeFormat>();
const resolvedTimeZoneCache = new Map<string, string>();

const getFormatter = (timeZone: string) => {
  let formatter = formatterCache.get(timeZone);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat('zh-CN', {
      timeZone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    });
    formatterCache.set(timeZone, formatter);
  }
  return formatter;
};

/** Resolve the scheduling time zone, falling back to the configured default instead of the client zone. */
export const resolveTimeZone = (timeZone?: string | null) => {
  const value = timeZone?.trim();
  if (!value) return DEFAULT_TIME_ZONE;
  const cached = resolvedTimeZoneCache.get(value);
  if (cached) return cached;
  let resolved = value;
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: value });
  } catch {
    resolved = DEFAULT_TIME_ZONE;
  }
  resolvedTimeZoneCache.set(value, resolved);
  return resolved;
};

const getDateTimeParts = (ts: number, timeZone: string): DateTimeParts | null => {
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return null;
  const parts = getFormatter(timeZone).formatToParts(date);
  const pick = (type: Intl.DateTimeFormatPartTypes) => parts.find((part) => part.type === type)?.value ?? '';
  const year = pick('year');
  const month = pick('month');
  const day = pick('day');
  const hour = pick('hour');
  const minute = pick('minute');
  const second = pick('second');
  if (!year || !month || !day || !hour || !minute || !second) return null;
  return { year, month, day, hour, minute, second };
};

/**
 * Format an absolute timestamp in an explicit time zone.
 * Never relies on the browser/WebView time zone; empty output means the timestamp was missing or invalid.
 */
export const formatDateTime = (
  ts: number | null | undefined,
  timeZone?: string | null,
  style: DateTimeStyle = 'dateTime',
) => {
  if (ts == null || !Number.isFinite(ts)) return '';
  const zone = resolveTimeZone(timeZone);
  const parts = getDateTimeParts(ts, zone);
  if (!parts) return '';
  const date = `${parts.year}/${parts.month}/${parts.day}`;
  const time = `${parts.hour}:${parts.minute}:${parts.second}`;
  if (style === 'date') return date;
  if (style === 'time') return time;
  return `${date} ${time}`;
};

/** Format the scheduled execution window, judging same-day in the configured time zone. */
export const formatDateTimeRange = (
  start?: number | null,
  end?: number | null,
  timeZone?: string | null,
) => {
  if (!start || !Number.isFinite(start)) return '暂无';
  const zone = resolveTimeZone(timeZone);
  if (!end || !Number.isFinite(end) || end <= start) return formatDateTime(start, zone);
  const startParts = getDateTimeParts(start, zone);
  const endParts = getDateTimeParts(end, zone);
  if (!startParts || !endParts) return '暂无';
  const sameDay = startParts.year === endParts.year
    && startParts.month === endParts.month
    && startParts.day === endParts.day;
  if (sameDay) {
    return `${formatDateTime(start, zone, 'time')} – ${formatDateTime(end, zone, 'time')}`;
  }
  return `${formatDateTime(start, zone)} – ${formatDateTime(end, zone)}`;
};

/** Human-readable label so users can tell which time zone the page renders. */
export const formatTimeZoneHint = (timeZone?: string | null) => {
  const zone = resolveTimeZone(timeZone);
  if (zone === 'Asia/Shanghai') return '北京时间（Asia/Shanghai）';
  return zone;
};

export const getZeroOclockOfDay = (dateTime: Date) => {
  return new Date(dateTime.getFullYear(), dateTime.getMonth(), dateTime.getDate());
};

export const padLeft = (num: number, length = 2, fill = '0') => {
  return num.toString().padStart(length, fill);
};

export const formatFullDateTime = (date: Date | number) => {
  const d = typeof date === 'number' ? new Date(date) : date;
  if (isNaN(d.getTime())) return '无效时间';
  
  return `${d.getFullYear()}-${padLeft(d.getMonth() + 1)}-${padLeft(d.getDate())} ${padLeft(d.getHours())}:${padLeft(d.getMinutes())}:${padLeft(d.getSeconds())}`;
};

export const getChineseStringByDatetime = (date: Date | number, nowInput: Date | null = null) => {
  const dateTime = typeof date === 'number' ? new Date(date) : date;
  
  // 确保输入是有效的日期对象
  if (isNaN(dateTime.getTime())) {
    return '无效时间';
  }

  const now = nowInput ?? new Date();

  const isSameDay = dateTime.getFullYear() === now.getFullYear() &&
    dateTime.getMonth() === now.getMonth() &&
    dateTime.getDate() === now.getDate();

  if (isSameDay) {
    // Same day
    const min = Math.floor((now.getTime() - dateTime.getTime()) / (1000 * 60));

    if (min <= 1) return '刚刚';
    if (min < 60) return `${min}分钟前`;
    return `${now.getHours() - dateTime.getHours()}小时前`;
  }

  // Different day
  const dayDiff = Math.floor(
    (getZeroOclockOfDay(now).getTime() - getZeroOclockOfDay(dateTime).getTime()) / (1000 * 60 * 60 * 24)
  );

  if (dayDiff < 0) return '未来(请检查本机系统时间)';
  if (dayDiff === 1) return `昨天${padLeft(dateTime.getHours())}:${padLeft(dateTime.getMinutes())}`;
  if (dayDiff === 2) return `前天${padLeft(dateTime.getHours())}:${padLeft(dateTime.getMinutes())}`;
  if (dayDiff <= 7) return `${dayDiff}天前`;

  // Full date format
  return `${dateTime.getFullYear()}-${padLeft(dateTime.getMonth() + 1)}-${padLeft(dateTime.getDate())}`;
};
