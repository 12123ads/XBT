import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { motion, AnimatePresence } from 'framer-motion';
import {
  ChevronLeft,
  LogOut,
  Trash2,
  User as UserIcon,
  Plus,
  Loader2,
  MapPin,
  RefreshCw,
  ToggleLeft,
  ToggleRight,
  CheckCircle2,
  XCircle,
} from 'lucide-react';
import toast from 'react-hot-toast';
import { useAuthStore } from '../store/auth';
import client from '../api/client';
import AdminModal from '../components/admin/AdminModal';
import type {
  AdminQMXAutoSignRecord,
  OwnQMXAutoSignConfig,
  OwnQMXAutoSignSettings,
  QMXLocationPreset,
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

const getRecordSummary = (record: AdminQMXAutoSignRecord | null) => {
  if (!record) {
    return {
      text: '暂无签到记录',
      success: null as boolean | null,
    };
  }

  return {
    text: `${record.success ? '成功' : '失败'} · ${formatTime(record.executed_at)}`,
    success: record.success,
  };
};

const AccountManagement = () => {
  const navigate = useNavigate();
  const { accounts, activeUid, switchAccount, removeAccount } = useAuthStore();
  const [settings, setSettings] = useState<OwnQMXAutoSignSettings | null>(null);
  const [isLoadingSettings, setIsLoadingSettings] = useState(true);
  const [isSavingToggle, setIsSavingToggle] = useState(false);
  const [isRefreshingLocations, setIsRefreshingLocations] = useState(false);
  const [isPickerOpen, setIsPickerOpen] = useState(false);
  const [preview, setPreview] = useState<QMXRoomCheckPreview | null>(null);
  const [isSavingLocationKey, setIsSavingLocationKey] = useState<string | null>(null);

  const sortedAccounts = useMemo(() => {
    return [...accounts].sort((a, b) => {
      if (a.user.uid === activeUid) return -1;
      if (b.user.uid === activeUid) return 1;
      return 0;
    });
  }, [accounts, activeUid]);

  const onlineLocations = useMemo<LocationOption[]>(() => {
    if (!preview?.locations?.length) return [];
    return preview.locations.map((location, index) => ({
      key: `online-${index}`,
      source: 'online',
      index,
      name: location.name || `在线地点 ${index + 1}`,
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
      index,
      name: preset.name || `预设地点 ${index + 1}`,
      lng: preset.lng,
      lat: preset.lat,
      range: preset.range,
    }));
  }, [settings]);

  const activeLocationKey = useMemo(() => {
    const config = settings?.config;
    if (!config?.location_name) return null;

    const onlineMatch = onlineLocations.find((item) => (
      item.name === config.location_name
      && item.lng === config.longitude
      && item.lat === config.latitude
      && item.range === config.range
    ));
    if (onlineMatch) return onlineMatch.key;

    const presetMatch = presetLocations.find((item) => (
      item.name === config.location_name
      && item.lng === config.longitude
      && item.lat === config.latitude
      && item.range === config.range
    ));
    return presetMatch?.key ?? null;
  }, [onlineLocations, presetLocations, settings]);

  const loadSettings = async () => {
    setIsLoadingSettings(true);
    try {
      const response = await client.get<OwnQMXAutoSignSettings>('/qmx/auto-sign/settings');
      setSettings(response.data);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取 QMX 配置失败'));
    } finally {
      setIsLoadingSettings(false);
    }
  };

  useEffect(() => {
    void loadSettings();
  }, []);

  const handleSwitch = (uid: number) => {
    if (uid === activeUid) return;
    switchAccount(uid);
    toast.success('已切换账号');
    navigate('/');
    void loadSettings();
  };

  const handleRemove = (e: React.MouseEvent, uid: number) => {
    e.stopPropagation();
    if (!confirm('确定要移除此账号吗？')) return;
    removeAccount(uid);
    toast.success('已移除账号');
  };

  const handleToggleQMX = async () => {
    if (!settings?.config) return;
    const nextEnabled = !settings.config.enabled;
    if (nextEnabled && !settings.config.location_name) {
      toast.error('请先选择签到地点');
      return;
    }

    setIsSavingToggle(true);
    try {
      const response = await client.put<OwnQMXAutoSignConfig>('/qmx/auto-sign/settings', {
        enabled: nextEnabled,
      });
      setSettings((current) => (current ? { ...current, config: response.data } : current));
      toast.success(nextEnabled ? '已开启自动签到' : '已关闭自动签到');
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '更新自动签到开关失败'));
    } finally {
      setIsSavingToggle(false);
    }
  };

  const handlePreviewLocations = async () => {
    setIsRefreshingLocations(true);
    try {
      const response = await client.post<QMXRoomCheckPreview>('/qmx/auto-sign/locations/preview');
      setPreview(response.data);
      setIsPickerOpen(true);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取签到地点失败'));
    } finally {
      setIsRefreshingLocations(false);
    }
  };

  const handleSaveLocation = async (location: LocationOption) => {
    if (!settings?.config) return;

    setIsSavingLocationKey(location.key);
    try {
      const response = await client.put<OwnQMXAutoSignConfig>('/qmx/auto-sign/settings', {
        enabled: settings.config.enabled,
        location: {
          location_name: location.name,
          location_index: location.index,
          longitude: location.lng,
          latitude: location.lat,
          range: location.range,
        },
      });
      setSettings((current) => (current ? { ...current, config: response.data } : current));
      setIsPickerOpen(false);
      toast.success('已保存签到地点');
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '保存签到地点失败'));
    } finally {
      setIsSavingLocationKey(null);
    }
  };

  const recordSummary = getRecordSummary(settings?.last_record ?? null);

  return (
    <div className="flex-1 flex-col bg-slate-50 relative overflow-hidden">
      <div className="bg-white sticky top-0 z-10 border-b border-slate-100 px-4 h-[calc(80px+var(--sat))] pt-[var(--sat)] flex items-center shrink-0">
        <div className="flex items-center">
          <button
            onClick={() => navigate(-1)}
            className="p-2 -ml-2 text-slate-600 hover:bg-slate-50 rounded-lg transition-colors"
          >
            <ChevronLeft size={24} />
          </button>
          <h2 className="ml-2 font-bold text-slate-900 text-lg">账号管理</h2>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-4 space-y-4 pb-[calc(40px+var(--sab))] custom-scrollbar">
        <div className="text-xs font-semibold text-slate-400 uppercase tracking-wider px-1">
          已保存的账号
        </div>

        <div className="space-y-3">
          <AnimatePresence initial={false}>
            {sortedAccounts.map((account) => (
              <motion.div
                key={account.user.uid}
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, scale: 0.95 }}
                whileTap={{ scale: 0.92 }}
                onClick={() => handleSwitch(account.user.uid)}
                className={`p-4 rounded-3xl border transition-all cursor-pointer flex items-center justify-between group ${
                  account.user.uid === activeUid
                    ? 'border-blue-500 bg-blue-50/30 shadow-sm'
                    : 'bg-white border-slate-100 hover:border-slate-200 shadow-sm'
                }`}
              >
                <div className="flex items-center space-x-4">
                  <div className="w-12 h-12 rounded-xl bg-slate-100 overflow-hidden border-2 border-white shadow-sm">
                    {account.user.avatar ? (
                      <img src={account.user.avatar} alt={account.user.name} referrerPolicy="no-referrer" className="w-full h-full object-cover" />
                    ) : (
                      <div className="w-full h-full flex items-center justify-center text-slate-400">
                        <UserIcon size={24} />
                      </div>
                    )}
                  </div>
                  <div>
                    <div className="font-bold text-slate-900 flex items-center">
                      {account.user.name}
                      {account.user.uid === activeUid && (
                        <span className="ml-2 px-1.5 py-0.5 bg-blue-600 text-[10px] text-white rounded-md font-medium uppercase">
                          当前
                        </span>
                      )}
                    </div>
                    <div className="text-xs text-slate-500 mt-0.5">{account.user.mobile}</div>
                  </div>
                </div>

                <div className="flex items-center space-x-2">
                  <button
                    onClick={(e) => handleRemove(e, account.user.uid)}
                    className={`w-8 h-8 flex items-center justify-center rounded-full transition-all ${
                      account.user.uid === activeUid
                        ? 'text-red-500 hover:bg-red-50'
                        : 'text-slate-300 hover:text-red-500 hover:bg-red-50'
                    }`}
                    title={account.user.uid === activeUid ? '退出登录' : '移除账号'}
                  >
                    {account.user.uid === activeUid ? <LogOut size={18} /> : <Trash2 size={18} />}
                  </button>
                </div>
              </motion.div>
            ))}
          </AnimatePresence>

          <motion.button
            whileTap={{ scale: 0.92 }}
            onClick={() => navigate('/login')}
            className="w-full p-4 border-2 border-dashed border-slate-200 rounded-3xl flex items-center justify-center space-x-2 text-slate-500 hover:border-blue-300 hover:text-blue-600 hover:bg-blue-50/30 transition-all"
          >
            <Plus size={20} />
            <span className="font-bold">添加新账号</span>
          </motion.button>
        </div>

        <div className="bg-white rounded-3xl border-slate-100 shadow-sm p-4 space-y-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <div className="text-xs font-semibold text-slate-400 uppercase tracking-wider">
                QMX 自动签到
              </div>
              <h3 className="mt-1 text-lg font-bold text-slate-900">个人签到配置</h3>
            </div>
            <button
              onClick={handleToggleQMX}
              disabled={isLoadingSettings || isSavingToggle}
              className="shrink-0 text-blue-600 disabled:text-slate-300 transition-colors"
              title="切换自动签到"
            >
              {isSavingToggle ? (
                <Loader2 size={28} className="animate-spin" />
              ) : settings?.config.enabled ? (
                <ToggleRight size={32} />
              ) : (
                <ToggleLeft size={32} />
              )}
            </button>
          </div>

          {isLoadingSettings ? (
            <div className="space-y-3">
              <div className="h-20 rounded-2xl bg-slate-50 animate-pulse" />
              <div className="h-12 rounded-2xl bg-slate-50 animate-pulse" />
            </div>
          ) : (
            <>
              <div className="rounded-2xl bg-slate-50 border-slate-100 p-4 space-y-3">
                <div className="flex items-start gap-3">
                  <MapPin size={18} className="text-blue-600 mt-0.5 shrink-0" />
                  <div className="min-w-0 flex-1">
                    <p className="text-xs font-semibold text-slate-400">当前签到地点</p>
                    <p className="mt-1 font-bold text-slate-900 break-words">
                      {settings?.config.location_name || '未选择签到地点'}
                    </p>
                    <p className="mt-1 text-xs text-slate-500">
                      {settings?.config.location_name
                        ? `${formatCoordinate(settings.config.longitude)}, ${formatCoordinate(settings.config.latitude)} · 范围 ${settings.config.range}m`
                        : '请先在线获取或从预设地点中选择'}
                    </p>
                  </div>
                </div>

                <div className="rounded-2xl bg-white border-slate-100 p-3 flex items-center justify-between gap-3">
                  <div>
                    <p className="text-xs font-semibold text-slate-400">最近签到记录</p>
                    <p className={`mt-1 text-sm font-bold ${recordSummary.success === null ? 'text-slate-500' : recordSummary.success ? 'text-emerald-600' : 'text-rose-600'}`}>
                      {recordSummary.text}
                    </p>
                  </div>
                  {recordSummary.success === null ? null : recordSummary.success ? (
                    <CheckCircle2 size={20} className="text-emerald-600 shrink-0" />
                  ) : (
                    <XCircle size={20} className="text-rose-600 shrink-0" />
                  )}
                </div>
              </div>

              <button
                onClick={handlePreviewLocations}
                disabled={isRefreshingLocations}
                className="w-full rounded-2xl bg-blue-600 text-white py-3 font-bold flex items-center justify-center gap-2 disabled:opacity-70"
              >
                {isRefreshingLocations ? <Loader2 size={18} className="animate-spin" /> : <RefreshCw size={18} />}
                在线获取签到地点
              </button>
            </>
          )}
        </div>

        <div className="pt-8 text-center pb-8">
          <p className="text-xs text-slate-400 leading-relaxed px-8">
            移除账号仅会从本地清除登录状态，不会影响您的学习通账号数据。
          </p>
        </div>
      </div>

      <AnimatePresence>
        {isPickerOpen && (
          <AdminModal title="选择签到地点" onClose={() => setIsPickerOpen(false)}>
            <div className="space-y-4 max-h-[70vh] overflow-y-auto pr-1 custom-scrollbar">
              {preview && (
                <div className="rounded-2xl bg-slate-50 border-slate-100 p-4 space-y-1.5">
                  <p className="text-sm font-bold text-slate-900">{preview.batch_name || '在线地点预览'}</p>
                  <p className="text-xs text-slate-500">签到时间：{preview.check_date} {preview.start_time} - {preview.end_time}</p>
                </div>
              )}

              <div className="space-y-2">
                <div className="text-xs font-semibold text-slate-400 uppercase tracking-wider">在线地点</div>
                {onlineLocations.length === 0 ? (
                  <div className="rounded-2xl bg-slate-50 border-slate-100 p-4 text-sm text-slate-400">暂无在线地点，请稍后重试。</div>
                ) : onlineLocations.map((location) => (
                  <button
                    key={location.key}
                    onClick={() => handleSaveLocation(location)}
                    disabled={isSavingLocationKey !== null}
                    className={`w-full text-left rounded-2xl border p-4 transition-all ${activeLocationKey === location.key ? 'border-blue-500 bg-blue-50/40' : 'border-slate-100 bg-white hover:border-slate-200'}`}
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <p className="font-bold text-slate-900 break-words">{location.name}</p>
                        <p className="mt-1 text-xs text-slate-500">
                          {formatCoordinate(location.lng)}, {formatCoordinate(location.lat)} · 范围 {location.range}m
                        </p>
                        {typeof location.distance === 'number' && (
                          <p className="mt-1 text-xs text-slate-400">距离参考点 {location.distance.toFixed(0)}m</p>
                        )}
                      </div>
                      {isSavingLocationKey === location.key ? <Loader2 size={18} className="animate-spin text-blue-600 shrink-0" /> : activeLocationKey === location.key ? <CheckCircle2 size={18} className="text-blue-600 shrink-0" /> : null}
                    </div>
                  </button>
                ))}
              </div>

              <div className="space-y-2">
                <div className="text-xs font-semibold text-slate-400 uppercase tracking-wider">预设地点</div>
                {presetLocations.length === 0 ? (
                  <div className="rounded-2xl bg-slate-50 border-slate-100 p-4 text-sm text-slate-400">暂无预设地点。</div>
                ) : presetLocations.map((location) => (
                  <button
                    key={location.key}
                    onClick={() => handleSaveLocation(location)}
                    disabled={isSavingLocationKey !== null}
                    className={`w-full text-left rounded-2xl border p-4 transition-all ${activeLocationKey === location.key ? 'border-blue-500 bg-blue-50/40' : 'border-slate-100 bg-white hover:border-slate-200'}`}
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <p className="font-bold text-slate-900 break-words">{location.name}</p>
                        <p className="mt-1 text-xs text-slate-500">
                          {formatCoordinate(location.lng)}, {formatCoordinate(location.lat)} · 范围 {location.range}m
                        </p>
                      </div>
                      {isSavingLocationKey === location.key ? <Loader2 size={18} className="animate-spin text-blue-600 shrink-0" /> : activeLocationKey === location.key ? <CheckCircle2 size={18} className="text-blue-600 shrink-0" /> : null}
                    </div>
                  </button>
                ))}
              </div>
            </div>
          </AdminModal>
        )}
      </AnimatePresence>

      <style>{`.custom-scrollbar::-webkit-scrollbar { width: 0px; }`}</style>
    </div>
  );
};

export default AccountManagement;
