import { useCallback, useEffect, useState } from 'react';
import { Bell, BookOpen, Loader2, MapPin, Plus, Save, Settings2, Trash2 } from 'lucide-react';
import toast from 'react-hot-toast';
import AdminShell from '../components/admin/AdminShell';
import client from '../api/client';
import type { AdminRuntimeSettings, ApiResponse, CourseLocationPreset, QMXLocationPreset } from '../types';
import { getBrowserLocation } from '../utils/geolocation';

const emptySettings: AdminRuntimeSettings = {
  course_sign_webhook_url: '',
  qmx_auto_sign_webhook_url: '',
  qmx_location_presets: [],
  course_location_presets: [],
};

const newQMXPreset = (): QMXLocationPreset => ({
  name: '',
  lng: 0,
  lat: 0,
  range: 400,
});

const newCoursePreset = (): CourseLocationPreset => ({
  name: '',
  lng: '',
  lat: '',
  description: '',
});

const getErrorMessage = (error: unknown, fallback: string) => (
  error instanceof Error ? error.message : fallback
);

const toNumber = (value: string) => {
  const next = Number(value);
  return Number.isFinite(next) ? next : 0;
};

const normalizeSettings = (settings?: Partial<AdminRuntimeSettings>): AdminRuntimeSettings => {
  const data = settings || {};
  return {
    ...emptySettings,
    ...data,
    qmx_location_presets: data.qmx_location_presets || [],
    course_location_presets: data.course_location_presets || [],
  };
};

const AdminSettings = () => {
  const [settings, setSettings] = useState<AdminRuntimeSettings>(emptySettings);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [locatingCourseIndex, setLocatingCourseIndex] = useState<number | null>(null);

  const loadSettings = useCallback(async () => {
    setIsLoading(true);
    try {
      const response = await client.get<ApiResponse<AdminRuntimeSettings>>('/admin/settings');
      setSettings(normalizeSettings(response.data.data));
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取后台设置失败'));
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadSettings();
  }, [loadSettings]);

  const saveSettings = async () => {
    setIsSaving(true);
    try {
      const response = await client.put<ApiResponse<AdminRuntimeSettings>>('/admin/settings', settings);
      setSettings(normalizeSettings(response.data.data));
      toast.success('后台设置已保存');
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '保存后台设置失败'));
    } finally {
      setIsSaving(false);
    }
  };

  const updateQMXPreset = (index: number, patch: Partial<QMXLocationPreset>) => {
    setSettings((current) => ({
      ...current,
      qmx_location_presets: current.qmx_location_presets.map((preset, itemIndex) => (
        itemIndex === index ? { ...preset, ...patch } : preset
      )),
    }));
  };

  const addQMXPreset = () => {
    setSettings((current) => ({
      ...current,
      qmx_location_presets: [...current.qmx_location_presets, newQMXPreset()],
    }));
  };

  const removeQMXPreset = (index: number) => {
    setSettings((current) => ({
      ...current,
      qmx_location_presets: current.qmx_location_presets.filter((_, itemIndex) => itemIndex !== index),
    }));
  };

  const updateCoursePreset = (index: number, patch: Partial<CourseLocationPreset>) => {
    setSettings((current) => ({
      ...current,
      course_location_presets: current.course_location_presets.map((preset, itemIndex) => (
        itemIndex === index ? { ...preset, ...patch } : preset
      )),
    }));
  };

  const addCoursePreset = () => {
    setSettings((current) => ({
      ...current,
      course_location_presets: [...current.course_location_presets, newCoursePreset()],
    }));
  };

  const removeCoursePreset = (index: number) => {
    setSettings((current) => ({
      ...current,
      course_location_presets: current.course_location_presets.filter((_, itemIndex) => itemIndex !== index),
    }));
  };

  const fillCoursePresetFromBrowser = async (index: number) => {
    if (locatingCourseIndex !== null) return;
    setLocatingCourseIndex(index);
    try {
      const current = await getBrowserLocation();
      setSettings((settingsNow) => ({
        ...settingsNow,
        course_location_presets: settingsNow.course_location_presets.map((preset, itemIndex) => (
          itemIndex === index
            ? {
              ...preset,
              name: preset.name.trim() || '浏览器当前位置',
              lng: current.lng,
              lat: current.lat,
              description: current.description,
            }
            : preset
        )),
      }));
      toast.success(`已获取当前位置，精度约 ${current.accuracy} 米`);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取当前位置失败'));
    } finally {
      setLocatingCourseIndex(null);
    }
  };

  return (
    <AdminShell
      title="后台设置"
      subtitle="推送通知与定位点"
      action={(
        <button
          type="button"
          onClick={saveSettings}
          disabled={isLoading || isSaving}
          className="h-10 w-10 rounded-xl bg-blue-600 text-white flex items-center justify-center disabled:opacity-50"
          title="保存"
        >
          {isSaving ? <Loader2 size={18} className="animate-spin" /> : <Save size={18} />}
        </button>
      )}
    >
      <div className="flex-1 min-h-0 overflow-y-auto p-4 pb-[calc(24px+var(--sab))] space-y-4 custom-scrollbar">
        {isLoading ? (
          <div className="space-y-3">
            <div className="h-36 rounded-[1.75rem] bg-white animate-pulse" />
            <div className="h-40 rounded-[1.75rem] bg-white animate-pulse" />
            <div className="h-72 rounded-[1.75rem] bg-white animate-pulse" />
          </div>
        ) : (
          <>
            <section className="rounded-[1.75rem] bg-slate-950 text-white p-4">
              <div className="flex items-center gap-2">
                <Settings2 size={18} className="text-cyan-200" />
                <p className="font-black">当前配置</p>
              </div>
              <div className="mt-3 grid grid-cols-2 gap-2 text-center">
                <div className="rounded-2xl bg-white/10 p-3">
                  <p className="text-[10px] font-bold text-slate-400">课程推送</p>
                  <p className="mt-1 text-sm font-black">{settings.course_sign_webhook_url ? '已配置' : '关闭'}</p>
                </div>
                <div className="rounded-2xl bg-white/10 p-3">
                  <p className="text-[10px] font-bold text-slate-400">QMX 推送</p>
                  <p className="mt-1 text-sm font-black">{settings.qmx_auto_sign_webhook_url ? '已配置' : '关闭'}</p>
                </div>
                <div className="rounded-2xl bg-white/10 p-3">
                  <p className="text-[10px] font-bold text-slate-400">课程定位点</p>
                  <p className="mt-1 text-sm font-black">{settings.course_location_presets.length}</p>
                </div>
                <div className="rounded-2xl bg-white/10 p-3">
                  <p className="text-[10px] font-bold text-slate-400">QMX 定位点</p>
                  <p className="mt-1 text-sm font-black">{settings.qmx_location_presets.length}</p>
                </div>
              </div>
            </section>

            <section className="rounded-[1.75rem] bg-white border border-slate-100 shadow-sm p-4">
              <div className="flex items-center gap-2 mb-4">
                <Bell size={18} className="text-blue-600" />
                <h3 className="font-black text-slate-900">推送通知</h3>
              </div>

              <div className="space-y-3">
                <label className="block">
                  <span className="text-xs font-bold text-slate-500">课程签到 webhook</span>
                  <input
                    type="url"
                    value={settings.course_sign_webhook_url}
                    onChange={(event) => setSettings((current) => ({ ...current, course_sign_webhook_url: event.target.value }))}
                    className="mt-1 w-full h-12 rounded-2xl border border-slate-100 bg-slate-50 px-3 text-sm outline-none focus:ring-2 focus:ring-blue-500"
                    placeholder="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."
                  />
                </label>
                <label className="block">
                  <span className="text-xs font-bold text-slate-500">QMX 自动签到 webhook</span>
                  <input
                    type="url"
                    value={settings.qmx_auto_sign_webhook_url}
                    onChange={(event) => setSettings((current) => ({ ...current, qmx_auto_sign_webhook_url: event.target.value }))}
                    className="mt-1 w-full h-12 rounded-2xl border border-slate-100 bg-slate-50 px-3 text-sm outline-none focus:ring-2 focus:ring-blue-500"
                    placeholder="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."
                  />
                </label>
              </div>
            </section>

            <section className="rounded-[1.75rem] bg-white border border-slate-100 shadow-sm p-4">
              <div className="flex items-center justify-between mb-4">
                <div className="flex items-center gap-2">
                  <BookOpen size={18} className="text-blue-600" />
                  <h3 className="font-black text-slate-900">课程定位签到定位点</h3>
                </div>
                <button
                  type="button"
                  onClick={addCoursePreset}
                  className="h-9 w-9 rounded-xl bg-slate-900 text-white flex items-center justify-center"
                  title="添加定位点"
                >
                  <Plus size={17} />
                </button>
              </div>

              {settings.course_location_presets.length === 0 ? (
                <div className="rounded-2xl bg-slate-50 py-10 text-center text-sm font-semibold text-slate-400">
                  暂无课程定位点
                </div>
              ) : (
                <div className="space-y-3">
                  {settings.course_location_presets.map((preset, index) => (
                    <div key={`${preset.name}-${index}`} className="rounded-2xl border border-slate-100 bg-slate-50/70 p-3">
                      <div className="flex items-center gap-2">
                        <div className="w-9 h-9 rounded-xl bg-blue-50 text-blue-600 flex items-center justify-center shrink-0">
                          <BookOpen size={17} />
                        </div>
                        <input
                          value={preset.name}
                          onChange={(event) => updateCoursePreset(index, { name: event.target.value })}
                          className="min-w-0 flex-1 h-10 rounded-xl border border-slate-100 bg-white px-3 text-sm font-bold outline-none focus:ring-2 focus:ring-blue-500"
                          placeholder="定位点名称"
                        />
                        <button
                          type="button"
                          onClick={() => fillCoursePresetFromBrowser(index)}
                          disabled={locatingCourseIndex !== null}
                          className="h-9 w-9 rounded-xl bg-blue-50 text-blue-600 flex items-center justify-center shrink-0 disabled:opacity-50"
                          title="使用当前位置"
                        >
                          {locatingCourseIndex === index ? <Loader2 size={16} className="animate-spin" /> : <MapPin size={16} />}
                        </button>
                        <button
                          type="button"
                          onClick={() => removeCoursePreset(index)}
                          className="h-9 w-9 rounded-xl bg-rose-50 text-rose-600 flex items-center justify-center shrink-0"
                          title="删除定位点"
                        >
                          <Trash2 size={16} />
                        </button>
                      </div>

                      <div className="mt-3 grid grid-cols-2 gap-2">
                        <label className="block min-w-0">
                          <span className="text-[10px] font-bold text-slate-400">经度</span>
                          <input
                            type="number"
                            step="0.000001"
                            value={preset.lng}
                            onChange={(event) => updateCoursePreset(index, { lng: event.target.value })}
                            className="mt-1 w-full h-10 rounded-xl border border-slate-100 bg-white px-2 text-xs outline-none focus:ring-2 focus:ring-blue-500"
                          />
                        </label>
                        <label className="block min-w-0">
                          <span className="text-[10px] font-bold text-slate-400">纬度</span>
                          <input
                            type="number"
                            step="0.000001"
                            value={preset.lat}
                            onChange={(event) => updateCoursePreset(index, { lat: event.target.value })}
                            className="mt-1 w-full h-10 rounded-xl border border-slate-100 bg-white px-2 text-xs outline-none focus:ring-2 focus:ring-blue-500"
                          />
                        </label>
                      </div>

                      <label className="mt-3 block">
                        <span className="text-[10px] font-bold text-slate-400">描述</span>
                        <input
                          value={preset.description}
                          onChange={(event) => updateCoursePreset(index, { description: event.target.value })}
                          className="mt-1 w-full h-10 rounded-xl border border-slate-100 bg-white px-3 text-xs outline-none focus:ring-2 focus:ring-blue-500"
                          placeholder="中国山东省日照市东港区秦楼街道"
                        />
                      </label>
                    </div>
                  ))}
                </div>
              )}
            </section>

            <section className="rounded-[1.75rem] bg-white border border-slate-100 shadow-sm p-4">
              <div className="flex items-center justify-between mb-4">
                <div className="flex items-center gap-2">
                  <MapPin size={18} className="text-cyan-600" />
                  <h3 className="font-black text-slate-900">QMX 预设定位点</h3>
                </div>
                <button
                  type="button"
                  onClick={addQMXPreset}
                  className="h-9 w-9 rounded-xl bg-slate-900 text-white flex items-center justify-center"
                  title="添加定位点"
                >
                  <Plus size={17} />
                </button>
              </div>

              {settings.qmx_location_presets.length === 0 ? (
                <div className="rounded-2xl bg-slate-50 py-10 text-center text-sm font-semibold text-slate-400">
                  暂无 QMX 预设定位点
                </div>
              ) : (
                <div className="space-y-3">
                  {settings.qmx_location_presets.map((preset, index) => (
                    <div key={`${preset.name}-${index}`} className="rounded-2xl border border-slate-100 bg-slate-50/70 p-3">
                      <div className="flex items-center gap-2">
                        <div className="w-9 h-9 rounded-xl bg-cyan-50 text-cyan-600 flex items-center justify-center shrink-0">
                          <MapPin size={17} />
                        </div>
                        <input
                          value={preset.name}
                          onChange={(event) => updateQMXPreset(index, { name: event.target.value })}
                          className="min-w-0 flex-1 h-10 rounded-xl border border-slate-100 bg-white px-3 text-sm font-bold outline-none focus:ring-2 focus:ring-cyan-500"
                          placeholder="定位点名称"
                        />
                        <button
                          type="button"
                          onClick={() => removeQMXPreset(index)}
                          className="h-9 w-9 rounded-xl bg-rose-50 text-rose-600 flex items-center justify-center shrink-0"
                          title="删除定位点"
                        >
                          <Trash2 size={16} />
                        </button>
                      </div>

                      <div className="mt-3 grid grid-cols-3 gap-2">
                        <label className="block min-w-0">
                          <span className="text-[10px] font-bold text-slate-400">经度</span>
                          <input
                            type="number"
                            step="0.00000000000001"
                            value={preset.lng}
                            onChange={(event) => updateQMXPreset(index, { lng: toNumber(event.target.value) })}
                            className="mt-1 w-full h-10 rounded-xl border border-slate-100 bg-white px-2 text-xs outline-none focus:ring-2 focus:ring-cyan-500"
                          />
                        </label>
                        <label className="block min-w-0">
                          <span className="text-[10px] font-bold text-slate-400">纬度</span>
                          <input
                            type="number"
                            step="0.00000000000001"
                            value={preset.lat}
                            onChange={(event) => updateQMXPreset(index, { lat: toNumber(event.target.value) })}
                            className="mt-1 w-full h-10 rounded-xl border border-slate-100 bg-white px-2 text-xs outline-none focus:ring-2 focus:ring-cyan-500"
                          />
                        </label>
                        <label className="block min-w-0">
                          <span className="text-[10px] font-bold text-slate-400">范围</span>
                          <input
                            type="number"
                            min={1}
                            max={5000}
                            value={preset.range}
                            onChange={(event) => updateQMXPreset(index, { range: Math.max(0, Math.round(toNumber(event.target.value))) })}
                            className="mt-1 w-full h-10 rounded-xl border border-slate-100 bg-white px-2 text-xs outline-none focus:ring-2 focus:ring-cyan-500"
                          />
                        </label>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </section>
          </>
        )}
      </div>
    </AdminShell>
  );
};

export default AdminSettings;
