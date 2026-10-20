export type AMapLoadErrorCode = 'missing-key' | 'load-failed' | 'timeout';

export class AMapLoadError extends Error {
  readonly code: AMapLoadErrorCode;

  constructor(code: AMapLoadErrorCode, message: string) {
    super(message);
    this.name = 'AMapLoadError';
    this.code = code;
  }
}

export interface AMapLngLat {
  lng: number;
  lat: number;
  getLng(): number;
  getLat(): number;
}

export interface AMapMapEvent {
  lnglat?: AMapLngLat;
  originEvent?: Event;
}

export interface AMapMarkerEvent {
  target?: AMapMarker;
  originEvent?: Event;
}

export interface AMapMarkerLabel {
  content: string;
  direction?: 'top' | 'bottom' | 'left' | 'right' | 'center';
}

export interface AMapMarkerOptions {
  position: [number, number];
  draggable?: boolean;
  cursor?: string;
  zIndex?: number;
  title?: string;
  label?: AMapMarkerLabel;
}

export interface AMapMarker {
  on(event: 'click' | 'dragstart' | 'dragend' | 'dragging', handler: (event: AMapMarkerEvent) => void): void;
  getPosition(): AMapLngLat;
  setPosition(position: [number, number]): void;
  setMap(map: AMapMap | null): void;
}

export interface AMapMapOptions {
  center: [number, number];
  zoom: number;
  viewMode?: '2D' | '3D';
  resizeEnable?: boolean;
  dragEnable?: boolean;
  zoomEnable?: boolean;
  doubleClickZoom?: boolean;
  touchZoom?: boolean;
}

export interface AMapMap {
  on(event: 'click', handler: (event: AMapMapEvent) => void): void;
  on(event: 'complete', handler: () => void): void;
  add(overlay: AMapMarker | AMapMarker[]): void;
  remove(overlay: AMapMarker): void;
  setCenter(position: [number, number]): void;
  setZoom(zoom: number): void;
  destroy(): void;
}

export interface AMapGeocodeResult {
  regeocode?: {
    formattedAddress?: string;
  };
}

export interface AMapGeocoder {
  getAddress(
    position: [number, number],
    callback: (status: string, result: AMapGeocodeResult | string) => void,
  ): void;
}

export interface AMapNamespace {
  Map: new (container: HTMLElement, options: AMapMapOptions) => AMapMap;
  Marker: new (options: AMapMarkerOptions) => AMapMarker;
  Geocoder: new (options?: { city?: string; radius?: number; extensions?: 'base' | 'all' }) => AMapGeocoder;
  plugin(plugins: string | string[], callback: () => void): void;
}

declare global {
  interface Window {
    AMap?: AMapNamespace;
    _AMapSecurityConfig?: { securityJsCode?: string };
  }
}

const SCRIPT_ID = 'xbt-amap-sdk';
const LOAD_TIMEOUT_MS = 15000;
const GEOCODE_TIMEOUT_MS = 8000;

let loaderPromise: Promise<AMapNamespace> | null = null;
let loaderKey = '';
let geocoderPromise: Promise<AMapGeocoder> | null = null;

const getAmapKey = () => (import.meta.env.VITE_AMAP_KEY || '').trim();
const getSecurityCode = () => (import.meta.env.VITE_AMAP_SECURITY_CODE || '').trim();

const removeScript = () => {
  document.getElementById(SCRIPT_ID)?.remove();
};

const createLoader = (key: string, securityCode: string) => new Promise<AMapNamespace>((resolve, reject) => {
  if (securityCode) {
    window._AMapSecurityConfig = { securityJsCode: securityCode };
  }

  removeScript();
  const script = document.createElement('script');
  script.id = SCRIPT_ID;
  script.async = true;
  script.src = `https://webapi.amap.com/maps?v=2.0&key=${encodeURIComponent(key)}`;

  const timeout = window.setTimeout(() => {
    script.onload = null;
    script.onerror = null;
    removeScript();
    reject(new AMapLoadError('timeout', '地图加载超时'));
  }, LOAD_TIMEOUT_MS);

  const settle = () => {
    window.clearTimeout(timeout);
    script.onload = null;
    script.onerror = null;
  };

  script.onload = () => {
    settle();
    if (window.AMap) {
      resolve(window.AMap);
      return;
    }
    removeScript();
    reject(new AMapLoadError('load-failed', '地图 SDK 未初始化'));
  };

  script.onerror = () => {
    settle();
    removeScript();
    reject(new AMapLoadError('load-failed', '地图 SDK 加载失败'));
  };

  document.head.appendChild(script);
});

export const loadAMap = () => {
  const key = getAmapKey();
  if (!key) {
    return Promise.reject(new AMapLoadError('missing-key', '未配置地图 key'));
  }
  if (window.AMap) {
    return Promise.resolve(window.AMap);
  }
  if (loaderPromise && loaderKey === key) {
    return loaderPromise;
  }

  loaderKey = key;
  const promise = createLoader(key, getSecurityCode());
  loaderPromise = promise;
  promise.catch(() => {
    if (loaderPromise === promise) {
      loaderPromise = null;
    }
  });
  return promise;
};

export const resetAMapLoad = () => {
  loaderPromise = null;
  loaderKey = '';
  geocoderPromise = null;
  removeScript();
};

const getGeocoder = (AMap: AMapNamespace) => {
  if (!geocoderPromise) {
    geocoderPromise = new Promise<AMapGeocoder>((resolve, reject) => {
      const timeout = window.setTimeout(() => {
        geocoderPromise = null;
        reject(new Error('逆地理编码组件加载超时'));
      }, LOAD_TIMEOUT_MS);

      try {
        AMap.plugin(['AMap.Geocoder'], () => {
          window.clearTimeout(timeout);
          try {
            resolve(new AMap.Geocoder({ city: '全国', extensions: 'base' }));
          } catch (error: unknown) {
            geocoderPromise = null;
            reject(error);
          }
        });
      } catch (error: unknown) {
        window.clearTimeout(timeout);
        geocoderPromise = null;
        reject(error);
      }
    });
  }
  return geocoderPromise;
};

export const reverseGeocode = async (lng: number, lat: number) => {
  const AMap = await loadAMap();
  const geocoder = await getGeocoder(AMap);

  return new Promise<string>((resolve, reject) => {
    let settled = false;
    const timeout = window.setTimeout(() => {
      if (settled) return;
      settled = true;
      reject(new Error('逆地理编码超时'));
    }, GEOCODE_TIMEOUT_MS);

    geocoder.getAddress([lng, lat], (status, result) => {
      if (settled) return;
      settled = true;
      window.clearTimeout(timeout);

      if (status === 'complete' && typeof result !== 'string') {
        const address = result.regeocode?.formattedAddress?.trim();
        if (address) {
          resolve(address);
          return;
        }
      }
      reject(new Error('逆地理编码失败'));
    });
  });
};

export const getAMapErrorMessage = (error: unknown) => {
  if (error instanceof AMapLoadError) {
    switch (error.code) {
      case 'missing-key':
        return '未配置地图 key（VITE_AMAP_KEY），地图选点暂不可用。仍可使用浏览器定位或预设地点。';
      case 'timeout':
        return '地图加载超时，可能是网络或瓦片服务不可用，请重试。';
      default:
        return '地图加载失败，请检查网络后重试。仍可使用浏览器定位或预设地点。';
    }
  }
  return error instanceof Error ? error.message : '地图加载失败，请重试。';
};
