import toast from 'react-hot-toast';
import { getBrowserLocation } from './geolocation';
import type { CourseLocationPreset, SelectedLocation } from '../types';

// BD-09, 日照校区。地图选点的最后兜底中心。
export const DEFAULT_MAP_CENTER_BD09 = { lng: 119.535984, lat: 35.47574 };

const LAST_MAP_SELECTION_KEY = 'xbt.location-map-last-selection.v1';

const EARTH_RADIUS_METERS = 6371008.8;

export interface SignLocationCenter {
  lat: number;
  lng: number;
}

export interface SignDistanceDescription {
  distanceM: number;
  outOfRange: boolean;
  label: string;
}

/**
 * Great-circle distance between two BD-09 points. Mirrors the server-side
 * haversine check so the picker and the server agree on "out of range".
 */
export const distanceMetersBetween = (
  aLat: string | number,
  aLng: string | number,
  bLat: string | number,
  bLng: string | number,
) => {
  const lat1 = Number(aLat) * Math.PI / 180;
  const lat2 = Number(bLat) * Math.PI / 180;
  const dLat = lat2 - lat1;
  const dLng = (Number(bLng) - Number(aLng)) * Math.PI / 180;
  const sinLat = Math.sin(dLat / 2);
  const sinLng = Math.sin(dLng / 2);
  const h = sinLat * sinLat + Math.cos(lat1) * Math.cos(lat2) * sinLng * sinLng;
  return 2 * EARTH_RADIUS_METERS * Math.asin(Math.sqrt(Math.min(1, h)));
};

export const describeSignDistance = (
  lat: string | number,
  lng: string | number,
  center: SignLocationCenter | null | undefined,
  radius: number | null | undefined,
): SignDistanceDescription | null => {
  if (!center || typeof radius !== 'number' || !Number.isFinite(radius) || radius <= 0) return null;
  const distance = distanceMetersBetween(lat, lng, center.lat, center.lng);
  if (!Number.isFinite(distance)) return null;
  const meters = Math.max(0, Math.round(distance));
  const outOfRange = distance > radius;
  return {
    distanceM: distance,
    outOfRange,
    label: outOfRange ? `超出范围，距签到点 ${meters} 米` : `距签到点 ${meters} 米`,
  };
};

export const signLocationCenterFromActivity = (
  activity: { if_open_address?: boolean; location_latitude?: number; location_longitude?: number; location_range?: number },
): { center: SignLocationCenter; radius: number } | null => {
  if (!activity.if_open_address) return null;
  const lat = activity.location_latitude;
  const lng = activity.location_longitude;
  const radius = activity.location_range;
  if (typeof lat !== 'number' || typeof lng !== 'number' || typeof radius !== 'number') return null;
  if (!Number.isFinite(lat) || !Number.isFinite(lng) || !Number.isFinite(radius) || radius <= 0) return null;
  if (lat === 0 && lng === 0) return null;
  return { center: { lat, lng }, radius };
};


export const formatCoordinate = (value: string | number) => {
  const parsed = typeof value === 'number'
    ? value
    : (value.trim() === '' ? Number.NaN : Number(value));
  return Number.isFinite(parsed) ? parsed.toFixed(6) : '';
};

export const isCoordinate = (lng: string, lat: string) => {
  const parsedLng = lng.trim() === '' ? Number.NaN : Number(lng);
  const parsedLat = lat.trim() === '' ? Number.NaN : Number(lat);
  return Number.isFinite(parsedLng)
    && Number.isFinite(parsedLat)
    && parsedLng >= -180
    && parsedLng <= 180
    && parsedLat >= -90
    && parsedLat <= 90;
};

export const coordinateFallbackDescription = (lng: string, lat: string) => `地图选点 ${lng},${lat}`;

export const presetToSelectedLocation = (preset: CourseLocationPreset): SelectedLocation => {
  const lng = formatCoordinate(preset.lng);
  const lat = formatCoordinate(preset.lat);
  return {
    lng,
    lat,
    description: preset.description || preset.name,
    source: 'preset',
    name: preset.name,
  };
};

export const isSameLocation = (left: SelectedLocation | null, right: SelectedLocation | null) => {
  if (!left || !right) return false;
  return left.source === right.source
    && left.lng === right.lng
    && left.lat === right.lat
    && left.name === right.name;
};

export const readLastMapSelection = (): SelectedLocation | null => {
  try {
    const raw = window.localStorage.getItem(LAST_MAP_SELECTION_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<Record<'lng' | 'lat' | 'description', unknown>>;
    const lng = formatCoordinate(typeof parsed.lng === 'string' ? parsed.lng : '');
    const lat = formatCoordinate(typeof parsed.lat === 'string' ? parsed.lat : '');
    if (!isCoordinate(lng, lat)) return null;
    return {
      lng,
      lat,
      description: typeof parsed.description === 'string' && parsed.description.trim()
        ? parsed.description
        : coordinateFallbackDescription(lng, lat),
      source: 'map',
      name: '地图选点',
    };
  } catch {
    return null;
  }
};

export const saveLastMapSelection = (location: SelectedLocation) => {
  try {
    window.localStorage.setItem(LAST_MAP_SELECTION_KEY, JSON.stringify({
      lng: location.lng,
      lat: location.lat,
      description: location.description,
    }));
  } catch {
    // Storage may be unavailable in private mode; the picker still works for this session.
  }
};

export const locateWithBrowserSelection = async (): Promise<SelectedLocation> => {
  const current = await getBrowserLocation();
  toast.success(`已获取当前位置，精度约 ${current.accuracy} 米`);
  return {
    lat: current.lat,
    lng: current.lng,
    description: current.description,
    source: 'browser',
    name: '浏览器当前位置',
  };
};
