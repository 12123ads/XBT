import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AnimatePresence, motion } from 'framer-motion';
import {
  AlarmClock,
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  Loader2,
  MapPin,
  Play,
  RefreshCw,
  ShieldCheck,
  ToggleLeft,
  ToggleRight,
  XCircle,
} from 'lucide-react';
import toast from 'react-hot-toast';
import client from '../api/client';
import type {
  AdminQMXAutoSignRecord,
  ApiResponse,
  OwnQMXAutoSignConfig,
  OwnQMXAutoSignSettings,
  QMXLocationPreset,
  QMXRoomCheckLocation,
  QMXRoomCheckPreview,
} from '../types';

type LocationOption = {
  key: string;
  source: 'online' | 'preset';
  index: number;
  name: string;
  lng: number;
  lat: number;
  range: number;
  distance?: number;
};

const requirementLabels: Record<string, string> = {
  photo: '拍照',
  face: '人脸识别',
  bluetooth: '蓝牙',
  special_sdk: '特殊定位 SDK',
};

const getErrorMessage = (error: unknown, fallback: string) => (
  error instanceof Error ? error.message : fallback
);

const formatTime = (value?: number) => {
  if (!value) return '暂无';
  return new Date(value).toLocaleString('zh-CN', { hour12: false });
};

const formatCoordinate = (value: number) => (
  Number.isFinite(value) && value !== 0 ? value.toFixed(6) : '-'
);

const hasSavedLocation = (config?: OwnQMXAutoSignConfig | null) => {
  if (!config?.location_name) return false;
  return config.location_index >= 0 || (config.longitude !== 0 && config.latitude !== 0);
};

const triggerLabel = (trigger: string) => (
  trigger === 'scheduled' ? '定时' : '手动'
);

const recordSummary = (record: AdminQMXAutoSignRecord | null) => {
  if (!record) {
    return {
      text: '暂无执行记录',
      success: null as boolean | null,
    };
  }
  return {
    text: `${record.success ? '成功' : '失败'} · ${triggerLabel(record.trigger)} · ${formatTime(record.executed_at)}`,
    success: record.success,
  };
};

const locationMatches = (location: LocationOption, config?: OwnQMXAutoSignConfig | null) => {
  if (!config?.location_name) return false;
  return location.name === config.location_name
    && location.lng === config.longitude
    && location.lat === config.latitude
    && location.range === config.range
    && (location.source === 'preset' ? config.location_index < 0 : config.location_index === location.index);
};

const RoomCheck = () => {
  const navigate = useNavigate();
  const [settings, setSettings] = useState<OwnQMXAutoSignSettings | null>(null);
  const [preview, setPreview] = useState<QMXRoomCheckPreview | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isSavingToggle, setIsSavingToggle] = useState(false);
  const [isPickerOpen, setIsPickerOpen] = useState(false);
  const [isPreviewing, setIsPreviewing] = useState(false);
  const [isSavingLocationKey, setIsSavingLocationKey] = useState<string | null>(null);
  const [isRunning, setIsRunning] = useState(false);

  const loadSettings = useCallback(async () => {
    setIsLoading(true);
    try {
      const response = await client.get<ApiResponse<OwnQMXAutoSignSettings>>('/qmx/auto-sign/settings');
      setSettings(response.data.data);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取 QMX 自动签到配置失败'));
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadSettings();
  }, [loadSettings]);

  const onlineLocations = useMemo<LocationOption[]>(() => {
    if (!preview?.locations?.length) return [];
    return preview.locations.map((location: QMXRoomCheckLocation, index) => ({
      key: `online-${index}`,
      source: 'online',
      index,
      name: location.name || `在线定位点 ${index + 1}`,
      lng: location.lng,
      lat: location.lat,
      range: location.range,
      distance: location.distance,
    }));
  }, [preview]);

  const presetLocations = useMemo<LocationOption[]>(() => {
    if (!settings?.presets?.length) return [];
    return settings.presets.map((preset: QMXLocationPreset, index) => ({
      key: `preset-${index}`,
      source: 'preset',
      index: -1,
      name: preset.name || `预设定位点 ${index + 1}`,
      lng: preset.lng,
      lat: preset.lat,
      range: preset.range,
    }));
  }, [settings]);

  const activeLocationKey = useMemo(() => {
    const config = settings?.config;
    return [...onlineLocations, ...presetLocations].find((location) => locationMatches(location, config))?.key ?? null;
  }, [onlineLocations, presetLocations, settings]);

  const config = settings?.config;
  const globalSettings = settings?.settings;
  const lastRecord = settings?.last_record ?? null;
  const latest = recordSummary(lastRecord);
  const canRun = hasSavedLocation(config);
  const previewUnsupported = preview?.unsupported ?? [];

  const handleToggle = async () => {
    if (!settings?.config) return;
    const nextEnabled = !settings.config.enabled;
    if (nextEnabled && !hasSavedLocation(settings.config)) {
      toast.error('请先选择 QMX 定位点');
      return;
    }

    setIsSavingToggle(true);
    try {
      const response = await client.put<ApiResponse<OwnQMXAutoSignConfig>>('/qmx/auto-sign/settings', {
        enabled: nextEnabled,
      });
      setSettings((current) => (current ? { ...current, config: response.data.data } : current));
      toast.success(nextEnabled ? '已开启个人自动签到' : '已关闭个人自动签到');
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '更新自动签到开关失败'));
    } finally {
      setIsSavingToggle(false);
    }
  };

  const loadPreviewLocations = async () => {
    setIsPreviewing(true);
    try {
      const response = await client.post<ApiResponse<QMXRoomCheckPreview>>('/qmx/auto-sign/locations/preview');
      const data = response.data.data;
      setPreview(data);
      if (data.unsupported?.length) {
        toast.error(`当前批次需要 ${data.unsupported.map((item) => requirementLabels[item] || item).join('、')}，自动签到可能失败`);
      }
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '读取在线定位点失败，可先使用预设地点'));
      setPreview(null);
    } finally {
      setIsPreviewing(false);
    }
  };

  const handleOpenPicker = () => {
    setIsPickerOpen(true);
    void loadPreviewLocations();
  };

  const handleSaveLocation = async (location: LocationOption) => {
    if (!settings?.config) return;
    setIsSavingLocationKey(location.key);
    try {
      const response = await client.put<ApiResponse<OwnQMXAutoSignConfig>>('/qmx/auto-sign/settings', {
        enabled: settings.config.enabled,
        location: {
          location_name: location.name,
          location_index: location.source === 'preset' ? -1 : location.index,
          longitude: location.lng,
          latitude: location.lat,
          range: location.range,
        },
      });
      setSettings((current) => (current ? { ...current, config: response.data.data } : current));
      setIsPickerOpen(false);
      toast.success('已保存 QMX 定位点');
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '保存 QMX 定位点失败'));
    } finally {
      setIsSavingLocationKey(null);
    }
  };

  const handleRunNow = async () => {
    if (!canRun) {
      toast.error('请先选择 QMX 定位点');
      return;
    }

    setIsRunning(true);
    try {
      const response = await client.post<ApiResponse<AdminQMXAutoSignRecord>>('/qmx/auto-sign/run');
      const record = response.data.data;
      setSettings((current) => (current ? { ...current, last_record: record } : current));
      if (record.success) {
        toast.success(record.message || 'QMX 签到成功');
      } else {
        toast.error(record.message || 'QMX 返回未成功');
      }
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '立即执行失败'));
    } finally {
      setIsRunning(false);
    }
  };

  const renderLocationButton = (location: LocationOption) => {
    const active = activeLocationKey === location.key;
    const saving = isSavingLocationKey === location.key;
    return (
      <button
        key={location.key}
        type="button"
        onClick={() => handleSaveLocation(location)}
        disabled={isSavingLocationKey !== null}
        className={`w-full text-left rounded-2xl border p-4 transition-colors disabled:opacity-60 ${
          active ? 'border-cyan-500 bg-cyan-50' : 'border-slate-100 bg-slate-50 hover:border-slate-200'
        }`}
      >
        <div className="flex items-start gap-3">
          <MapPin size={18} className={location.source === 'preset' ? 'text-blue-600 shrink-0 mt-0.5' : 'text-cyan-600 shrink-0 mt-0.5'} />
          <div className="min-w-0 flex-1">
            <p className="font-black text-sm text-slate-900 break-words">{location.name}</p>
            <p className="mt-1 text-xs text-slate-500">
              {formatCoordinate(location.lng)}, {formatCoordinate(location.lat)} · 范围 {location.range}m
            </p>
            {typeof location.distance === 'number' && (
              <p className="mt-1 text-xs text-slate-400">距离参考点 {location.distance.toFixed(0)}m</p>
            )}
          </div>
          {saving ? (
            <Loader2 size={18} className="animate-spin text-cyan-600 shrink-0" />
          ) : active ? (
            <CheckCircle2 size={18} className="text-cyan-600 shrink-0" />
          ) : null}
        </div>
      </button>
    );
  };

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
          <h1 className="font-black text-slate-900">QMX 自动签到</h1>
          <p className="text-xs text-slate-500">个人查寝签到配置</p>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-4 space-y-4 pb-[calc(24px+var(--sab))] custom-scrollbar">
        {isLoading ? (
          <div className="space-y-4">
            <div className="h-48 rounded-[28px] bg-white animate-pulse" />
            <div className="h-40 rounded-[28px] bg-white animate-pulse" />
          </div>
        ) : (
          <>
            <section className="rounded-[28px] bg-slate-950 text-white p-5 shadow-xl shadow-slate-200 overflow-hidden relative">
              <AlarmClock size={92} className="absolute -right-5 -bottom-6 text-white/10" />
              <div className="relative">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="w-11 h-11 rounded-2xl bg-cyan-300 text-slate-950 flex items-center justify-center shrink-0">
                      <ShieldCheck size={22} />
                    </div>
                    <div className="min-w-0">
                      <p className="text-xs font-black text-cyan-200 uppercase tracking-[0.18em]">QMX Scheduler</p>
                      <h2 className="mt-1 text-xl font-black truncate">晚上十点自动查寝</h2>
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={handleToggle}
                    disabled={isSavingToggle || !settings}
                    className="text-cyan-200 disabled:text-slate-500 shrink-0"
                    title="切换个人自动签到"
                  >
                    {isSavingToggle ? (
                      <Loader2 size={32} className="animate-spin" />
                    ) : config?.enabled ? (
                      <ToggleRight size={36} />
                    ) : (
                      <ToggleLeft size={36} />
                    )}
                  </button>
                </div>

                <div className="mt-5 grid grid-cols-2 gap-3">
                  <div className="rounded-2xl bg-white/10 p-3">
                    <p className="text-[10px] font-bold text-slate-400">下次执行</p>
                    <p className="mt-1 text-sm font-black">{formatTime(globalSettings?.next_run_at)}</p>
                  </div>
                  <div className={`rounded-2xl p-3 ${config?.enabled ? 'bg-emerald-400 text-emerald-950' : 'bg-white/10 text-white'}`}>
                    <p className="text-[10px] font-bold opacity-70">个人状态</p>
                    <p className="mt-1 text-sm font-black flex items-center gap-1.5">
                      {config?.enabled ? <CheckCircle2 size={15} /> : <XCircle size={15} />}
                      {config?.enabled ? '已开启' : '已关闭'}
                    </p>
                  </div>
                </div>

                {!globalSettings?.enabled && (
                  <div className="mt-4 rounded-2xl bg-amber-400/15 text-amber-100 p-3 text-xs font-semibold flex gap-2">
                    <AlertTriangle size={16} className="shrink-0 mt-0.5" />
                    <span>全局定时任务当前关闭；你仍可保存个人配置并立即执行一次。</span>
                  </div>
                )}
              </div>
            </section>

            <section className="rounded-[28px] bg-white border border-slate-100 shadow-sm overflow-hidden">
              <div className="p-5 border-b border-slate-100">
                <div className="flex items-start gap-3">
                  <div className={`w-11 h-11 rounded-2xl flex items-center justify-center shrink-0 ${canRun ? 'bg-cyan-50 text-cyan-600' : 'bg-slate-100 text-slate-400'}`}>
                    <MapPin size={22} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="text-xs font-bold text-slate-400 uppercase tracking-widest">当前定位点</p>
                    <h2 className="mt-1 text-xl font-black text-slate-900 break-words">
                      {config?.location_name || '未选择定位点'}
                    </h2>
                    <p className="mt-1 text-sm text-slate-500">
                      {canRun
                        ? `${formatCoordinate(config?.longitude || 0)}, ${formatCoordinate(config?.latitude || 0)} · 范围 ${config?.range || 0}m`
                        : '选择在线定位点或预设定位点后即可开启自动签到'}
                    </p>
                  </div>
                </div>
              </div>

              <div className="p-5 grid grid-cols-2 gap-3">
                <button
                  type="button"
                  onClick={handleOpenPicker}
                  className="h-12 rounded-2xl bg-cyan-500 text-white font-black flex items-center justify-center gap-2"
                >
                  <MapPin size={18} />
                  选择地点
                </button>
                <button
                  type="button"
                  onClick={handleRunNow}
                  disabled={isRunning || !canRun}
                  className="h-12 rounded-2xl bg-slate-950 text-white font-black flex items-center justify-center gap-2 disabled:opacity-40"
                >
                  {isRunning ? <Loader2 size={18} className="animate-spin" /> : <Play size={18} />}
                  立即执行
                </button>
              </div>
            </section>

            <section className={`rounded-[28px] border p-5 ${latest.success === null ? 'bg-white border-slate-100' : latest.success ? 'bg-emerald-50 border-emerald-100' : 'bg-rose-50 border-rose-100'}`}>
              <div className="flex items-center justify-between gap-3">
                <div>
                  <p className="text-xs font-bold text-slate-400 uppercase tracking-widest">最近记录</p>
                  <p className={`mt-1 font-black ${latest.success === null ? 'text-slate-700' : latest.success ? 'text-emerald-800' : 'text-rose-800'}`}>
                    {latest.text}
                  </p>
                </div>
                {latest.success === null ? (
                  <RefreshCw size={20} className="text-slate-300" />
                ) : latest.success ? (
                  <CheckCircle2 size={24} className="text-emerald-600" />
                ) : (
                  <XCircle size={24} className="text-rose-600" />
                )}
              </div>
              {lastRecord && (
                <div className="mt-4 text-xs text-slate-600 space-y-1">
                  <p>结果：{lastRecord.message || (lastRecord.success ? '成功' : '失败')}</p>
                  <p>批次：{lastRecord.batch_name || '未知批次'}</p>
                  <p>位置：{lastRecord.location_name || '-'}</p>
                  <p>坐标：{formatCoordinate(lastRecord.longitude)}, {formatCoordinate(lastRecord.latitude)}</p>
                </div>
              )}
            </section>

            <button
              type="button"
              onClick={loadSettings}
              className="w-full h-12 rounded-2xl bg-white border border-slate-100 text-slate-600 font-black flex items-center justify-center gap-2"
            >
              <RefreshCw size={17} />
              刷新状态
            </button>
          </>
        )}
      </div>

      <AnimatePresence>
        {isPickerOpen && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/60 backdrop-blur-md"
            onClick={() => setIsPickerOpen(false)}
          >
            <motion.div
              initial={{ y: '100%' }}
              animate={{ y: 0 }}
              exit={{ y: '100%' }}
              transition={{ type: 'spring', damping: 28, stiffness: 260 }}
              className="w-full max-w-[480px] bg-white rounded-t-[2rem] p-6 pb-[calc(24px+var(--sab))] shadow-2xl max-h-[82vh] flex flex-col"
              onClick={(event) => event.stopPropagation()}
            >
              <div className="w-12 h-1.5 rounded-full bg-slate-200 mx-auto mb-5 shrink-0" />
              <div className="flex items-center justify-between mb-5 shrink-0">
                <h3 className="text-lg font-black text-slate-900">选择 QMX 定位点</h3>
                <button
                  type="button"
                  onClick={loadPreviewLocations}
                  disabled={isPreviewing}
                  className="w-9 h-9 rounded-full bg-slate-100 text-cyan-600 flex items-center justify-center disabled:opacity-60"
                  title="刷新在线定位点"
                >
                  {isPreviewing ? <Loader2 size={18} className="animate-spin" /> : <RefreshCw size={18} />}
                </button>
              </div>

              <div className="flex-1 overflow-y-auto space-y-5 pr-1 custom-scrollbar">
                <div className="space-y-2">
                  <div className="flex items-center justify-between">
                    <p className="text-xs font-black text-slate-400 uppercase tracking-widest">在线定位点</p>
                    {preview && <span className="text-[11px] font-bold text-slate-400">{onlineLocations.length} 个</span>}
                  </div>
                  {previewUnsupported.length > 0 && (
                    <div className="rounded-2xl bg-amber-50 text-amber-800 p-3 text-xs font-semibold flex gap-2">
                      <AlertTriangle size={16} className="shrink-0 mt-0.5" />
                      <span>当前批次要求 {previewUnsupported.map((item) => requirementLabels[item] || item).join('、')}，自动提交可能失败。</span>
                    </div>
                  )}
                  {isPreviewing ? (
                    <div className="h-24 rounded-2xl bg-slate-50 animate-pulse" />
                  ) : onlineLocations.length > 0 ? (
                    onlineLocations.map(renderLocationButton)
                  ) : (
                    <div className="rounded-2xl bg-slate-50 border border-slate-100 p-4 text-sm font-semibold text-slate-400">
                      暂无在线定位点
                    </div>
                  )}
                </div>

                <div className="space-y-2">
                  <div className="flex items-center justify-between">
                    <p className="text-xs font-black text-slate-400 uppercase tracking-widest">预设定位点</p>
                    <span className="text-[11px] font-bold text-slate-400">{presetLocations.length} 个</span>
                  </div>
                  {presetLocations.length > 0 ? (
                    presetLocations.map(renderLocationButton)
                  ) : (
                    <div className="rounded-2xl bg-slate-50 border border-slate-100 p-4 text-sm font-semibold text-slate-400">
                      暂无预设定位点
                    </div>
                  )}
                </div>
              </div>
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>
      <style>{`.custom-scrollbar::-webkit-scrollbar { width: 0px; }`}</style>
    </div>
  );
};

export default RoomCheck;
