import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { motion, useIsPresent } from 'framer-motion';
import { CheckCircle2, Loader2, Plug, Save, X } from 'lucide-react';
import toast from 'react-hot-toast';
import axios from 'axios';
import client, { withAccount, type RequestAccount } from '../../api/client';
import { useAuthStore } from '../../store/auth';
import type { ApiResponse, VikunjaProject, VikunjaSettings } from '../../types';

interface VikunjaSettingsForm {
  enabled: boolean;
  api_token: string;
  project_id: number;
}

const emptyForm: VikunjaSettingsForm = {
  enabled: false,
  api_token: '',
  project_id: 0
};

const VikunjaSettingsModal = ({ owner, onClose, onSync, isSyncing }: {
  owner: RequestAccount;
  onClose: () => void;
  onSync: () => Promise<void>;
  isSyncing: boolean;
}) => {
  const isPresent = useIsPresent();
  const [form, setForm] = useState<VikunjaSettingsForm>(emptyForm);
  const [settings, setSettings] = useState<VikunjaSettings | null>(null);
  const [projects, setProjects] = useState<VikunjaProject[]>([]);
  const [testedToken, setTestedToken] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isTesting, setIsTesting] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [isDirty, setIsDirty] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [connectionError, setConnectionError] = useState('');
  const mountedRef = useRef(false);
  const loadControllerRef = useRef<AbortController | null>(null);
  const loadRequestIdRef = useRef(0);
  const testControllerRef = useRef<AbortController | null>(null);
  const testRequestIdRef = useRef(0);
  const saveControllerRef = useRef<AbortController | null>(null);
  const saveRequestIdRef = useRef(0);
  const formVersionRef = useRef(0);
  const savedVersionRef = useRef(-1);

  const ownsDialog = useCallback(() => {
    const state = useAuthStore.getState();
    return mountedRef.current && state.activeUid === owner.uid && state.token === owner.token;
  }, [owner]);

  const invalidateRequests = useCallback(() => {
    mountedRef.current = false;
    loadControllerRef.current?.abort();
    testControllerRef.current?.abort();
    saveControllerRef.current?.abort();
    loadControllerRef.current = null;
    testControllerRef.current = null;
    saveControllerRef.current = null;
    loadRequestIdRef.current += 1;
    testRequestIdRef.current += 1;
    saveRequestIdRef.current += 1;
  }, []);

  useLayoutEffect(() => {
    mountedRef.current = isPresent && owner.uid > 0 && !!owner.token;
    return invalidateRequests;
  }, [invalidateRequests, isPresent, owner]);

  const applySettings = useCallback((next: VikunjaSettings) => {
    setSettings(next);
    setForm({ enabled: next.enabled, api_token: '', project_id: next.project_id || 0 });
    setProjects(next.token_configured && next.project_id > 0
      ? [{ id: next.project_id, title: next.project_title }]
      : []);
    setTestedToken(next.token_configured ? '' : null);
    savedVersionRef.current = formVersionRef.current;
    setIsDirty(false);
    setConnectionError('');
  }, []);

  const loadSettings = useCallback(async () => {
    if (!ownsDialog()) return;
    loadControllerRef.current?.abort();
    const controller = new AbortController();
    const requestId = ++loadRequestIdRef.current;
    const formVersion = formVersionRef.current;
    loadControllerRef.current = controller;
    const isCurrent = () => ownsDialog() && !controller.signal.aborted &&
      loadRequestIdRef.current === requestId && loadControllerRef.current === controller &&
      formVersionRef.current === formVersion;
    try {
      const response = await client.get<ApiResponse<VikunjaSettings>>(
        '/vikunja/settings', withAccount(owner, controller.signal)
      );
      if (!isCurrent()) return;
      applySettings(response.data.data);
      setLoadError('');
    } catch (error: unknown) {
      if (!isCurrent() || axios.isCancel(error)) return;
      const message = error instanceof Error ? error.message : '加载设置失败';
      setLoadError(message);
      toast.error(message);
    } finally {
      if (isCurrent()) {
        loadControllerRef.current = null;
        setIsLoading(false);
      }
    }
  }, [applySettings, owner, ownsDialog]);

  useEffect(() => {
    if (isPresent) void loadSettings();
  }, [isPresent, loadSettings]);

  const reloadSettings = async () => {
    if (!ownsDialog() || loadControllerRef.current) return;
    setIsLoading(true);
    await loadSettings();
  };

  const close = () => {
    if (!ownsDialog()) return;
    invalidateRequests();
    onClose();
  };

  const update = (patch: Partial<VikunjaSettingsForm>) => {
    if (!ownsDialog() || isLoading || !settings) return;
    formVersionRef.current += 1;
    testControllerRef.current?.abort();
    saveControllerRef.current?.abort();
    testControllerRef.current = null;
    saveControllerRef.current = null;
    testRequestIdRef.current += 1;
    saveRequestIdRef.current += 1;
    setIsTesting(false);
    setIsSaving(false);
    setIsDirty(true);
    setConnectionError('');
    if (patch.api_token !== undefined) {
      setProjects([]);
      setTestedToken(null);
      setForm(previous => ({ ...previous, ...patch, project_id: 0 }));
    } else {
      setForm(previous => ({ ...previous, ...patch }));
    }
  };

  const selectedProject = projects.find(project => project.id === form.project_id);
  const closingOnly = !form.enabled && !form.api_token && form.project_id === 0;
  const selectionVerified = !!selectedProject && testedToken === form.api_token;
  const canSave = !!settings && !isLoading && !isTesting && !isSaving &&
    (closingOnly || (!!settings.base_url && selectionVerified));
  const canSync = !!settings?.base_url && settings.enabled && settings.token_configured && settings.project_id > 0 &&
    !isDirty && !isLoading && !isTesting && !isSaving && !isSyncing;

  const testConnection = async () => {
    if (!ownsDialog() || isLoading || !settings || testControllerRef.current || saveControllerRef.current) return;
    if (!settings.base_url) {
      toast.error('服务端未配置 Vikunja 地址');
      return;
    }
    if (!form.api_token && !settings.token_configured) {
      toast.error('请重新填写 API Token');
      return;
    }
    const controller = new AbortController();
    const requestId = ++testRequestIdRef.current;
    const formVersion = formVersionRef.current;
    testControllerRef.current = controller;
    const isCurrent = () => ownsDialog() && !controller.signal.aborted &&
      testRequestIdRef.current === requestId && testControllerRef.current === controller &&
      formVersionRef.current === formVersion;
    setIsTesting(true);
    setConnectionError('');
    try {
      const response = await client.post<ApiResponse<{ projects: VikunjaProject[] }>>(
        '/vikunja/test', { api_token: form.api_token || undefined }, withAccount(owner, controller.signal)
      );
      if (!isCurrent()) return;
      const list = response.data.data.projects || [];
      setProjects(list);
      setTestedToken(form.api_token);
      if (form.project_id > 0 && !list.some(project => project.id === form.project_id)) {
        setForm(previous => ({ ...previous, project_id: 0 }));
        savedVersionRef.current = -1;
        setIsDirty(true);
      }
      toast.success(`连接成功，可选项目 ${list.length} 个`);
    } catch (error: unknown) {
      if (!isCurrent() || axios.isCancel(error)) return;
      const message = error instanceof Error ? error.message : '连接失败';
      setConnectionError(message);
      toast.error(message);
    } finally {
      if (isCurrent()) {
        testControllerRef.current = null;
        setIsTesting(false);
      }
    }
  };

  const save = async () => {
    if (!ownsDialog() || isLoading || !settings || testControllerRef.current || saveControllerRef.current) return;
    if (!canSave) {
      toast.error(settings.base_url ? '请先测试当前 Token 并选择项目' : '服务端未配置 Vikunja 地址，仅可关闭同步');
      return;
    }
    const controller = new AbortController();
    const requestId = ++saveRequestIdRef.current;
    const formVersion = ++formVersionRef.current;
    saveControllerRef.current = controller;
    const isCurrent = () => ownsDialog() && !controller.signal.aborted &&
      saveRequestIdRef.current === requestId && saveControllerRef.current === controller &&
      formVersionRef.current === formVersion;
    setIsSaving(true);
    setIsDirty(true);
    setConnectionError('');
    try {
      const response = await client.put<ApiResponse<VikunjaSettings>>('/vikunja/settings', {
        enabled: form.enabled,
        api_token: form.api_token || undefined,
        project_id: form.project_id
      }, withAccount(owner, controller.signal));
      if (!isCurrent()) return;
      applySettings(response.data.data);
      toast.success('设置已保存');
    } catch (error: unknown) {
      if (!isCurrent() || axios.isCancel(error)) return;
      const message = error instanceof Error ? error.message : '保存失败';
      setConnectionError(message);
      toast.error(message);
    } finally {
      if (isCurrent()) {
        saveControllerRef.current = null;
        setIsSaving(false);
      }
    }
  };

  const handleSync = async () => {
    if (!ownsDialog() || !canSync || savedVersionRef.current !== formVersionRef.current ||
      loadControllerRef.current || testControllerRef.current || saveControllerRef.current) return;
    close();
    await onSync();
  };

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/60 backdrop-blur-md"
      onClick={close}
    >
      <motion.div
        initial={{ y: '100%' }}
        animate={{ y: 0 }}
        exit={{ y: '100%' }}
        transition={{ type: 'spring', damping: 28, stiffness: 260 }}
        className="w-full max-w-[480px] bg-white rounded-t-[2rem] p-6 pb-[calc(24px+var(--sab))] shadow-2xl max-h-[88vh] overflow-y-auto"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="w-12 h-1.5 rounded-full bg-slate-200 mx-auto mb-5" />
        <div className="flex items-center justify-between mb-5">
          <h3 className="text-lg font-black text-slate-900">Vikunja 同步设置</h3>
          <button onClick={close} className="w-9 h-9 rounded-full bg-slate-100 text-slate-500 flex items-center justify-center">
            <X size={18} />
          </button>
        </div>

        {isLoading ? (
          <div className="py-10 flex items-center justify-center text-slate-400">
            <Loader2 size={26} className="animate-spin" />
          </div>
        ) : loadError || !settings ? (
          <div className="py-6 text-center text-sm text-slate-500">
            <p>{loadError || '加载设置失败'}</p>
            <button onClick={() => void reloadSettings()} className="mt-4 text-blue-600 font-bold">重新加载设置</button>
          </div>
        ) : (
          <div className="space-y-4">
            <button
              onClick={() => update({ enabled: !form.enabled })}
              disabled={!settings.base_url && !form.enabled}
              className="w-full flex items-center justify-between rounded-2xl border border-slate-100 bg-slate-50 px-4 py-3 disabled:opacity-50"
            >
              <span className="text-sm font-bold text-slate-700">启用每日自动同步</span>
              <span className={`w-11 h-6 rounded-full relative transition-colors ${form.enabled ? 'bg-blue-600' : 'bg-slate-300'}`}>
                <span className={`absolute top-0.5 w-5 h-5 rounded-full bg-white shadow transition-all ${form.enabled ? 'left-[22px]' : 'left-0.5'}`} />
              </span>
            </button>

            <div>
              <div className="text-xs font-bold text-slate-500 px-1">Vikunja 地址（由服务器配置）</div>
              <div className="mt-1 w-full px-4 py-3 rounded-2xl border border-slate-100 bg-slate-50 text-sm text-slate-700 break-all">
                {settings.base_url || '服务端未配置 Vikunja 地址'}
              </div>
              {!settings.base_url ? (
                <p className="mt-2 px-1 text-xs text-amber-700">请联系管理员配置实例地址；当前仅可关闭同步。</p>
              ) : !settings.token_configured && (
                <p className="mt-2 px-1 text-xs text-amber-700">尚未绑定可用凭据或服务器实例已变化，请重新填写 Token、测试连接并选择项目后保存。</p>
              )}
            </div>

            <div>
              <label className="text-xs font-bold text-slate-500 px-1">
                API Token{settings.token_configured && <span className="ml-1 text-emerald-600 font-normal">（{settings.token_mask || '已配置'}）</span>}
              </label>
              <input
                value={form.api_token}
                onChange={(e) => update({ api_token: e.target.value.trim() })}
                placeholder={settings.token_configured ? '留空表示不修改' : '在 Vikunja 设置 → API Tokens 中创建'}
                type="password"
                disabled={!settings.base_url}
                className="mt-1 w-full h-12 px-4 rounded-2xl border border-slate-100 bg-slate-50 text-sm text-slate-900 placeholder:text-slate-400 focus:outline-none focus:border-blue-200 disabled:opacity-50"
              />
            </div>

            <button
              onClick={() => void testConnection()}
              disabled={isTesting || isSaving || !settings.base_url || (!form.api_token && !settings.token_configured)}
              className="w-full h-11 rounded-2xl bg-slate-900 text-white text-sm font-black flex items-center justify-center gap-2 disabled:opacity-50"
            >
              {isTesting ? <Loader2 size={16} className="animate-spin" /> : <Plug size={16} />}
              测试连接并获取项目
            </button>

            <div>
              <label className="text-xs font-bold text-slate-500 px-1">目标项目</label>
              {projects.length > 0 ? (
                <select
                  value={form.project_id}
                  onChange={(e) => update({ project_id: Number(e.target.value) })}
                  className="mt-1 w-full h-12 px-4 rounded-2xl border border-slate-100 bg-slate-50 text-sm text-slate-900 focus:outline-none focus:border-blue-200"
                >
                  <option value={0}>请选择项目</option>
                  {projects.map(project => (
                    <option key={project.id} value={project.id}>{project.title}</option>
                  ))}
                </select>
              ) : (
                <div className="mt-1 w-full h-12 px-4 rounded-2xl border border-dashed border-slate-200 bg-slate-50 flex items-center text-sm text-slate-400">
                  测试连接后在这里选择项目
                </div>
              )}
              {selectedProject && form.project_id > 0 && (
                <div className="mt-1.5 px-1 text-xs text-emerald-600 flex items-center gap-1">
                  <CheckCircle2 size={13} /> 已选择「{selectedProject.title}」
                </div>
              )}
            </div>

            {connectionError && <p role="alert" className="px-1 text-xs text-red-600">{connectionError}</p>}
            <div className="rounded-2xl bg-slate-50 border border-slate-100 px-4 py-3 text-xs text-slate-500 leading-5">
              每日 07:00 自动把未提交的作业同步到所选项目，作业按课程自动打标签。当前账号：
              {settings.last_sync_at ? (
                <>
                  <div className="mt-1 text-slate-600 font-bold">{settings.last_sync_message || '—'}</div>
                  <div className="mt-0.5">上次同步：{new Date(settings.last_sync_at).toLocaleString('zh-CN')}</div>
                </>
              ) : (
                <div className="mt-1">尚未同步过</div>
              )}
            </div>

            {isDirty && <p className="px-1 text-xs text-amber-700">有未保存的修改，请保存后再同步。</p>}
            <div className="flex gap-2">
              <button
                onClick={() => void save()}
                disabled={!canSave}
                className="flex-1 h-12 rounded-2xl bg-blue-600 text-white text-sm font-black flex items-center justify-center gap-2 disabled:opacity-50"
              >
                {isSaving ? <Loader2 size={16} className="animate-spin" /> : <Save size={16} />}
                保存
              </button>
              <button
                onClick={() => void handleSync()}
                disabled={!canSync}
                title="启用同步并保存后可用"
                className="flex-1 h-12 rounded-2xl bg-slate-900 text-white text-sm font-black flex items-center justify-center gap-2 disabled:opacity-50"
              >
                {isSyncing && <Loader2 size={16} className="animate-spin" />}
                立即同步
              </button>
            </div>
          </div>
        )}
      </motion.div>
    </motion.div>
  );
};

export default VikunjaSettingsModal;
