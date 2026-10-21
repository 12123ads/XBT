export const DEFAULT_TIME_ZONE = 'Asia/Shanghai';

export type DateTimeStyle = 'dateTime' | 'date' | 'time';

// Fixed offsets for the zones this product displays. Normal rendering uses the
// server-provided `*_text` fields; these offsets only power the fallback for an
// older server. Intl is deliberately not used because some WebViews ignore its
// `timeZone` option and silently render UTC.
const TIME_ZONE_OFFSETS: Record<string, number> = {
  'Asia/Shanghai': 8 * 60 * 60,
  UTC: 0,
};

export const DEFAULT_TIME_ZONE_OFFSET_SECONDS = TIME_ZONE_OFFSETS[DEFAULT_TIME_ZONE];

type DateTimeParts = {
  year: string;
  month: string;
  day: string;
  hour: string;
  minute: string;
  second: string;
};

const DISPLAY_TEXT_PATTERN = /^(\d{4}\/\d{2}\/\d{2}) (\d{2}:\d{2}:\d{2})$/;

const pad2 = (value: number) => String(value).padStart(2, '0');

const resolveOffsetSeconds = (timeZone?: string | null) => {
  const zone = timeZone?.trim();
  if (!zone) return DEFAULT_TIME_ZONE_OFFSET_SECONDS;
  return TIME_ZONE_OFFSETS[zone] ?? DEFAULT_TIME_ZONE_OFFSET_SECONDS;
};

const getDateTimeParts = (ts: number, timeZone?: string | null): DateTimeParts | null => {
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return null;
  const shifted = new Date(ts + resolveOffsetSeconds(timeZone) * 1000);
  return {
    year: String(shifted.getUTCFullYear()),
    month: pad2(shifted.getUTCMonth() + 1),
    day: pad2(shifted.getUTCDate()),
    hour: pad2(shifted.getUTCHours()),
    minute: pad2(shifted.getUTCMinutes()),
    second: pad2(shifted.getUTCSeconds()),
  };
};

/**
 * Fallback formatter for servers that do not send `*_text` yet.
 * Pure arithmetic on the UTC getters: no Intl and no dependency on the device zone.
 */
export const formatDateTime = (
  ts: number | null | undefined,
  timeZone?: string | null,
  style: DateTimeStyle = 'dateTime',
) => {
  if (ts == null || !Number.isFinite(ts)) return '';
  const parts = getDateTimeParts(ts, timeZone);
  if (!parts) return '';
  const date = `${parts.year}/${parts.month}/${parts.day}`;
  const time = `${parts.hour}:${parts.minute}:${parts.second}`;
  if (style === 'date') return date;
  if (style === 'time') return time;
  return `${date} ${time}`;
};

/** Fallback range formatter; same-day is judged on the offset-adjusted dates. */
export const formatDateTimeRange = (
  start?: number | null,
  end?: number | null,
  timeZone?: string | null,
) => {
  if (!start || !Number.isFinite(start)) return '暂无';
  if (!end || !Number.isFinite(end) || end <= start) return formatDateTime(start, timeZone);
  const startParts = getDateTimeParts(start, timeZone);
  const endParts = getDateTimeParts(end, timeZone);
  if (!startParts || !endParts) return '暂无';
  const sameDay = startParts.year === endParts.year
    && startParts.month === endParts.month
    && startParts.day === endParts.day;
  if (sameDay) {
    return `${formatDateTime(start, timeZone, 'time')} – ${formatDateTime(end, timeZone, 'time')}`;
  }
  return `${formatDateTime(start, timeZone)} – ${formatDateTime(end, timeZone)}`;
};

/**
 * Render a timestamp for display: the server-provided text wins, otherwise fall
 * back to the fixed-offset formatter. Empty output means neither input is usable.
 */
export const displayDateTime = (
  text: string | null | undefined,
  ts: number | null | undefined,
  timeZone?: string | null,
) => {
  const value = text?.trim();
  if (value) return value;
  return formatDateTime(ts, timeZone);
};

const splitDisplayText = (value: string) => {
  const match = DISPLAY_TEXT_PATTERN.exec(value);
  return match ? { date: match[1], time: match[2] } : null;
};

/**
 * Render a scheduled window. Both server texts are used verbatim; a same-day
 * window keeps the compact `HH:mm:ss – HH:mm:ss` form.
 */
export const displayDateTimeRange = (
  startText: string | null | undefined,
  endText: string | null | undefined,
  start?: number | null,
  end?: number | null,
  timeZone?: string | null,
) => {
  const startValue = displayDateTime(startText, start, timeZone);
  if (!startValue) return '暂无';
  const endValue = displayDateTime(endText, end, timeZone);
  if (!endValue) return startValue;
  if (startValue === endValue) return startValue;
  if (!endText?.trim() && start != null && end != null && end <= start) return startValue;
  const startParts = splitDisplayText(startValue);
  const endParts = splitDisplayText(endValue);
  if (startParts && endParts && startParts.date === endParts.date) {
    return `${startParts.time} – ${endParts.time}`;
  }
  return `${startValue} – ${endValue}`;
};

/** Human-readable label so users can tell which time zone the page renders. */
export const formatTimeZoneHint = (timeZone?: string | null) => {
  const zone = timeZone?.trim() || DEFAULT_TIME_ZONE;
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
