import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ChevronLeft, Camera, Loader2, ScanFace } from 'lucide-react';
import toast from 'react-hot-toast';
import client from '../api/client';
import type { ApiResponse } from '../types';

type NativeCameraBridge = {
  isReady?: () => boolean;
  getCameraState?: () => string;
  setScannerActive?: (active: boolean) => void;
  setLensFacing?: (mode: 'user' | 'environment') => void;
  syncPunchHole?: (left: number, top: number, width: number, height: number) => void;
  takePhoto?: () => void;
};

type WindowWithBridge = Window & { XBTCameraBridge?: NativeCameraBridge };

const getNativeBridge = (): NativeCameraBridge | null => {
  const bridge = (window as WindowWithBridge).XBTCameraBridge;
  if (!bridge) return null;
  try {
    if (typeof bridge.isReady === 'function' && !bridge.isReady()) return null;
  } catch { return null; }
  return bridge;
};

const FaceCapture = () => {
  const navigate = useNavigate();

  const videoRef = useRef<HTMLVideoElement | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const readerRef = useRef<HTMLDivElement | null>(null);
  const isNativeBridgeModeRef = useRef(false);
  const nativePreviewReadyRef = useRef(false);

  const [isNativeBridgeMode, setIsNativeBridgeMode] = useState(false);
  const [nativePreviewReady, setNativePreviewReady] = useState(false);
  const [isCameraReady, setIsCameraReady] = useState(false);
  const [showLoadingOverlay, setShowLoadingOverlay] = useState(true);
  const [isCapturing, setIsCapturing] = useState(false);
  const [isUploading, setIsUploading] = useState(false);
  const [facingFront, setFacingFront] = useState(true);

  useEffect(() => { nativePreviewReadyRef.current = nativePreviewReady; }, [nativePreviewReady]);
  useEffect(() => { isNativeBridgeModeRef.current = isNativeBridgeMode; }, [isNativeBridgeMode]);

  useEffect(() => {
    const bridge = getNativeBridge();
    isNativeBridgeModeRef.current = !!bridge;
    setIsNativeBridgeMode(!!bridge);

    if (bridge) {
      bridge.setScannerActive?.(true);
      bridge.setLensFacing?.('user');
    } else {
      const start = async () => {
        try {
          const stream = await navigator.mediaDevices.getUserMedia({
            video: { facingMode: { ideal: 'user' }, width: { ideal: 1280 }, height: { ideal: 720 } }
          });
          streamRef.current = stream;
          if (videoRef.current) {
            videoRef.current.srcObject = stream;
            videoRef.current.onplaying = () => setIsCameraReady(true);
            videoRef.current.onloadedmetadata = () => videoRef.current?.play().catch(() => {});
          }
        } catch {
          toast.error('无法访问摄像头，请检查权限设置');
          setIsCameraReady(true);
        }
      };
      start();
    }

    return () => {
      streamRef.current?.getTracks().forEach(t => t.stop());
      getNativeBridge()?.setScannerActive?.(false);
    };
  }, []);

  useEffect(() => {
    if (!isNativeBridgeMode) return;

    const root = document.getElementById('root');
    const prevHtml = document.documentElement.style.backgroundColor;
    const prevBody = document.body.style.backgroundColor;
    const prevRoot = root?.style.backgroundColor ?? '';
    document.documentElement.style.backgroundColor = 'transparent';
    document.body.style.backgroundColor = 'transparent';
    if (root) root.style.backgroundColor = 'transparent';

    const onCameraState = (e: Event) => {
      const ev = e as CustomEvent<{ active?: boolean; error?: string }>;
      const active = !!ev.detail?.active;
      setNativePreviewReady(active);
      if (active || ev.detail?.error) setIsCameraReady(true);
    };
    window.addEventListener('xbt-native-camera-state', onCameraState);

    return () => {
      document.documentElement.style.backgroundColor = prevHtml;
      document.body.style.backgroundColor = prevBody;
      if (root) root.style.backgroundColor = prevRoot;
      window.removeEventListener('xbt-native-camera-state', onCameraState);
    };
  }, [isNativeBridgeMode]);

  useEffect(() => {
    if (!isNativeBridgeMode || !isCameraReady) return;
    const sync = () => {
      const bridge = getNativeBridge();
      const el = readerRef.current;
      if (!bridge || !el || typeof bridge.syncPunchHole !== 'function') return;
      const r = el.getBoundingClientRect();
      bridge.syncPunchHole(r.left, r.top, r.width, r.height);
    };
    sync();
    const observer = new ResizeObserver(sync);
    if (readerRef.current) observer.observe(readerRef.current);
    window.addEventListener('resize', sync);
    return () => { observer.disconnect(); window.removeEventListener('resize', sync); };
  }, [isNativeBridgeMode, isCameraReady]);

  useEffect(() => {
    if (isCameraReady) {
      const t = setTimeout(() => setShowLoadingOverlay(false), 50);
      return () => clearTimeout(t);
    }
    setShowLoadingOverlay(true);
  }, [isCameraReady]);

  const toggleCamera = () => {
    const next = !facingFront;
    setFacingFront(next);
    const bridge = getNativeBridge();
    if (bridge) {
      bridge.setLensFacing?.(next ? 'user' : 'environment');
      return;
    }
    streamRef.current?.getTracks().forEach(t => t.stop());
    setIsCameraReady(false);
    navigator.mediaDevices.getUserMedia({
      video: { facingMode: next ? 'user' : 'environment', width: { ideal: 1280 }, height: { ideal: 720 } }
    }).then(stream => {
      streamRef.current = stream;
      if (videoRef.current) {
        videoRef.current.srcObject = stream;
        videoRef.current.onplaying = () => setIsCameraReady(true);
        videoRef.current.onloadedmetadata = () => videoRef.current?.play().catch(() => {});
      }
    }).catch(() => { toast.error('切换摄像头失败'); setIsCameraReady(true); });
  };

  const captureAndUpload = async () => {
    if (isCapturing || isUploading) return;
    setIsCapturing(true);

    try {
      let blob: Blob | null = null;
      const bridge = getNativeBridge();

      if (bridge && typeof bridge.takePhoto === 'function') {
        const result = (bridge.takePhoto as () => unknown)();
        if (result instanceof Promise) {
          const b64 = await result as string;
          if (typeof b64 === 'string' && b64.length > 100) {
            const raw = atob(b64.split(',')[1] || b64);
            const arr = new Uint8Array(raw.length);
            for (let i = 0; i < raw.length; i++) arr[i] = raw.charCodeAt(i);
            blob = new Blob([arr], { type: 'image/jpeg' });
          }
        }
        if (!blob) {
          blob = await new Promise<Blob | null>(resolve => {
            const timeout = setTimeout(() => { cleanup(); resolve(null); }, 6000);
            const onPhoto = (e: Event) => {
              const ev = e as CustomEvent<{ base64?: string }>;
              const b64 = ev.detail?.base64 ?? (e as any).base64;
              if (typeof b64 === 'string' && b64.length > 100) {
                clearTimeout(timeout); cleanup();
                const raw = atob(b64.split(',')[1] || b64);
                const arr = new Uint8Array(raw.length);
                for (let i = 0; i < raw.length; i++) arr[i] = raw.charCodeAt(i);
                resolve(new Blob([arr], { type: 'image/jpeg' }));
              }
            };
            (window as any).onXBTNativePhoto = onPhoto;
            const events = ['xbt-native-photo', 'xbt-native-camera-photo'];
            events.forEach(ev => window.addEventListener(ev, onPhoto));
            function cleanup() {
              delete (window as any).onXBTNativePhoto;
              events.forEach(ev => window.removeEventListener(ev, onPhoto));
            }
          });
        }
      }

      if (!blob && videoRef.current) {
        const v = videoRef.current;
        if (v.readyState >= 2) {
          const canvas = document.createElement('canvas');
          canvas.width = v.videoWidth;
          canvas.height = v.videoHeight;
          v.style.transform.includes('scaleX(-1)') || facingFront
            ? (() => {
                const ctx = canvas.getContext('2d')!;
                ctx.translate(canvas.width, 0);
                ctx.scale(-1, 1);
                ctx.drawImage(v, 0, 0);
              })()
            : canvas.getContext('2d')!.drawImage(v, 0, 0);
          blob = await new Promise<Blob | null>(r => canvas.toBlob(r, 'image/jpeg', 0.9));
        }
      }

      if (!blob) { toast.error('拍照失败，请重试'); return; }

      setIsCapturing(false);
      setIsUploading(true);

      const formData = new FormData();
      formData.append('file', new File([blob], `face-${Date.now()}.jpg`, { type: 'image/jpeg' }));
      await client.post<ApiResponse<{ has_face: boolean }>>('/user/face', formData);
      toast.success('人脸照片已保存');
      navigate(-1);
    } catch (err: any) {
      toast.error(err.message || '上传失败');
    } finally {
      setIsCapturing(false);
      setIsUploading(false);
    }
  };

  const busy = isCapturing || isUploading;

  return (
    <div
      className={`fixed inset-0 z-[100] flex flex-col overflow-hidden ${isNativeBridgeMode && nativePreviewReady ? 'bg-transparent' : 'bg-black'}`}
      data-native-bridge={isNativeBridgeMode ? '1' : '0'}
      data-native-ready={nativePreviewReady ? '1' : '0'}
    >
      <div id="face-reader" ref={readerRef} className="w-full h-full">
        {!isNativeBridgeMode && (
          <video
            ref={videoRef}
            className="w-full h-full object-cover"
            style={facingFront ? { transform: 'scaleX(-1)' } : undefined}
            autoPlay muted playsInline
          />
        )}
      </div>

      {showLoadingOverlay && (
        <div className="absolute inset-0 z-[110] bg-slate-900 flex flex-col items-center justify-center">
          <div className="relative">
            <div className="w-20 h-20 border-4 border-blue-500/20 border-t-blue-500 rounded-full animate-spin" />
            <ScanFace className="absolute inset-0 m-auto text-blue-500" size={32} />
          </div>
          <p className="mt-6 text-blue-400 font-bold text-sm animate-pulse">正在启动相机...</p>
        </div>
      )}

      {/* Top bar */}
      <div className="absolute top-0 left-0 right-0 z-20 px-4 pt-[calc(24px+var(--sat))] flex items-center justify-between text-white pointer-events-none">
        <button onClick={() => navigate(-1)} className="p-2 active:opacity-60 transition-opacity pointer-events-auto">
          <ChevronLeft size={32} />
        </button>
        <h2 className="text-[20px] font-normal tracking-widest">人脸拍照</h2>
        <div className="w-10" />
      </div>

      <div className="absolute inset-0 flex items-center justify-center pointer-events-none z-10">
        <div className="w-56 h-72 rounded-[50%] border-4 border-white/60 shadow-[0_0_0_9999px_rgba(0,0,0,0.45)]" />
      </div>

      <div className="absolute bottom-[calc(48px+var(--sab))] left-0 right-0 z-20 flex items-center justify-center gap-12">
        <button
          onClick={toggleCamera}
          disabled={busy}
          className="w-14 h-14 bg-black/40 rounded-full flex items-center justify-center backdrop-blur-md border border-white/10 text-white shadow-lg pointer-events-auto disabled:opacity-40"
        >
          <Camera size={22} />
        </button>

        <button
          onClick={captureAndUpload}
          disabled={busy || !isCameraReady}
          className="w-20 h-20 bg-white rounded-full flex items-center justify-center shadow-2xl active:scale-90 transition-transform disabled:opacity-50 pointer-events-auto"
        >
          <div className="w-16 h-16 border-4 border-slate-900 rounded-full flex items-center justify-center">
            {busy
              ? <Loader2 className="animate-spin text-slate-900" size={24} />
              : <div className="w-12 h-12 bg-slate-900 rounded-full" />
            }
          </div>
        </button>

        <div className="w-14 h-14" />
      </div>

      <p className="absolute bottom-[calc(16px+var(--sab))] left-0 right-0 text-center text-white/60 text-xs z-20 pointer-events-none">
        {isUploading ? '正在上传...' : '将人脸对准椭圆框后拍照'}
      </p>

      <style>{`
        #face-reader video { object-fit: cover !important; width: 100% !important; height: 100% !important; }
        #face-reader { background: black !important; }
        [data-native-bridge="1"][data-native-ready="1"] #face-reader { background: transparent !important; }
        [data-native-bridge="1"][data-native-ready="1"] #face-reader video { opacity: 0 !important; }
      `}</style>
    </div>
  );
};

export default FaceCapture;
