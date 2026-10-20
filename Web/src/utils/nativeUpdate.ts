export type NativeUpdateBridge = {
  isReady?: () => boolean;
  getVersion?: () => string;
  checkForUpdate?: () => void;
};

type WindowWithUpdateBridge = Window & { XBTUpdateBridge?: NativeUpdateBridge };

export type NativeAppVersion = {
  versionName: string;
  versionCode: number;
};

export const getNativeUpdateBridge = (): NativeUpdateBridge | null => {
  const bridge = (window as WindowWithUpdateBridge).XBTUpdateBridge;
  if (!bridge) return null;
  try {
    if (typeof bridge.isReady === 'function' && !bridge.isReady()) return null;
  } catch {
    return null;
  }
  return bridge;
};

export const readNativeAppVersion = (): NativeAppVersion | null => {
  const bridge = getNativeUpdateBridge();
  if (!bridge?.getVersion) return null;
  try {
    const parsed = JSON.parse(bridge.getVersion()) as Partial<NativeAppVersion>;
    if (typeof parsed.versionName !== 'string' || typeof parsed.versionCode !== 'number') {
      return null;
    }
    return { versionName: parsed.versionName, versionCode: parsed.versionCode };
  } catch {
    return null;
  }
};
