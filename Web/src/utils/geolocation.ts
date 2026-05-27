export interface BrowserLocationResult {
  lat: string;
  lng: string;
  description: string;
  accuracy: number;
}

interface Coordinate {
  lng: number;
  lat: number;
}

const PI = Math.PI;
const X_PI = (PI * 3000.0) / 180.0;
const A = 6378245.0;
const EE = 0.00669342162296594323;

const isLocalhost = () => (
  window.location.hostname === 'localhost'
  || window.location.hostname === '127.0.0.1'
  || window.location.hostname === '[::1]'
);

const toFixedCoord = (value: number) => value.toFixed(6);

const outOfChina = (lng: number, lat: number) => (
  lng < 72.004 || lng > 137.8347 || lat < 0.8293 || lat > 55.8271
);

const transformLat = (lng: number, lat: number) => {
  let ret = -100.0 + 2.0 * lng + 3.0 * lat + 0.2 * lat * lat + 0.1 * lng * lat + 0.2 * Math.sqrt(Math.abs(lng));
  ret += ((20.0 * Math.sin(6.0 * lng * PI) + 20.0 * Math.sin(2.0 * lng * PI)) * 2.0) / 3.0;
  ret += ((20.0 * Math.sin(lat * PI) + 40.0 * Math.sin((lat / 3.0) * PI)) * 2.0) / 3.0;
  ret += ((160.0 * Math.sin((lat / 12.0) * PI) + 320 * Math.sin((lat * PI) / 30.0)) * 2.0) / 3.0;
  return ret;
};

const transformLng = (lng: number, lat: number) => {
  let ret = 300.0 + lng + 2.0 * lat + 0.1 * lng * lng + 0.1 * lng * lat + 0.1 * Math.sqrt(Math.abs(lng));
  ret += ((20.0 * Math.sin(6.0 * lng * PI) + 20.0 * Math.sin(2.0 * lng * PI)) * 2.0) / 3.0;
  ret += ((20.0 * Math.sin(lng * PI) + 40.0 * Math.sin((lng / 3.0) * PI)) * 2.0) / 3.0;
  ret += ((150.0 * Math.sin((lng / 12.0) * PI) + 300.0 * Math.sin((lng / 30.0) * PI)) * 2.0) / 3.0;
  return ret;
};

const wgs84ToGcj02 = (lng: number, lat: number): Coordinate => {
  if (outOfChina(lng, lat)) {
    return { lng, lat };
  }

  let dLat = transformLat(lng - 105.0, lat - 35.0);
  let dLng = transformLng(lng - 105.0, lat - 35.0);
  const radLat = (lat / 180.0) * PI;
  let magic = Math.sin(radLat);
  magic = 1 - EE * magic * magic;
  const sqrtMagic = Math.sqrt(magic);
  dLat = (dLat * 180.0) / (((A * (1 - EE)) / (magic * sqrtMagic)) * PI);
  dLng = (dLng * 180.0) / ((A / sqrtMagic) * Math.cos(radLat) * PI);

  return {
    lng: lng + dLng,
    lat: lat + dLat,
  };
};

const gcj02ToBd09 = (lng: number, lat: number): Coordinate => {
  const z = Math.sqrt(lng * lng + lat * lat) + 0.00002 * Math.sin(lat * X_PI);
  const theta = Math.atan2(lat, lng) + 0.000003 * Math.cos(lng * X_PI);

  return {
    lng: z * Math.cos(theta) + 0.0065,
    lat: z * Math.sin(theta) + 0.006,
  };
};

const wgs84ToBd09 = (lng: number, lat: number): Coordinate => {
  const gcj02 = wgs84ToGcj02(lng, lat);
  return gcj02ToBd09(gcj02.lng, gcj02.lat);
};

const getGeolocationErrorMessage = (error: GeolocationPositionError) => {
  switch (error.code) {
    case error.PERMISSION_DENIED:
      return '浏览器定位权限被拒绝，请在地址栏权限设置中允许定位';
    case error.POSITION_UNAVAILABLE:
      return '暂时无法获取当前位置，请稍后重试或选择预设地点';
    case error.TIMEOUT:
      return '获取当前位置超时，请稍后重试或选择预设地点';
    default:
      return '获取当前位置失败，请选择预设地点';
  }
};

export const getBrowserLocation = () => {
  return new Promise<BrowserLocationResult>((resolve, reject) => {
    if (!('geolocation' in navigator)) {
      reject(new Error('当前浏览器不支持定位，请选择预设地点'));
      return;
    }

    if (!window.isSecureContext && !isLocalhost()) {
      reject(new Error('浏览器定位需要 HTTPS 或 localhost 环境'));
      return;
    }

    navigator.geolocation.getCurrentPosition(
      (position) => {
        const { latitude, longitude, accuracy } = position.coords;
        const bd09 = wgs84ToBd09(longitude, latitude);
        const roundedAccuracy = Math.max(1, Math.round(accuracy || 0));
        resolve({
          lat: toFixedCoord(bd09.lat),
          lng: toFixedCoord(bd09.lng),
          accuracy: roundedAccuracy,
          description: "中国山东省日照市东港区秦楼街道",
        });
      },
      (error) => reject(new Error(getGeolocationErrorMessage(error))),
      {
        enableHighAccuracy: true,
        timeout: 12000,
        maximumAge: 30000,
      },
    );
  });
};
