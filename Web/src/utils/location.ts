import toast from 'react-hot-toast';
import { getBrowserLocation } from './geolocation';
import type { CourseLocationPreset, SelectedLocation } from '../types';

// BD-09, 日照校区。地图选点的最后兜底中心。
export const DEFAULT_MAP_CENTER_BD09 = { lng: 119.535984, lat: 35.47574 };

const LAST_MAP_SELECTION_KEY = 'xbt.location-map-last-selection.v1';

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
