import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { motion } from 'framer-motion';
import {
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  ClipboardPaste,
  Loader2,
  LocateFixed,
  MapPin,
  ShieldCheck,
} from 'lucide-react';
import toast from 'react-hot-toast';
import client from '../api/client';
import { getBrowserLocation, type BrowserLocationResult } from '../utils/geolocation';
import type { ApiResponse, QMXRoomCheckExecuteResponse, QMXRoomCheckPreview } from '../types';

const extractFirstURL = (text: string) => text.match(/https?:\/\/\S+/)?.[0] || '';

const requirementLabels: Record<string, string> = {
  photo: '拍照',
  face: '人脸识别',
  bluetooth: '蓝牙',
  special_sdk: '特殊定位 SDK',
};

const RoomCheck = () => {
  const navigate = useNavigate();
  const [credentialText, setCredentialText] = useState('');
  const [preview, setPreview] = useState<QMXRoomCheckPreview | null>(null);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [browserLocation, setBrowserLocation] = useState<BrowserLocationResult | null>(null);
  const [useBrowserLocation, setUseBrowserLocation] = useState(false);
  const [isPreviewing, setIsPreviewing] = useState(false);
  const [isLocating, setIsLocating] = useState(false);
  const [isExecuting, setIsExecuting] = useState(false);
  const [result, setResult] = useState<QMXRoomCheckExecuteResponse | null>(null);

  const credentialPayload = () => {
    const raw = credentialText.trim();
    return {
      raw,
      qmx_url: extractFirstURL(raw),
    };
  };

  const handlePreview = async () => {
    setIsPreviewing(true);
    setResult(null);
    try {
      const response = await client.post<ApiResponse<QMXRoomCheckPreview>>('/qmx/room-check/preview', credentialPayload());
      const data = response.data.data;
      setPreview(data);
      setSelectedIndex(0);
      setUseBrowserLocation(false);
      setBrowserLocation(null);
      if (data.unsupported?.length) {
        toast.error(`当前批次需要 ${data.unsupported.map(v => requirementLabels[v] || v).join('、')}，暂不支持自动提交`);
      } else {
        toast.success('已读取查寝批次');
      }
    } catch (error: any) {
      toast.error(error.message || '读取查寝信息失败');
    } finally {
      setIsPreviewing(false);
    }
  };

  const handleLocate = async () => {
    setIsLocating(true);
    try {
      const location = await getBrowserLocation();
      setBrowserLocation(location);
      setUseBrowserLocation(true);
      toast.success('已获取浏览器定位并转换为 BD-09');
    } catch (error: any) {
      toast.error(error.message || '获取定位失败');
    } finally {
      setIsLocating(false);
    }
  };

  const handleExecute = async () => {
    if (!preview) return;
    if (preview.unsupported?.length) {
      toast.error('当前批次包含暂不支持的要求，不能自动提交');
      return;
    }

    const selected = preview.locations[selectedIndex];
    if (!useBrowserLocation && !selected) {
      toast.error('请选择一个查寝定位点');
      return;
    }

    setIsExecuting(true);
    setResult(null);
    try {
      const payload: Record<string, any> = {
        ...credentialPayload(),
        location_index: useBrowserLocation ? -1 : selectedIndex,
      };
      if (useBrowserLocation && browserLocation) {
        payload.longitude = Number(browserLocation.lng);
        payload.latitude = Number(browserLocation.lat);
        payload.location_name = browserLocation.description;
      } else if (selected) {
        payload.location_name = selected.name;
      }

      const response = await client.post<ApiResponse<QMXRoomCheckExecuteResponse>>('/qmx/room-check/execute', payload);
      setResult(response.data.data);
      if (response.data.data.success) {
        toast.success(response.data.data.message || '查寝打卡成功');
      } else {
        toast.error(response.data.data.message || 'QMX 返回未成功');
      }
    } catch (error: any) {
      toast.error(error.message || '查寝打卡失败');
    } finally {
      setIsExecuting(false);
    }
  };

  const selectedLocation = preview?.locations[selectedIndex];

  return (
    <div className="flex-1 flex flex-col bg-[#f5f7fb] overflow-hidden">
      <div className="sticky top-0 z-10 bg-white/95 backdrop-blur border-b border-slate-100 px-4 h-[calc(72px+var(--sat))] pt-[var(--sat)] flex items-center gap-3">
        <motion.button
          whileTap={{ scale: 0.9 }}
          onClick={() => navigate(-1)}
          className="w-10 h-10 rounded-2xl bg-slate-100 text-slate-700 flex items-center justify-center"
        >
          <ArrowLeft size={20} />
        </motion.button>
        <div className="min-w-0">
          <h1 className="font-black text-slate-900">查寝定位打卡</h1>
          <p className="text-xs text-slate-500">QMX / 学工系统定位查寝</p>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-4 space-y-4 pb-[calc(24px+var(--sab))]">
        <div className="rounded-[28px] bg-slate-950 text-white p-5 shadow-xl shadow-slate-200">
          <div className="flex items-center gap-3 mb-4">
            <div className="w-11 h-11 rounded-2xl bg-cyan-400 text-slate-950 flex items-center justify-center">
              <ShieldCheck size={22} />
            </div>
            <div>
              <p className="font-black">临时凭据模式</p>
            <p className="text-xs text-slate-300">默认使用当前 xbt 账号；手动粘贴的 Token/Cookie 不保存</p>
            </div>
          </div>
          <textarea
            value={credentialText}
            onChange={(event) => setCredentialText(event.target.value)}
            placeholder="可留空直接读取当前账号；也可粘贴 QMX 链接、X-Token、Cookie，或整段抓包文本..."
            className="w-full min-h-32 rounded-2xl bg-white/10 border border-white/10 p-4 text-sm text-white placeholder:text-slate-400 outline-none focus:ring-2 focus:ring-cyan-300 resize-none"
          />
          <button
            onClick={handlePreview}
            disabled={isPreviewing}
            className="mt-4 w-full h-12 rounded-2xl bg-cyan-300 text-slate-950 font-black flex items-center justify-center gap-2 disabled:opacity-60"
          >
            {isPreviewing ? <Loader2 size={18} className="animate-spin" /> : <ClipboardPaste size={18} />}
            {credentialText.trim() ? '读取查寝信息' : '读取当前账号查寝'}
          </button>
        </div>

        {preview && (
          <div className="rounded-[28px] bg-white border border-slate-100 shadow-sm overflow-hidden">
            <div className="p-5 border-b border-slate-100">
              <div className="flex items-start justify-between gap-3">
                <div>
                  <p className="text-xs font-bold text-slate-400 uppercase tracking-widest">当前批次</p>
                  <h2 className="mt-1 text-xl font-black text-slate-900">{preview.batch_name || '定位打卡'}</h2>
                  <p className="mt-1 text-sm text-slate-500">
                    {preview.check_date} {preview.start_time}-{preview.end_time}
                    {preview.late_end_time ? `，晚归截止 ${preview.late_end_time}` : ''}
                  </p>
                </div>
                <div className={`px-3 py-1.5 rounded-full text-xs font-black ${preview.unsupported?.length ? 'bg-amber-100 text-amber-700' : 'bg-emerald-100 text-emerald-700'}`}>
                  {preview.unsupported?.length ? '有限支持' : '可提交'}
                </div>
              </div>
              {preview.unsupported?.length > 0 && (
                <div className="mt-4 rounded-2xl bg-amber-50 text-amber-800 p-3 text-xs font-semibold flex gap-2">
                  <AlertTriangle size={16} className="shrink-0 mt-0.5" />
                  <span>该批次要求 {preview.unsupported.map(v => requirementLabels[v] || v).join('、')}，当前只支持纯定位查寝。</span>
                </div>
              )}
            </div>

            <div className="p-5 space-y-3">
              <div className="flex items-center justify-between">
                <p className="font-black text-slate-900">定位点</p>
                <button
                  type="button"
                  onClick={handleLocate}
                  disabled={isLocating}
                  className="px-3 py-2 rounded-xl bg-blue-50 text-blue-600 text-xs font-black flex items-center gap-1.5 disabled:opacity-60"
                >
                  {isLocating ? <Loader2 size={14} className="animate-spin" /> : <LocateFixed size={14} />}
                  浏览器定位
                </button>
              </div>

              {browserLocation && (
                <button
                  type="button"
                  onClick={() => setUseBrowserLocation(true)}
                  className={`w-full text-left rounded-2xl border p-4 transition-colors ${useBrowserLocation ? 'border-blue-500 bg-blue-50' : 'border-slate-100 bg-slate-50'}`}
                >
                  <div className="flex items-center gap-3">
                    <MapPin size={18} className="text-blue-500 shrink-0" />
                    <div className="min-w-0">
                      <p className="font-black text-sm text-slate-900">浏览器当前位置</p>
                      <p className="text-xs text-slate-500 truncate">{browserLocation.lng}, {browserLocation.lat}</p>
                    </div>
                  </div>
                </button>
              )}

              <div className="space-y-2">
                {preview.locations.map((location, index) => (
                  <button
                    key={`${location.lng}-${location.lat}-${index}`}
                    type="button"
                    onClick={() => {
                      setSelectedIndex(index);
                      setUseBrowserLocation(false);
                    }}
                    className={`w-full text-left rounded-2xl border p-4 transition-colors ${!useBrowserLocation && selectedIndex === index ? 'border-cyan-500 bg-cyan-50' : 'border-slate-100 bg-slate-50'}`}
                  >
                    <div className="flex items-start gap-3">
                      <MapPin size={18} className="text-cyan-600 shrink-0 mt-0.5" />
                      <div className="min-w-0 flex-1">
                        <p className="font-black text-sm text-slate-900 truncate">{location.name || `定位点 ${index + 1}`}</p>
                        <p className="text-xs text-slate-500 mt-0.5">
                          {location.lng.toFixed(6)}, {location.lat.toFixed(6)} · 范围 {location.range}m
                        </p>
                      </div>
                    </div>
                  </button>
                ))}
              </div>
            </div>
          </div>
        )}

        {preview && (
          <button
            onClick={handleExecute}
            disabled={isExecuting || preview.unsupported?.length > 0}
            className="w-full h-14 rounded-3xl bg-slate-950 text-white font-black flex items-center justify-center gap-2 shadow-xl shadow-slate-200 disabled:opacity-50"
          >
            {isExecuting ? <Loader2 size={19} className="animate-spin" /> : <CheckCircle2 size={19} />}
            提交查寝打卡
          </button>
        )}

        {result && (
          <div className={`rounded-[28px] border p-5 ${result.success ? 'bg-emerald-50 border-emerald-100' : 'bg-rose-50 border-rose-100'}`}>
            <div className="flex items-center gap-3">
              {result.success ? <CheckCircle2 className="text-emerald-600" /> : <AlertTriangle className="text-rose-600" />}
              <div>
                <p className={`font-black ${result.success ? 'text-emerald-900' : 'text-rose-900'}`}>
                  {result.message || (result.success ? '成功' : '失败')}
                </p>
                <p className="text-xs text-slate-500 mt-0.5">QMX code: {String(result.code)}</p>
              </div>
            </div>
            <div className="mt-4 text-xs text-slate-600 space-y-1">
              <p>时间：{result.check_time}</p>
              <p>位置：{result.location_name || selectedLocation?.name}</p>
              <p>坐标：{result.longitude?.toFixed(6)}, {result.latitude?.toFixed(6)}</p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
};

export default RoomCheck;
