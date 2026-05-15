import { useEffect, useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import { motion, AnimatePresence } from 'framer-motion';
import { Camera, CheckCircle2, Clock, Fingerprint, Loader2, MapPin, QrCode, RectangleEllipsis, Share2, XCircle } from 'lucide-react';
import { Html5Qrcode, type CameraDevice } from 'html5-qrcode';
import toast from 'react-hot-toast';
import publicClient from '../api/publicClient';
import type { ApiResponse, SignShareExecuteResponse, SignShareInfo } from '../types';
import { GestureInput } from '../components/sign/GestureInput';
import { PinInput } from '../components/sign/PinInput';
import { LocationInput } from '../components/sign/LocationInput';
import { NormalInput } from '../components/sign/NormalInput';
import { getBrowserLocation } from '../utils/geolocation';
import { parseChaoxingQrText } from '../utils/qr';
import config from '../../config.yaml';

const LOCATION_PRESETS = config.sign?.location_presets || [];
const QR_READER_ID = 'shared-sign-qr-reader';

const getErrorMessage = (error: unknown, fallback: string) => (
  error instanceof Error ? error.message : fallback
);

const signTypeName = (type: number) => {
  switch (type) {
    case 2: return '二维码签到';
    case 3: return '手势签到';
    case 4: return '位置签到';
    case 5: return '签到码签到';
    default: return '普通签到';
  }
};

const signIcon = (type: number, size = 22) => {
  switch (type) {
    case 2: return <QrCode size={size} />;
    case 3: return <Fingerprint size={size} />;
    case 4: return <MapPin size={size} />;
    case 5: return <RectangleEllipsis size={size} />;
    default: return <CheckCircle2 size={size} />;
  }
};

const formatTime = (ts: number) => {
  const d = new Date(ts);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
};

const SharedSign = () => {
  const { token } = useParams();
  const [share, setShare] = useState<SignShareInfo | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [isExecuting, setIsExecuting] = useState(false);
  const [result, setResult] = useState<SignShareExecuteResponse | null>(null);

  const [signCode, setSignCode] = useState('');
  const [lat, setLat] = useState('');
  const [lng, setLng] = useState('');
  const [locationStr, setLocationStr] = useState('');
  const [isLocationPickerOpen, setIsLocationPickerOpen] = useState(false);
  const [isLocating, setIsLocating] = useState(false);

  const [cameras, setCameras] = useState<CameraDevice[]>([]);
  const [selectedDeviceId, setSelectedDeviceId] = useState('');
  const [isScanning, setIsScanning] = useState(false);
  const [scannerError, setScannerError] = useState('');
  const scannerRef = useRef<Html5Qrcode | null>(null);
  const isExecutingRef = useRef(false);

  useEffect(() => {
    isExecutingRef.current = isExecuting;
  }, [isExecuting]);

  useEffect(() => {
    const loadShare = async () => {
      if (!token) {
        setLoadError('分享链接无效');
        setIsLoading(false);
        return;
      }
      try {
        const response = await publicClient.get<ApiResponse<SignShareInfo>>(`/sign/shares/${token}`);
        setShare(response.data.data);
      } catch (error: unknown) {
        setLoadError(getErrorMessage(error, '分享链接已失效'));
      } finally {
        setIsLoading(false);
      }
    };
    loadShare();
  }, [token]);

  useEffect(() => {
    if (share?.sign_type !== 2) return;
    let disposed = false;
    Html5Qrcode.getCameras()
      .then((devices) => {
        if (disposed) return;
        setCameras(devices || []);
        if (devices?.length) {
          const back = devices.find((device) => /back|rear|environment|后置|背面/.test((device.label || '').toLowerCase()));
          setSelectedDeviceId((back || devices[0]).id);
        }
      })
      .catch(() => setScannerError('无法读取摄像头，请检查浏览器权限'));
    return () => {
      disposed = true;
    };
  }, [share?.sign_type]);

  useEffect(() => {
    return () => {
      const scanner = scannerRef.current;
      if (!scanner) return;
      if (scanner.isScanning) {
        scanner.stop().catch(() => {});
      }
      scannerRef.current = null;
    };
  }, []);

  const applyLocation = (nextLat: string, nextLng: string, nextDescription: string) => {
    setLat(nextLat);
    setLng(nextLng);
    setLocationStr(nextDescription);
  };

  const executeShare = async (specialParams: Record<string, any>) => {
    if (!token || !share || isExecutingRef.current || result?.used) return;
    setIsExecuting(true);
    setResult(null);
    try {
      const response = await publicClient.post<ApiResponse<SignShareExecuteResponse>>(`/sign/shares/${token}/execute`, {
        special_params: specialParams,
      });
      const data = response.data.data;
      setResult(data);
      if (data.used) {
        toast.success('签到完成，分享链接已失效');
      } else if (data.failed_count > 0) {
        toast.error(data.message || '部分账号签到失败，可重试');
      }
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '签到失败'));
    } finally {
      setIsExecuting(false);
    }
  };

  const handleSubmit = () => {
    if (!share) return;
    if ((share.sign_type === 3 || share.sign_type === 5) && !signCode.trim()) {
      toast.error('请先填写签到码或手势');
      return;
    }
    if (share.sign_type === 4 && (!lat || !lng)) {
      toast.error('请先选择签到位置');
      return;
    }
    const specialParams: Record<string, any> = {};
    if (share.sign_type === 3 || share.sign_type === 5) {
      specialParams.sign_code = signCode.trim();
    } else if (share.sign_type === 4) {
      specialParams.latitude = lat;
      specialParams.longitude = lng;
      specialParams.description = locationStr;
    }
    executeShare(specialParams);
  };

  const handleUseBrowserLocation = async () => {
    if (isLocating) return;
    setIsLocating(true);
    try {
      const current = await getBrowserLocation();
      applyLocation(current.lat, current.lng, current.description);
      setIsLocationPickerOpen(false);
      toast.success(`已获取当前位置，精度约 ${current.accuracy} 米`);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取当前位置失败'));
    } finally {
      setIsLocating(false);
    }
  };

  const stopScanner = async () => {
    const scanner = scannerRef.current;
    if (!scanner) return;
    try {
      if (scanner.isScanning) await scanner.stop();
    } catch {
      // Camera streams are best-effort here; a failed stop should not block execution.
    }
    scannerRef.current = null;
    setIsScanning(false);
  };

  const handleStartScanner = async () => {
    if (!selectedDeviceId || isScanning || isExecuting || result?.used) return;
    setScannerError('');
    try {
      const scanner = new Html5Qrcode(QR_READER_ID);
      scannerRef.current = scanner;
      await scanner.start(
        selectedDeviceId,
        { fps: 12, qrbox: { width: 250, height: 250 }, aspectRatio: 1 },
        async (decodedText) => {
          if (isExecutingRef.current) return;
          const qr = parseChaoxingQrText(decodedText);
          if (!qr) return;
          await stopScanner();
          await executeShare({ enc: qr.enc, c: qr.c });
        },
        () => {},
      );
      setIsScanning(true);
    } catch (error: unknown) {
      setScannerError(getErrorMessage(error, '启动摄像头失败'));
      setIsScanning(false);
    }
  };

  const renderAction = () => {
    if (!share) return null;
    if (share.sign_type === 2) {
      return (
        <div className="space-y-4">
          <div id={QR_READER_ID} className="h-72 overflow-hidden rounded-[1.75rem] bg-slate-950" />
          {scannerError && <p className="text-xs font-bold text-red-500 text-center">{scannerError}</p>}
          {cameras.length > 1 && (
            <select
              value={selectedDeviceId}
              onChange={(event) => setSelectedDeviceId(event.target.value)}
              disabled={isScanning}
              className="w-full rounded-2xl bg-slate-50 px-4 py-3 text-sm font-bold text-slate-700 outline-none"
            >
              {cameras.map((camera, index) => (
                <option key={camera.id} value={camera.id}>{camera.label || `摄像头 ${index + 1}`}</option>
              ))}
            </select>
          )}
          <button
            type="button"
            onClick={isScanning ? stopScanner : handleStartScanner}
            disabled={!selectedDeviceId || isExecuting || result?.used}
            className="w-full py-3.5 rounded-xl bg-blue-600 text-white text-sm font-black shadow-lg shadow-blue-100 disabled:opacity-60 flex items-center justify-center gap-2"
          >
            {isExecuting ? <Loader2 size={18} className="animate-spin" /> : <Camera size={18} />}
            {isScanning ? '停止扫码' : '开始扫码签到'}
          </button>
        </div>
      );
    }
    return (
      <>
        <div className="p-5 pb-4">
          {share.sign_type === 3 && <GestureInput value={signCode} onChange={setSignCode} />}
          {share.sign_type === 5 && <PinInput value={signCode} onChange={setSignCode} />}
          {share.sign_type === 4 && (
            <LocationInput
              name={LOCATION_PRESETS.find((p: any) => p.lat === lat)?.name || (lat ? '浏览器当前位置' : '')}
              description={locationStr}
              onOpen={() => setIsLocationPickerOpen(true)}
              onLocate={handleUseBrowserLocation}
              isLocating={isLocating}
            />
          )}
          {share.sign_type === 0 && <NormalInput />}
        </div>
        <div className="px-5 py-4 bg-slate-50 border-t border-slate-100">
          <button
            type="button"
            onClick={handleSubmit}
            disabled={isExecuting || result?.used}
            className="w-full py-3.5 rounded-xl bg-blue-600 text-white text-sm font-black shadow-lg shadow-blue-100 disabled:opacity-60 flex items-center justify-center gap-2"
          >
            {isExecuting ? <Loader2 size={18} className="animate-spin" /> : <CheckCircle2 size={18} />}
            确认签到
          </button>
        </div>
      </>
    );
  };

  if (isLoading) {
    return (
      <div className="flex-1 flex items-center justify-center bg-slate-50">
        <Loader2 className="animate-spin text-blue-600" size={28} />
      </div>
    );
  }

  if (loadError || !share) {
    return (
      <div className="flex-1 flex items-center justify-center bg-slate-50 px-8">
        <div className="w-full rounded-[2rem] bg-white p-8 text-center shadow-xl shadow-slate-200">
          <XCircle className="mx-auto mb-4 text-red-500" size={42} />
          <h1 className="text-xl font-black text-slate-900 mb-2">链接不可用</h1>
          <p className="text-sm font-bold text-slate-500">{loadError || '分享链接已失效'}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex-1 min-h-0 flex flex-col bg-slate-50">
      <div className="bg-white sticky top-0 z-10 border-b border-slate-100 px-6 h-[calc(80px+var(--sat))] pt-[var(--sat)] flex items-center shrink-0 overflow-hidden">
        <div className="w-11 h-11 rounded-2xl bg-blue-50 text-blue-600 flex items-center justify-center shrink-0">
          {signIcon(share.sign_type)}
        </div>
        <div className="ml-3 min-w-0 relative z-10">
          <h2 className="font-black text-slate-900 truncate">{signTypeName(share.sign_type)}</h2>
          <p className="text-[10px] font-bold text-slate-400 truncate tracking-wide">免登录分享签到</p>
        </div>
        <Share2 className="absolute -right-7 -bottom-6 text-blue-600/10 rotate-12" size={120} />
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto touch-pan-y px-6 py-5 space-y-5 custom-scrollbar pb-[calc(40px+var(--sab))]">
        <div className="rounded-[2rem] bg-white p-5 shadow-xl shadow-blue-900/5 border border-slate-100">
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <h1 className="text-xl font-black text-slate-900 tracking-tight truncate">{share.activity_name}</h1>
              <p className="text-xs font-bold text-slate-500 mt-1 truncate">{share.course_name}</p>
              {share.course_teacher && <p className="text-[11px] font-bold text-slate-400 mt-0.5 truncate">{share.course_teacher}</p>}
            </div>
            <div className="shrink-0 px-2.5 py-1 rounded-lg bg-blue-50 text-blue-600 text-xs font-black">{signTypeName(share.sign_type).replace('签到', '')}</div>
          </div>
          <div className="mt-4 flex items-center gap-2 text-[11px] font-bold text-slate-400">
            <Clock size={13} />
            <span>{formatTime(share.expires_at)} 前有效</span>
          </div>
        </div>

        <div className="bg-white rounded-[2rem] shadow-xl shadow-blue-900/5 border border-slate-100 overflow-hidden">
          {renderAction()}
        </div>

        {result && (
          <div className={`rounded-[2rem] p-5 border shadow-xl ${result.used ? 'bg-green-50 border-green-100 shadow-green-100/40' : 'bg-amber-50 border-amber-100 shadow-amber-100/40'}`}>
            <div className="flex items-center gap-3 mb-3">
              {result.used ? <CheckCircle2 className="text-green-600" size={24} /> : <XCircle className="text-amber-600" size={24} />}
              <h3 className={`font-black ${result.used ? 'text-green-800' : 'text-amber-800'}`}>{result.message}</h3>
            </div>
            <div className="grid grid-cols-3 gap-2 text-center">
              <div className="rounded-2xl bg-white/70 p-3"><div className="text-lg font-black text-slate-900">{result.success_count}</div><div className="text-[10px] font-bold text-slate-500">成功</div></div>
              <div className="rounded-2xl bg-white/70 p-3"><div className="text-lg font-black text-slate-900">{result.already_signed_count}</div><div className="text-[10px] font-bold text-slate-500">已签</div></div>
              <div className="rounded-2xl bg-white/70 p-3"><div className="text-lg font-black text-slate-900">{result.failed_count}</div><div className="text-[10px] font-bold text-slate-500">失败</div></div>
            </div>
            {result.failures.length > 0 && (
              <div className="mt-3 space-y-1">
                {result.failures.map((failure) => <p key={failure} className="text-xs font-bold text-amber-700">{failure}</p>)}
              </div>
            )}
          </div>
        )}
      </div>

      <AnimatePresence>
        {isLocationPickerOpen && (
          <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/60 backdrop-blur-md p-0" onClick={() => setIsLocationPickerOpen(false)}>
            <motion.div initial={{ y: '100%' }} animate={{ y: 0 }} exit={{ y: '100%' }} transition={{ type: 'spring', damping: 28, stiffness: 250 }} className="bg-white w-full max-w-[480px] rounded-t-[3rem] p-8 shadow-2xl overflow-hidden flex flex-col max-h-[80vh]" onClick={(e) => e.stopPropagation()}>
              <div className="w-12 h-1.5 bg-slate-200 rounded-full mx-auto mb-8 shrink-0" />
              <div className="flex items-center justify-between mb-6 shrink-0"><h3 className="text-xl font-bold text-slate-900">选择签到位置</h3><button onClick={() => setIsLocationPickerOpen(false)} className="w-8 h-8 flex items-center justify-center bg-slate-100 text-slate-400 rounded-full">✕</button></div>
              <div className="flex-1 overflow-y-auto space-y-3 pr-1 pb-[calc(40px+var(--sab))] custom-scrollbar px-1">
                <motion.div whileTap={{ scale: 0.98 }} onClick={handleUseBrowserLocation} className={`p-5 rounded-[1.5rem] border-2 transition-all cursor-pointer flex items-center justify-between ${lat && !LOCATION_PRESETS.some((p: any) => p.lat === lat && p.lng === lng) ? 'border-blue-500 bg-blue-50/30' : 'border-slate-50 bg-slate-100/50 hover:bg-white'}`}>
                  <div className="flex-1 min-w-0 pr-4">
                    <div className="font-bold text-slate-800 mb-0.5 text-sm">使用浏览器当前位置</div>
                    <div className="text-[10px] text-slate-400 font-medium truncate">{isLocating ? '正在请求定位权限并转换为 BD-09' : '从系统定位服务读取并转换为 BD-09'}</div>
                  </div>
                  {isLocating ? <Loader2 size={20} className="text-blue-600 shrink-0 animate-spin" /> : <MapPin size={20} className="text-blue-600 shrink-0" />}
                </motion.div>
                {LOCATION_PRESETS.map((p: any, i: number) => {
                  const isSelected = p.lat === lat && p.lng === lng;
                  return (
                    <motion.div key={i} whileTap={{ scale: 0.98 }} onClick={() => { applyLocation(p.lat, p.lng, p.description); setIsLocationPickerOpen(false); }} className={`p-5 rounded-[1.5rem] border-2 transition-all cursor-pointer flex items-center justify-between ${isSelected ? 'border-blue-500 bg-blue-50/30' : 'border-slate-50 bg-slate-50/50 hover:bg-white'}`}>
                      <div className="flex-1 min-w-0 pr-4"><div className="font-bold text-slate-800 mb-0.5 text-sm">{p.name}</div><div className="text-[10px] text-slate-400 font-medium truncate">{p.description}</div></div>
                      {isSelected && <CheckCircle2 size={20} className="text-blue-600 shrink-0" />}
                    </motion.div>
                  );
                })}
              </div>
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>
      <style>{`
        #${QR_READER_ID}__dashboard, #${QR_READER_ID}__status_span, #${QR_READER_ID} img { display: none !important; }
        #${QR_READER_ID} video { object-fit: cover !important; width: 100% !important; height: 100% !important; }
        .custom-scrollbar::-webkit-scrollbar { width: 0px; }
      `}</style>
    </div>
  );
};

export default SharedSign;
