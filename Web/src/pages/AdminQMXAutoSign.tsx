import { useCallback, useEffect, useState } from 'react';
import { AnimatePresence } from 'framer-motion';
import { AlarmClock, CheckCircle2, Loader2, MapPin, Play, RefreshCw, ShieldCheck, XCircle } from 'lucide-react';
import toast from 'react-hot-toast';
import AdminModal from '../components/admin/AdminModal';
import AdminShell from '../components/admin/AdminShell';
import client from '../api/client';
import type {
  AdminQMXAutoSignAccount,
  AdminQMXAutoSignOverview,
  AdminQMXAutoSignRecord,
  AdminQMXAutoSignRecordPage,
  ApiResponse,
  QMXRoomCheckLocation,
  QMXRoomCheckPreview,
} from '../types';

const RECORD_PAGE_SIZE = 10;

type LocationPickerState = {
  account: AdminQMXAutoSignAccount;
  preview: QMXRoomCheckPreview;
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

const triggerLabel = (trigger: string) => (
  trigger === 'scheduled' ? '定时' : '手动'
);

const AdminQMXAutoSign = () => {
  const [overview, setOverview] = useState<AdminQMXAutoSignOverview | null>(null);
  const [records, setRecords] = useState<AdminQMXAutoSignRecord[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [totalPages, setTotalPages] = useState(0);
  const [isLoading, setIsLoading] = useState(true);
  const [isLoadingRecords, setIsLoadingRecords] = useState(true);
  const [isSavingSettings, setIsSavingSettings] = useState(false);
  const [busyUid, setBusyUid] = useState<number | null>(null);
  const [previewingUid, setPreviewingUid] = useState<number | null>(null);
  const [picker, setPicker] = useState<LocationPickerState | null>(null);

  const loadOverview = useCallback(async () => {
    setIsLoading(true);
    try {
      const response = await client.get<ApiResponse<AdminQMXAutoSignOverview>>('/admin/qmx-auto-sign');
      setOverview(response.data.data);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取 QMX 自动签到配置失败'));
    } finally {
      setIsLoading(false);
    }
  }, []);

  const loadRecords = useCallback(async (targetPage: number) => {
    setIsLoadingRecords(true);
    try {
      const response = await client.get<ApiResponse<AdminQMXAutoSignRecordPage>>('/admin/qmx-auto-sign/records', {
        params: { page: targetPage, page_size: RECORD_PAGE_SIZE },
      });
      const data = response.data.data;
      setRecords(data.items || []);
      setTotal(data.total || 0);
      setTotalPages(data.total_pages || 0);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取 QMX 自动签到记录失败'));
    } finally {
      setIsLoadingRecords(false);
    }
  }, []);

  useEffect(() => {
    void loadOverview();
  }, [loadOverview]);

  useEffect(() => {
    void loadRecords(page);
  }, [loadRecords, page]);

  const refreshAll = async () => {
    await Promise.all([loadOverview(), loadRecords(page)]);
  };

  const handleToggleSettings = async () => {
    if (!overview) return;
    const nextEnabled = !overview.settings.enabled;
    setIsSavingSettings(true);
    try {
      await client.put('/admin/qmx-auto-sign/settings', { enabled: nextEnabled });
      toast.success(nextEnabled ? '已开启 QMX 定时签到' : '已关闭 QMX 定时签到');
      await loadOverview();
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '更新全局开关失败'));
    } finally {
      setIsSavingSettings(false);
    }
  };

  const handlePreviewLocations = async (account: AdminQMXAutoSignAccount) => {
    setPreviewingUid(account.uid);
    try {
      const response = await client.post<ApiResponse<QMXRoomCheckPreview>>(`/admin/qmx-auto-sign/accounts/${account.uid}/locations/preview`);
      setPicker({ account, preview: response.data.data });
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '读取 QMX 定位点失败'));
    } finally {
      setPreviewingUid(null);
    }
  };

  const handleSaveLocation = async (location: QMXRoomCheckLocation, index: number) => {
    if (!picker) return;
    setBusyUid(picker.account.uid);
    try {
      await client.put(`/admin/qmx-auto-sign/accounts/${picker.account.uid}`, {
        enabled: picker.account.config.enabled,
        location: {
          location_name: location.name || `定位点 ${index + 1}`,
          location_index: index,
          longitude: location.lng,
          latitude: location.lat,
          range: location.range,
        },
      });
      toast.success('已保存 QMX 定位点');
      setPicker(null);
      await loadOverview();
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '保存 QMX 定位点失败'));
    } finally {
      setBusyUid(null);
    }
  };

  const handleToggleAccount = async (account: AdminQMXAutoSignAccount) => {
    const nextEnabled = !account.config.enabled;
    if (nextEnabled && !account.config.location_name) {
      toast.error('请先选择 QMX 定位点');
      return;
    }

    setBusyUid(account.uid);
    try {
      await client.put(`/admin/qmx-auto-sign/accounts/${account.uid}`, { enabled: nextEnabled });
      toast.success(nextEnabled ? '已开启该账号自动签到' : '已关闭该账号自动签到');
      await loadOverview();
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '更新账号开关失败'));
    } finally {
      setBusyUid(null);
    }
  };

  const handleRunAccount = async (account: AdminQMXAutoSignAccount) => {
    setBusyUid(account.uid);
    try {
      const response = await client.post<ApiResponse<AdminQMXAutoSignRecord>>(`/admin/qmx-auto-sign/accounts/${account.uid}/run`);
      if (response.data.data.success) {
        toast.success(response.data.data.message || 'QMX 签到成功');
      } else {
        toast.error(response.data.data.message || 'QMX 返回未成功');
      }
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, 'QMX 立即执行失败'));
    } finally {
      setBusyUid(null);
      await Promise.all([loadOverview(), loadRecords(page)]);
    }
  };

  const settings = overview?.settings;
  const accounts = overview?.accounts || [];

  return (
    <AdminShell
      title="QMX 自动签到"
      subtitle="每天北京时间 22:00 自动查寝"
      action={(
        <button
          onClick={refreshAll}
          className="h-10 w-10 rounded-xl bg-slate-50 text-blue-600 flex items-center justify-center"
          title="刷新"
        >
          <RefreshCw size={17} />
        </button>
      )}
    >
      <div className="flex-1 min-h-0 overflow-y-auto p-4 pb-[calc(16px+var(--sab))] space-y-4 custom-scrollbar">
        <section className="rounded-[2rem] bg-slate-950 p-5 text-white shadow-xl shadow-slate-200 overflow-hidden relative">
          <AlarmClock size={84} className="absolute -right-5 -bottom-5 text-white/10" />
          <p className="text-xs font-black text-cyan-200 uppercase tracking-[0.2em]">QMX Scheduler</p>
          <h3 className="mt-2 text-2xl font-black">晚上十点，自动完成查寝签到</h3>
          <p className="mt-3 text-sm leading-relaxed text-slate-300">
            全局开关控制每天 22:00 的定时任务；单账号开关和定位点在下方分别配置。
          </p>
          <div className="mt-5 grid grid-cols-2 gap-3">
            <div className="rounded-2xl bg-white/10 p-3">
              <p className="text-[10px] font-bold text-slate-400">下次执行</p>
              <p className="mt-1 text-sm font-black">{formatTime(settings?.next_run_at)}</p>
            </div>
            <button
              onClick={handleToggleSettings}
              disabled={!settings || isSavingSettings}
              className={`rounded-2xl p-3 text-left transition-colors disabled:opacity-60 ${
                settings?.enabled ? 'bg-emerald-400 text-emerald-950' : 'bg-white/10 text-white'
              }`}
            >
              <p className="text-[10px] font-bold opacity-70">全局状态</p>
              <p className="mt-1 text-sm font-black flex items-center gap-1.5">
                {isSavingSettings ? <Loader2 size={15} className="animate-spin" /> : settings?.enabled ? <CheckCircle2 size={15} /> : <XCircle size={15} />}
                {settings?.enabled ? '已开启' : '已关闭'}
              </p>
            </button>
          </div>
        </section>

        <section className="bg-white rounded-[1.75rem] border border-slate-100 shadow-sm p-4">
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center gap-2">
              <ShieldCheck size={18} className="text-blue-600" />
              <h3 className="font-black text-slate-900">账号自动签到</h3>
            </div>
            <span className="text-[11px] font-bold text-slate-400">{accounts.length} 个</span>
          </div>

          {isLoading ? (
            <div className="space-y-2">
              {[1, 2, 3].map((item) => <div key={item} className="h-28 rounded-2xl bg-slate-50 animate-pulse" />)}
            </div>
          ) : accounts.length === 0 ? (
            <div className="py-10 text-center rounded-2xl bg-slate-50 text-sm text-slate-400">暂无账号</div>
          ) : (
            <div className="space-y-3">
              {accounts.map((account) => {
                const enabled = account.config.enabled;
                const hasLocation = Boolean(account.config.location_name);
                const busy = busyUid === account.uid;
                return (
                  <div key={account.uid} className="rounded-2xl border border-slate-100 bg-slate-50/70 p-3">
                    <div className="flex items-center gap-3">
                      <div className="w-11 h-11 rounded-xl bg-slate-200 overflow-hidden shrink-0">
                        {account.avatar ? (
                          <img src={account.avatar} alt={account.name} referrerPolicy="no-referrer" className="w-full h-full object-cover" />
                        ) : (
                          <div className="w-full h-full flex items-center justify-center text-slate-400 font-black">
                            {account.name[0] || 'U'}
                          </div>
                        )}
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-1.5">
                          <p className="font-black text-slate-900 truncate">{account.name || '未命名账号'}</p>
                          {account.permission >= 2 && <ShieldCheck size={13} className="text-blue-600 shrink-0" />}
                        </div>
                        <p className="text-[11px] text-slate-500 font-mono">{account.mobile_masked} · UID {account.uid}</p>
                      </div>
                      <span className={`px-2 py-1 rounded-full text-[10px] font-black shrink-0 ${
                        enabled ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-200 text-slate-500'
                      }`}>
                        {enabled ? '自动' : '关闭'}
                      </span>
                    </div>

                    <div className="mt-3 rounded-xl bg-white border border-slate-100 p-3">
                      <div className="flex items-start gap-2">
                        <MapPin size={15} className={hasLocation ? 'text-blue-600 shrink-0 mt-0.5' : 'text-slate-300 shrink-0 mt-0.5'} />
                        <div className="min-w-0 flex-1">
                          <p className="text-xs font-black text-slate-800 truncate">
                            {hasLocation ? account.config.location_name : '未选择定位点'}
                          </p>
                          <p className="mt-0.5 text-[11px] text-slate-400">
                            {hasLocation
                              ? `${formatCoordinate(account.config.longitude)}, ${formatCoordinate(account.config.latitude)} · 范围 ${account.config.range}m`
                              : '开启前必须先读取并选择 QMX 返回的允许定位点'}
                          </p>
                        </div>
                      </div>
                      {account.last_record && (
                        <p className={`mt-2 text-[11px] font-semibold ${account.last_record.success ? 'text-emerald-600' : 'text-rose-600'}`}>
                          最近{triggerLabel(account.last_record.trigger)}：{account.last_record.message || (account.last_record.success ? '成功' : '失败')} · {formatTime(account.last_record.executed_at)}
                        </p>
                      )}
                    </div>

                    <div className="mt-3 grid grid-cols-3 gap-2">
                      <button
                        onClick={() => handlePreviewLocations(account)}
                        disabled={previewingUid === account.uid || busy}
                        className="py-2.5 rounded-xl bg-slate-900 text-white text-xs font-black flex items-center justify-center gap-1 disabled:opacity-50"
                      >
                        {previewingUid === account.uid ? <Loader2 size={14} className="animate-spin" /> : <MapPin size={14} />}
                        定位点
                      </button>
                      <button
                        onClick={() => handleToggleAccount(account)}
                        disabled={busy}
                        className={`py-2.5 rounded-xl text-xs font-black flex items-center justify-center gap-1 disabled:opacity-50 ${
                          enabled ? 'bg-emerald-100 text-emerald-700' : 'bg-blue-600 text-white'
                        }`}
                      >
                        {busy ? <Loader2 size={14} className="animate-spin" /> : enabled ? <CheckCircle2 size={14} /> : <XCircle size={14} />}
                        {enabled ? '关闭' : '开启'}
                      </button>
                      <button
                        onClick={() => handleRunAccount(account)}
                        disabled={busy || !enabled || !hasLocation}
                        className="py-2.5 rounded-xl bg-cyan-100 text-cyan-700 text-xs font-black flex items-center justify-center gap-1 disabled:opacity-40"
                      >
                        {busy ? <Loader2 size={14} className="animate-spin" /> : <Play size={14} />}
                        执行
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>

        <section className="bg-white rounded-[1.75rem] border border-slate-100 shadow-sm p-4">
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center gap-2">
              <AlarmClock size={18} className="text-blue-600" />
              <h3 className="font-black text-slate-900">执行记录</h3>
            </div>
            <span className="text-[11px] font-bold text-slate-400">共 {total} 条</span>
          </div>

          {isLoadingRecords ? (
            <div className="space-y-2">
              {[1, 2, 3].map((item) => <div key={item} className="h-20 rounded-2xl bg-slate-50 animate-pulse" />)}
            </div>
          ) : records.length === 0 ? (
            <div className="py-8 text-center rounded-2xl bg-slate-50 text-sm text-slate-400">暂无执行记录</div>
          ) : (
            <div className="space-y-2">
              {records.map((record) => (
                <div key={record.id} className="rounded-2xl border border-slate-100 bg-slate-50/70 p-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="font-black text-slate-900 truncate">
                        {record.name || `UID ${record.user_uid}`}
                      </p>
                      <p className="text-[11px] text-slate-400">
                        {triggerLabel(record.trigger)} · {formatTime(record.executed_at)}
                      </p>
                    </div>
                    <span className={`px-2 py-1 rounded-full text-[10px] font-black shrink-0 ${
                      record.success ? 'bg-emerald-100 text-emerald-700' : 'bg-rose-100 text-rose-700'
                    }`}>
                      {record.success ? '成功' : '失败'}
                    </span>
                  </div>
                  <p className="mt-2 text-xs text-slate-600 leading-relaxed">{record.message || '-'}</p>
                  <p className="mt-1 text-[11px] text-slate-400 truncate">
                    {record.batch_name || '未知批次'} · {record.location_name || '未提交定位点'}
                  </p>
                </div>
              ))}
            </div>
          )}

          <div className="mt-4 flex items-center justify-between">
            <button
              onClick={() => setPage((current) => Math.max(1, current - 1))}
              disabled={page <= 1 || isLoadingRecords}
              className="px-4 py-2 rounded-xl bg-slate-100 text-slate-600 text-xs font-black disabled:opacity-40"
            >
              上一页
            </button>
            <span className="text-xs font-bold text-slate-400">
              {page} / {Math.max(totalPages, 1)}
            </span>
            <button
              onClick={() => setPage((current) => current + 1)}
              disabled={page >= totalPages || isLoadingRecords}
              className="px-4 py-2 rounded-xl bg-slate-100 text-slate-600 text-xs font-black disabled:opacity-40"
            >
              下一页
            </button>
          </div>
        </section>
      </div>

      <AnimatePresence>
        {picker && (
          <AdminModal title="选择 QMX 定位点" onClose={() => setPicker(null)}>
            <div className="space-y-3">
              <div className="rounded-2xl bg-slate-50 border border-slate-100 p-3">
                <p className="text-sm font-black text-slate-900">{picker.preview.batch_name || '当前查寝批次'}</p>
                <p className="mt-1 text-xs text-slate-500">
                  {picker.preview.check_date} {picker.preview.start_time}-{picker.preview.end_time}
                </p>
              </div>
              {picker.preview.unsupported?.length > 0 && (
                <div className="rounded-2xl bg-amber-50 text-amber-700 p-3 text-xs font-semibold">
                  当前批次包含暂不支持的要求：{picker.preview.unsupported.join('、')}。保存定位点后仍可能无法自动提交。
                </div>
              )}
              <div className="max-h-80 overflow-y-auto space-y-2 pr-1 custom-scrollbar">
                {picker.preview.locations.length === 0 ? (
                  <div className="py-8 text-center rounded-2xl bg-slate-50 text-sm text-slate-400">没有可选定位点</div>
                ) : picker.preview.locations.map((location, index) => (
                  <button
                    key={`${location.lng}-${location.lat}-${index}`}
                    onClick={() => handleSaveLocation(location, index)}
                    disabled={busyUid === picker.account.uid}
                    className="w-full rounded-2xl border border-slate-100 bg-slate-50 p-4 text-left flex items-start gap-3 disabled:opacity-50"
                  >
                    <MapPin size={18} className="text-blue-600 shrink-0 mt-0.5" />
                    <div className="min-w-0 flex-1">
                      <p className="font-black text-slate-900 truncate">{location.name || `定位点 ${index + 1}`}</p>
                      <p className="mt-1 text-xs text-slate-500">
                        {location.lng.toFixed(6)}, {location.lat.toFixed(6)} · 范围 {location.range}m
                      </p>
                    </div>
                  </button>
                ))}
              </div>
            </div>
          </AdminModal>
        )}
      </AnimatePresence>
    </AdminShell>
  );
};

export default AdminQMXAutoSign;
