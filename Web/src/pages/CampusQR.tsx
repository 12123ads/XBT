import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { motion } from 'framer-motion';
import { ChevronLeft, Clipboard, Loader2, QrCode, RefreshCw, ShieldCheck, Timer } from 'lucide-react';
import toast from 'react-hot-toast';
import axios from 'axios';
import client, { withAccount, type RequestAccount } from '../api/client';
import { useAuthStore } from '../store/auth';
import PullToRefresh from '../components/PullToRefresh';
import type { ApiResponse, CampusQR as CampusQRData } from '../types';
import { createQRCodeDataURL } from '../utils/qrgen';


const parseExpireTime = (value: string) => {
  const match = value.match(/^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})$/);
  if (!match) return 0;
  const [, year, month, day, hour, minute, second] = match;
  return new Date(
    Number(year),
    Number(month) - 1,
    Number(day),
    Number(hour),
    Number(minute),
    Number(second)
  ).getTime();
};

const formatRemaining = (remainingMs: number) => {
  if (remainingMs <= 0) return '已过期';
  const totalSeconds = Math.ceil(remainingMs / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${seconds.toString().padStart(2, '0')}`;
};

const maskValue = (value: string) => {
  if (value.length <= 4) return value;
  return `${value.slice(0, 2)}${'*'.repeat(Math.min(6, Math.max(2, value.length - 4)))}${value.slice(-2)}`;
};

const CampusQR = () => {
  const navigate = useNavigate();
  const { activeUid, token } = useAuthStore();
  const owner = useMemo<RequestAccount>(() => ({ uid: activeUid ?? 0, token: token ?? '' }), [activeUid, token]);
  const [snapshot, setSnapshot] = useState<{ data: CampusQRData; image: string } | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [now, setNow] = useState(Date.now);
  const mountedRef = useRef(false);
  const controllerRef = useRef<AbortController | null>(null);
  const requestIdRef = useRef(0);
  const qr = snapshot?.data;
  const qrImage = snapshot?.image;

  const expireAt = useMemo(() => (qr?.expire_time ? parseExpireTime(qr.expire_time) : 0), [qr?.expire_time]);
  const remainingMs = expireAt > 0 ? expireAt - now : 0;
  const expired = expireAt > 0 && remainingMs <= 0;

  const ownsPage = useCallback(() => {
    const state = useAuthStore.getState();
    return mountedRef.current && owner.uid > 0 && !!owner.token &&
      state.activeUid === owner.uid && state.token === owner.token;
  }, [owner]);

  useLayoutEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      controllerRef.current?.abort();
      controllerRef.current = null;
      requestIdRef.current += 1;
    };
  }, [owner]);

  const loadQR = useCallback(async () => {
    if (!ownsPage()) {
      if (mountedRef.current) setIsLoading(false);
      return;
    }
    controllerRef.current?.abort();
    const controller = new AbortController();
    const requestId = ++requestIdRef.current;
    controllerRef.current = controller;
    const isCurrent = () => ownsPage() && !controller.signal.aborted &&
      requestIdRef.current === requestId && controllerRef.current === controller;
    try {
      const response = await client.get<ApiResponse<CampusQRData>>('/campus-qr', withAccount(owner, controller.signal));
      if (!isCurrent()) return;
      const data = response.data.data;
      if (!data?.qr_content) {
        throw new Error('校园码内容为空');
      }
      const image = createQRCodeDataURL(data.qr_content);
      if (!isCurrent()) return;
      setSnapshot({ data, image });
      setNow(Date.now());
      setLoadError('');
    } catch (error: unknown) {
      if (!isCurrent() || axios.isCancel(error)) return;
      const message = error instanceof Error ? error.message : '获取校园码失败';
      const response = axios.isAxiosError<ApiResponse<unknown>>(error) ? error.response : undefined;
      console.error('[CampusQR] fetch failed', { message, status: response?.status, code: response?.data?.code });
      setLoadError(message);
      toast.error(message);
    } finally {
      if (isCurrent()) {
        controllerRef.current = null;
        setIsLoading(false);
      }
    }
  }, [owner, ownsPage]);

  const fetchQR = useCallback(async () => {
    if (!ownsPage()) return;
    setIsLoading(true);
    await loadQR();
  }, [loadQR, ownsPage]);

  useEffect(() => {
    void loadQR();
  }, [loadQR]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (ownsPage()) setNow(Date.now());
    }, 1000);
    return () => window.clearInterval(timer);
  }, [ownsPage]);

  const copyText = async (value: string, label: string) => {
    if (!ownsPage()) return;
    try {
      await navigator.clipboard.writeText(value);
      if (ownsPage()) toast.success(`已复制${label}`);
    } catch {
      if (ownsPage()) toast.error('复制失败');
    }
  };

  return (
    <div className="h-full flex flex-col bg-slate-50 relative overflow-hidden">
      <div className="bg-white sticky top-0 z-30 border-b border-slate-100 px-4 h-[calc(80px+var(--sat))] pt-[var(--sat)] flex items-center justify-between shrink-0">
        <button
          onClick={() => navigate(-1)}
          className="p-2 -ml-2 text-slate-600 hover:bg-slate-50 rounded-lg transition-colors"
        >
          <ChevronLeft size={24} />
        </button>
        <h2 className="font-bold text-slate-900 text-lg">校园码</h2>
        <button
          onClick={() => void fetchQR()}
          disabled={isLoading}
          className="p-2 -mr-2 text-blue-600 hover:bg-blue-50 rounded-lg transition-colors disabled:opacity-50"
        >
          <RefreshCw size={20} className={isLoading ? 'animate-smooth-spin' : ''} />
        </button>
      </div>

      <PullToRefresh onRefresh={fetchQR} isRefreshing={isLoading} className="p-4">
        <div className="space-y-4 pb-[calc(80px+var(--sab))]">
          <div className="bg-white rounded-3xl border border-slate-100 shadow-sm p-5">
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0">
                <div className="flex items-center gap-2 text-xs font-bold text-blue-600">
                  <ShieldCheck size={15} />
                  二维码 PLUS
                </div>
                <h3 className="text-xl font-black text-slate-900 mt-2 truncate">
                  {qr?.full_name || '校园码'}
                </h3>
                <p className="text-xs text-slate-500 mt-1 truncate">
                  {qr?.effect_account ? maskValue(qr.effect_account) : (isLoading ? '正在读取' : '请重试获取校园码')}
                </p>
              </div>
              <div className={`px-3 py-2 rounded-xl text-xs font-black shrink-0 ${
                !qr ? 'bg-slate-100 text-slate-500' : expired ? 'bg-red-50 text-red-600' : 'bg-emerald-50 text-emerald-700'
              }`}>
                {qr ? (expired ? '已过期' : '可用') : (isLoading ? '读取中' : '获取失败')}
              </div>
            </div>

            <div className="mt-6 flex justify-center">
              <div className="w-[min(74vw,280px)] aspect-square rounded-3xl border border-slate-100 bg-white p-3 shadow-inner flex items-center justify-center">
                {isLoading && !qrImage ? (
                  <div className="text-slate-400 flex flex-col items-center gap-3">
                    <Loader2 size={34} className="animate-spin" />
                    <span className="text-sm font-bold">正在生成</span>
                  </div>
                ) : qrImage ? (
                  <img src={qrImage} alt="校园二维码" className="w-full h-full object-contain" />
                ) : (
                  <QrCode size={72} className="text-slate-200" />
                )}
              </div>
            </div>

            <div className="mt-5 grid grid-cols-2 gap-3">
              <div className="rounded-2xl bg-slate-50 px-3 py-3">
                <div className="text-[11px] font-bold text-slate-400 flex items-center gap-1">
                  <Timer size={13} />
                  剩余时间
                </div>
                <div className={`mt-1 text-lg font-black ${expired ? 'text-red-600' : 'text-slate-900'}`}>
                  {qr ? formatRemaining(remainingMs) : '--'}
                </div>
              </div>
              <div className="rounded-2xl bg-slate-50 px-3 py-3 min-w-0">
                <div className="text-[11px] font-bold text-slate-400">到期时间</div>
                <div className="mt-1 text-sm font-black text-slate-900 truncate">
                  {qr?.expire_time || '--'}
                </div>
              </div>
            </div>
          </div>

          <div className="bg-white rounded-3xl border border-slate-100 shadow-sm overflow-hidden">
            <button
              onClick={() => qr?.qr_content && copyText(qr.qr_content, '二维码内容')}
              disabled={!qr?.qr_content}
              className="w-full px-4 py-4 flex items-center gap-3 hover:bg-slate-50 disabled:opacity-50 transition-colors text-left"
            >
              <div className="w-10 h-10 rounded-xl bg-blue-50 text-blue-600 flex items-center justify-center shrink-0">
                <QrCode size={19} />
              </div>
              <div className="flex-1 min-w-0">
                <div className="text-xs font-bold text-slate-400">二维码内容</div>
                <div className="text-sm font-black text-slate-900 truncate mt-0.5">{qr?.qr_content || '--'}</div>
              </div>
              <Clipboard size={18} className="text-slate-300 shrink-0" />
            </button>
            <div className="h-px bg-slate-100 mx-4" />
            <button
              onClick={() => qr?.bar_content && copyText(qr.bar_content, '条码内容')}
              disabled={!qr?.bar_content}
              className="w-full px-4 py-4 flex items-center gap-3 hover:bg-slate-50 disabled:opacity-50 transition-colors text-left"
            >
              <div className="w-10 h-10 rounded-xl bg-slate-50 text-slate-600 flex items-center justify-center shrink-0">
                <Clipboard size={19} />
              </div>
              <div className="flex-1 min-w-0">
                <div className="text-xs font-bold text-slate-400">条码内容</div>
                <div className="text-sm font-black text-slate-900 truncate mt-0.5">{qr?.bar_content || '--'}</div>
              </div>
              <Clipboard size={18} className="text-slate-300 shrink-0" />
            </button>
          </div>

          {!isLoading && loadError && (
            <div role="alert" className="rounded-2xl border border-amber-100 bg-amber-50 px-4 py-3 text-sm text-amber-800">
              {snapshot ? `刷新失败，保留上次校园码：${loadError}` : loadError}
            </div>
          )}

          {(expired || (!snapshot && !isLoading)) && (
            <motion.button
              whileTap={{ scale: 0.96 }}
              onClick={() => void fetchQR()}
              disabled={isLoading}
              className="w-full py-4 rounded-2xl bg-blue-600 text-white text-sm font-black shadow-lg shadow-blue-100 flex items-center justify-center gap-2 disabled:opacity-60"
            >
              {isLoading ? <Loader2 size={18} className="animate-spin" /> : <RefreshCw size={18} />}
              {snapshot ? '刷新校园码' : '重试获取校园码'}
            </motion.button>
          )}
        </div>
      </PullToRefresh>
    </div>
  );
};

export default CampusQR;
