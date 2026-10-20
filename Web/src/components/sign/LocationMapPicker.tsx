import { useCallback, useEffect, useRef, useState } from 'react';
import { motion } from 'framer-motion';
import { AlertTriangle, Crosshair, Loader2, MapPin, RefreshCw, X } from 'lucide-react';
import toast from 'react-hot-toast';
import { bd09ToGcj02, gcj02ToBd09 } from '../../utils/geolocation';
import {
  DEFAULT_MAP_CENTER_BD09,
  coordinateFallbackDescription,
  formatCoordinate,
  isCoordinate,
  locateWithBrowserSelection,
  presetToSelectedLocation,
  readLastMapSelection,
  saveLastMapSelection,
} from '../../utils/location';
import {
  getAMapErrorMessage,
  loadAMap,
  resetAMapLoad,
  reverseGeocode,
  type AMapMap,
  type AMapMarker,
  type AMapNamespace,
} from '../../utils/amap';
import type { CourseLocationPreset, SelectedLocation } from '../../types';

interface LocationMapPickerProps {
  current: SelectedLocation | null;
  presets: CourseLocationPreset[];
  onCancel: () => void;
  onConfirm: (location: SelectedLocation) => void;
}

type MapStatus = 'loading' | 'ready' | 'error';
type AddressStatus = 'idle' | 'pending' | 'ready' | 'error';

const HTML_ESCAPES: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

const escapeHtml = (value: string) => value.replace(/[&<>"']/g, (char) => HTML_ESCAPES[char] || char);

const defaultCenter = () => bd09ToGcj02(DEFAULT_MAP_CENTER_BD09.lng, DEFAULT_MAP_CENTER_BD09.lat);

const resolveInitial = (current: SelectedLocation | null, presets: CourseLocationPreset[]) => {
  if (current && isCoordinate(current.lng, current.lat)) {
    return current;
  }
  const last = readLastMapSelection();
  if (last) {
    return last;
  }
  if (presets.length > 0) {
    return presetToSelectedLocation(presets[0]);
  }
  const lng = formatCoordinate(DEFAULT_MAP_CENTER_BD09.lng);
  const lat = formatCoordinate(DEFAULT_MAP_CENTER_BD09.lat);
  return {
    lng,
    lat,
    description: coordinateFallbackDescription(lng, lat),
    source: 'map',
    name: '地图选点',
  } satisfies SelectedLocation;
};

const toMapLocation = (lng: number, lat: number): SelectedLocation => {
  const bd = gcj02ToBd09(lng, lat);
  const bdLng = formatCoordinate(bd.lng);
  const bdLat = formatCoordinate(bd.lat);
  return {
    lng: bdLng,
    lat: bdLat,
    description: coordinateFallbackDescription(bdLng, bdLat),
    source: 'map',
    name: '地图选点',
  };
};

export const LocationMapPicker = ({ current, presets, onCancel, onConfirm }: LocationMapPickerProps) => {
  const [initial] = useState(() => {
    const location = resolveInitial(current, presets);
    const lng = Number(location.lng);
    const lat = Number(location.lat);
    const gcj = isCoordinate(location.lng, location.lat)
      ? bd09ToGcj02(lng, lat)
      : defaultCenter();
    return { location, gcj };
  });
  const [selected, setSelected] = useState<SelectedLocation>(() => ({
    ...initial.location,
    lng: formatCoordinate(initial.location.lng),
    lat: formatCoordinate(initial.location.lat),
    description: initial.location.description || coordinateFallbackDescription(
      formatCoordinate(initial.location.lng),
      formatCoordinate(initial.location.lat),
    ),
    source: 'map',
    name: '地图选点',
  }));
  const [mapStatus, setMapStatus] = useState<MapStatus>('loading');
  const [mapError, setMapError] = useState('');
  const [addressStatus, setAddressStatus] = useState<AddressStatus>('idle');
  const [isLocating, setIsLocating] = useState(false);
  const [attempt, setAttempt] = useState(0);

  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<AMapMap | null>(null);
  const markerRef = useRef<AMapMarker | null>(null);
  const pinRef = useRef(initial.gcj);
  const geocodeSeqRef = useRef(0);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const requestAddress = useCallback((lng: number, lat: number) => {
    const seq = geocodeSeqRef.current + 1;
    geocodeSeqRef.current = seq;
    setAddressStatus('pending');
    reverseGeocode(lng, lat)
      .then((address) => {
        if (!mountedRef.current || seq !== geocodeSeqRef.current) return;
        setSelected((previous) => (previous ? { ...previous, description: address } : previous));
        setAddressStatus('ready');
      })
      .catch(() => {
        if (!mountedRef.current || seq !== geocodeSeqRef.current) return;
        setAddressStatus('error');
      });
  }, []);

  const applyPoint = useCallback((
    lng: number,
    lat: number,
    options?: { center?: boolean; geocode?: boolean },
  ) => {
    const next = toMapLocation(lng, lat);
    pinRef.current = { lng, lat };
    markerRef.current?.setPosition([lng, lat]);
    setSelected(next);
    if (options?.center) {
      mapRef.current?.setCenter([lng, lat]);
    }
    if (options?.geocode !== false) {
      requestAddress(lng, lat);
    }
  }, [requestAddress]);

  useEffect(() => {
    let disposed = false;
    let map: AMapMap | null = null;
    let tileTimer = 0;
    let initialGeocoded = false;

    setMapStatus('loading');
    setMapError('');

    loadAMap()
      .then((AMap: AMapNamespace) => {
        if (disposed || !containerRef.current) return;

        const start = pinRef.current;
        map = new AMap.Map(containerRef.current, {
          center: [start.lng, start.lat],
          zoom: 16,
          viewMode: '2D',
          resizeEnable: true,
          dragEnable: true,
          zoomEnable: true,
          doubleClickZoom: true,
          touchZoom: true,
        });
        mapRef.current = map;

        const marker = new AMap.Marker({
          position: [start.lng, start.lat],
          draggable: true,
          cursor: 'move',
          zIndex: 200,
        });
        markerRef.current = marker;
        marker.on('dragstart', () => {
          setAddressStatus('pending');
        });
        marker.on('dragging', () => {
          const position = marker.getPosition();
          const next = toMapLocation(position.getLng(), position.getLat());
          pinRef.current = { lng: position.getLng(), lat: position.getLat() };
          setSelected(next);
        });
        marker.on('dragend', () => {
          const position = marker.getPosition();
          applyPoint(position.getLng(), position.getLat());
        });

        const presetMarkers = presets.flatMap((preset) => {
          const presetLng = formatCoordinate(preset.lng);
          const presetLat = formatCoordinate(preset.lat);
          if (!isCoordinate(presetLng, presetLat)) return [];
          const gcj = bd09ToGcj02(Number(presetLng), Number(presetLat));
          const presetMarker = new AMap.Marker({
            position: [gcj.lng, gcj.lat],
            title: preset.name,
            zIndex: 100,
            label: preset.name
              ? { content: escapeHtml(preset.name), direction: 'top' }
              : undefined,
          });
          presetMarker.on('click', (event) => {
            event.originEvent?.stopPropagation();
            applyPoint(gcj.lng, gcj.lat, { center: true });
          });
          return [presetMarker];
        });

        map.add([marker, ...presetMarkers]);
        map.on('click', (event) => {
          if (!event.lnglat) return;
          applyPoint(event.lnglat.getLng(), event.lnglat.getLat());
        });
        map.on('complete', () => {
          window.clearTimeout(tileTimer);
          if (disposed) return;
          setMapStatus('ready');
          if (!initialGeocoded) {
            initialGeocoded = true;
            requestAddress(start.lng, start.lat);
          }
        });

        tileTimer = window.setTimeout(() => {
          if (!disposed) {
            setMapStatus('error');
            setMapError('地图瓦片加载超时，请检查网络后重试。');
          }
        }, 12000);
      })
      .catch((error: unknown) => {
        if (disposed) return;
        setMapStatus('error');
        setMapError(getAMapErrorMessage(error));
      });

    return () => {
      disposed = true;
      window.clearTimeout(tileTimer);
      markerRef.current = null;
      mapRef.current = null;
      map?.destroy();
    };
  }, [applyPoint, attempt, initial, presets, requestAddress]);

  const handleRetryMap = () => {
    resetAMapLoad();
    setAttempt((value) => value + 1);
  };

  const handleRetryAddress = () => {
    const pin = pinRef.current;
    requestAddress(pin.lng, pin.lat);
  };

  const handleLocate = async () => {
    if (isLocating) return;
    setIsLocating(true);
    try {
      const browser = await locateWithBrowserSelection();
      const gcj = bd09ToGcj02(Number(browser.lng), Number(browser.lat));
      applyPoint(gcj.lng, gcj.lat, { center: true });
      mapRef.current?.setZoom(17);
    } catch (error: unknown) {
      toast.error(error instanceof Error ? error.message : '获取当前位置失败');
    } finally {
      setIsLocating(false);
    }
  };

  const handleConfirm = () => {
    const fallback = coordinateFallbackDescription(selected.lng, selected.lat);
    const location: SelectedLocation = {
      ...selected,
      description: selected.description.trim() || fallback,
      source: 'map',
      name: '地图选点',
    };
    saveLastMapSelection(location);
    onConfirm(location);
  };

  const canConfirm = mapStatus === 'ready';

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-[160] flex items-end justify-center bg-slate-900/60 backdrop-blur-md p-0"
      onClick={onCancel}
      role="presentation"
    >
      <motion.div
        initial={{ y: '100%' }}
        animate={{ y: 0 }}
        exit={{ y: '100%' }}
        transition={{ type: 'spring', damping: 28, stiffness: 250 }}
        className="bg-white w-full max-w-[480px] rounded-t-[3rem] shadow-2xl flex flex-col max-h-[92dvh] pb-[calc(16px+var(--sab))]"
        onClick={(event) => event.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="地图选点"
      >
        <div className="w-12 h-1.5 bg-slate-200 rounded-full mx-auto mt-4 mb-3 shrink-0" />
        <div className="flex items-center justify-between px-6 pb-3 shrink-0">
          <h3 className="text-xl font-bold text-slate-900">地图选点</h3>
          <button
            type="button"
            onClick={onCancel}
            aria-label="关闭地图选点"
            className="w-8 h-8 flex items-center justify-center bg-slate-100 text-slate-400 rounded-full active:scale-95 transition-transform"
          >
            <X size={16} />
          </button>
        </div>

        <div className="relative mx-5 h-[40vh] min-h-[220px] rounded-2xl overflow-hidden bg-slate-100 shrink-0">
          <div ref={containerRef} className="absolute inset-0" style={{ touchAction: 'none' }} />

          {mapStatus === 'loading' && (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-slate-50 text-slate-500">
              <Loader2 size={24} className="animate-spin text-blue-600" />
              <p className="text-xs font-bold">地图加载中…</p>
            </div>
          )}

          {mapStatus === 'error' && (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 bg-slate-50 px-6 text-center">
              <AlertTriangle size={26} className="text-amber-500" />
              <p className="text-xs font-bold text-slate-600 leading-5">{mapError}</p>
              <button
                type="button"
                onClick={handleRetryMap}
                className="mt-1 inline-flex items-center gap-1.5 rounded-xl bg-blue-600 px-4 py-2 text-xs font-black text-white active:scale-95 transition-transform"
              >
                <RefreshCw size={14} />
                重试
              </button>
              <p className="text-[10px] font-medium text-slate-400">关闭弹层后仍可使用浏览器定位或预设地点签到</p>
            </div>
          )}

          {mapStatus === 'ready' && (
            <button
              type="button"
              onClick={handleLocate}
              disabled={isLocating}
              className="absolute right-3 top-3 z-10 flex items-center gap-1.5 rounded-full bg-white px-3 py-2 text-[11px] font-black text-slate-700 shadow-lg disabled:opacity-60 active:scale-95 transition-transform"
            >
              {isLocating ? <Loader2 size={14} className="animate-spin" /> : <Crosshair size={14} className="text-blue-600" />}
              {isLocating ? '定位中' : '我的位置'}
            </button>
          )}
        </div>

        <div className="px-6 pt-4 space-y-1.5">
          <div className="flex items-start gap-2">
            <MapPin size={15} className="text-blue-600 mt-0.5 shrink-0" />
            <p className="text-sm font-bold text-slate-800 leading-5 line-clamp-2">
              {addressStatus === 'pending' ? '正在获取地址…' : selected.description}
            </p>
          </div>
          <p className="pl-[23px] text-[11px] font-mono font-bold tracking-tight text-slate-400">
            BD-09 {selected.lng}, {selected.lat}
          </p>
          {addressStatus === 'error' && (
            <div className="pl-[23px] flex items-center gap-2">
              <p className="text-[10px] font-bold text-amber-600">地址解析失败，将使用坐标作为地址</p>
              <button
                type="button"
                onClick={handleRetryAddress}
                className="text-[10px] font-black text-blue-600 underline underline-offset-2"
              >
                重试
              </button>
            </div>
          )}
          <p className="pl-[23px] text-[10px] font-medium text-slate-400">请选择教师设置的签到范围内位置</p>
        </div>

        <div className="px-6 pt-4 flex gap-3 shrink-0">
          <button
            type="button"
            onClick={onCancel}
            className="flex-1 py-3.5 rounded-2xl bg-slate-100 text-sm font-black text-slate-500 active:scale-[0.98] transition-transform"
          >
            取消
          </button>
          <button
            type="button"
            onClick={handleConfirm}
            disabled={!canConfirm}
            className="flex-[1.4] py-3.5 rounded-2xl bg-blue-600 text-sm font-black text-white shadow-lg shadow-blue-200 disabled:opacity-50 active:scale-[0.98] transition-transform"
          >
            使用此位置
          </button>
        </div>
      </motion.div>
    </motion.div>
  );
};

export default LocationMapPicker;
