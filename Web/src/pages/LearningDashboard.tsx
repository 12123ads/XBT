import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AnimatePresence, motion } from 'framer-motion';
import {
  AlertTriangle,
  BookOpen,
  CheckCircle2,
  ChevronLeft,
  ExternalLink,
  FileText,
  GraduationCap,
  Loader2,
  ListChecks,
  RefreshCw,
  RotateCcw,
  Settings,
  Timer,
  X
} from 'lucide-react';
import toast from 'react-hot-toast';
import axios from 'axios';
import client, { withAccount, type RequestAccount } from '../api/client';
import CaptchaModal from '../components/learning/CaptchaModal';
import VikunjaSettingsModal from '../components/learning/VikunjaSettingsModal';
import PullToRefresh from '../components/PullToRefresh';
import { useAuthStore } from '../store/auth';
import type { ApiResponse, LearningDashboard as LearningDashboardData, LearningItem, VikunjaSyncResult } from '../types';

type ViewKey = 'todo' | 'homework' | 'exams' | 'activities';
type StatusFilter = 'all' | 'active' | 'ended' | 'finished';
type CaptchaAction = 'dashboard' | 'vikunja';
type CaptchaChallenge = { id: number; owner: RequestAccount };

const emptyDashboard: LearningDashboardData = {
  todo: [],
  homework: [],
  exams: [],
  activities: [],
  errors: []
};

const views: Array<{ key: ViewKey; label: string; icon: typeof Timer }> = [
  { key: 'todo', label: '待办', icon: Timer },
  { key: 'homework', label: '作业', icon: FileText },
  { key: 'exams', label: '考试', icon: GraduationCap },
  { key: 'activities', label: '课程任务', icon: BookOpen }
];

const statusFilters: Array<{ key: StatusFilter; label: string }> = [
  { key: 'all', label: '全部' },
  { key: 'active', label: '进行中' },
  { key: 'ended', label: '已结束' },
  { key: 'finished', label: '已完成' }
];

const itemKey = (item: LearningItem) => `${item.kind}:${item.id || item.title}:${item.course_id}:${item.class_id}`;

const isActiveItem = (item: LearningItem) => (item.ongoing || item.pending) && !item.finished && !item.expired;

const isEndedItem = (item: LearningItem) => item.expired || (!isActiveItem(item) && !item.finished && item.status?.includes('已结束'));

const itemStatusRank = (item: LearningItem) => {
  if (isActiveItem(item)) return 0;
  if (isEndedItem(item)) return 1;
  if (item.finished) return 2;
  return 3;
};

const itemSortTime = (item: LearningItem) => {
  if (item.end_time > 0) return item.end_time;
  if (item.start_time > 0) return item.start_time;
  return Number.MAX_SAFE_INTEGER;
};

const sortLearningItems = (items: LearningItem[]) => {
  return [...items].sort((a, b) => {
    const rankDiff = itemStatusRank(a) - itemStatusRank(b);
    if (rankDiff !== 0) return rankDiff;

    const timeDiff = itemSortTime(a) - itemSortTime(b);
    if (timeDiff !== 0) return timeDiff;

    const courseDiff = (a.course_name || '').localeCompare(b.course_name || '', 'zh-CN');
    if (courseDiff !== 0) return courseDiff;
    return (a.title || '').localeCompare(b.title || '', 'zh-CN');
  });
};

const filterByStatus = (items: LearningItem[], filter: StatusFilter) => {
  switch (filter) {
    case 'active':
      return items.filter(isActiveItem);
    case 'ended':
      return items.filter(isEndedItem);
    case 'finished':
      return items.filter(item => item.finished);
    default:
      return items;
  }
};

const statusClass = (item: LearningItem) => {
  if (isActiveItem(item)) return 'bg-blue-50 text-blue-700 border-blue-100';
  if (isEndedItem(item)) return 'bg-slate-50 text-slate-600 border-slate-100';
  if (item.finished) return 'bg-emerald-50 text-emerald-700 border-emerald-100';
  return 'bg-amber-50 text-amber-700 border-amber-100';
};

const kindAccent = (kind: string) => {
  switch (kind) {
    case 'homework':
      return 'text-amber-600 bg-amber-50';
    case 'exam':
      return 'text-red-600 bg-red-50';
    case 'activity':
      return 'text-blue-600 bg-blue-50';
    default:
      return 'text-slate-600 bg-slate-50';
  }
};

const LearningDashboard = () => {
  const navigate = useNavigate();
  const { activeUid, token } = useAuthStore();
  const owner = useMemo<RequestAccount>(() => ({ uid: activeUid ?? 0, token: token ?? '' }), [activeUid, token]);
  const [data, setData] = useState<LearningDashboardData>(emptyDashboard);
  const [activeView, setActiveView] = useState<ViewKey>('todo');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [isLoading, setIsLoading] = useState(true);
  const [captchaChallenge, setCaptchaChallenge] = useState<CaptchaChallenge | null>(null);
  const [isSyncing, setIsSyncing] = useState(false);
  const [settingsCycle, setSettingsCycle] = useState<number | null>(null);
  const [showIgnored, setShowIgnored] = useState(false);
  const mountedRef = useRef(false);
  const dashboardControllerRef = useRef<AbortController | null>(null);
  const dashboardRequestIdRef = useRef(0);
  const syncControllerRef = useRef<AbortController | null>(null);
  const syncRequestIdRef = useRef(0);
  const captchaActionsRef = useRef(new Set<CaptchaAction>());
  const challengeIdRef = useRef(0);
  const challengeRef = useRef<CaptchaChallenge | null>(null);
  const settingsCycleIdRef = useRef(0);
  const ignoredUidRef = useRef(activeUid);
  const [ignored, setIgnored] = useState<Record<string, LearningItem>>(() => {
    const raw = localStorage.getItem(`learning_ignored_${activeUid || 'default'}`);
    if (!raw) return {};
    try {
      return JSON.parse(raw);
    } catch {
      return {};
    }
  });

  const ownsPage = useCallback(() => {
    const state = useAuthStore.getState();
    return mountedRef.current && owner.uid > 0 && !!owner.token &&
      state.activeUid === owner.uid && state.token === owner.token;
  }, [owner]);

  useLayoutEffect(() => {
    mountedRef.current = true;
    const captchaActions = captchaActionsRef.current;
    return () => {
      mountedRef.current = false;
      dashboardControllerRef.current?.abort();
      syncControllerRef.current?.abort();
      dashboardRequestIdRef.current += 1;
      syncRequestIdRef.current += 1;
      captchaActions.clear();
      challengeRef.current = null;
    };
  }, [owner]);

  useEffect(() => {
    if (!ownsPage()) return;
    localStorage.setItem(`learning_ignored_${ignoredUidRef.current || 'default'}`, JSON.stringify(ignored));
  }, [ignored, ownsPage]);

  const requireCaptcha = useCallback((action: CaptchaAction) => {
    if (!ownsPage()) return;
    captchaActionsRef.current.add(action);
    if (challengeRef.current) return;
    const challenge = { id: ++challengeIdRef.current, owner };
    challengeRef.current = challenge;
    setCaptchaChallenge(challenge);
  }, [owner, ownsPage]);

  const completeCaptchaAction = useCallback((action: CaptchaAction) => {
    if (!ownsPage()) return;
    captchaActionsRef.current.delete(action);
    if (challengeRef.current && captchaActionsRef.current.size === 0) {
      challengeRef.current = null;
      setCaptchaChallenge(null);
    }
  }, [ownsPage]);

  const loadDashboard = useCallback(async () => {
    if (!ownsPage()) return;
    dashboardControllerRef.current?.abort();
    const controller = new AbortController();
    const requestId = ++dashboardRequestIdRef.current;
    dashboardControllerRef.current = controller;
    const isCurrent = () => ownsPage() && !controller.signal.aborted &&
      dashboardRequestIdRef.current === requestId && dashboardControllerRef.current === controller;
    try {
      const response = await client.get<ApiResponse<LearningDashboardData>>(
        '/learning/dashboard', withAccount(owner, controller.signal)
      );
      if (!isCurrent()) return;
      const next = response.data.data || emptyDashboard;
      setData({
        todo: next.todo || [],
        homework: next.homework || [],
        exams: next.exams || [],
        activities: next.activities || [],
        errors: next.errors || []
      });
      completeCaptchaAction('dashboard');
      if (next.errors?.length) {
        toast.error(`部分数据获取失败：${next.errors.length} 项`);
      }
    } catch (error: unknown) {
      if (!isCurrent() || axios.isCancel(error)) return;
      if (axios.isAxiosError<ApiResponse<unknown>>(error) && error.response?.data.code === 4301) {
        requireCaptcha('dashboard');
        return;
      }
      toast.error(error instanceof Error ? error.message : '获取学习仪表盘失败');
    } finally {
      if (isCurrent()) {
        dashboardControllerRef.current = null;
        setIsLoading(false);
      }
    }
  }, [owner, ownsPage, requireCaptcha, completeCaptchaAction]);

  const fetchDashboard = useCallback(async () => {
    if (!ownsPage()) return;
    setIsLoading(true);
    await loadDashboard();
  }, [loadDashboard, ownsPage]);

  const syncToVikunja = useCallback(async () => {
    if (!ownsPage()) return;
    syncControllerRef.current?.abort();
    const controller = new AbortController();
    const requestId = ++syncRequestIdRef.current;
    syncControllerRef.current = controller;
    const isCurrent = () => ownsPage() && !controller.signal.aborted &&
      syncRequestIdRef.current === requestId && syncControllerRef.current === controller;
    setIsSyncing(true);
    try {
      const response = await client.post<ApiResponse<VikunjaSyncResult>>(
        '/vikunja/sync', undefined, withAccount(owner, controller.signal)
      );
      if (!isCurrent()) return;
      completeCaptchaAction('vikunja');
      toast.success(response.data.data?.message || '同步完成');
    } catch (error: unknown) {
      if (!isCurrent() || axios.isCancel(error)) return;
      if (axios.isAxiosError<ApiResponse<unknown>>(error) && error.response?.data.code === 4301) {
        requireCaptcha('vikunja');
        return;
      }
      toast.error(error instanceof Error ? error.message : '同步 Vikunja 失败');
    } finally {
      if (isCurrent()) {
        syncControllerRef.current = null;
        setIsSyncing(false);
      }
    }
  }, [owner, ownsPage, requireCaptcha, completeCaptchaAction]);

  const handleCaptchaClose = useCallback(() => {
    if (!ownsPage() || !captchaChallenge || challengeRef.current?.id !== captchaChallenge.id) return;
    captchaActionsRef.current.clear();
    challengeRef.current = null;
    setCaptchaChallenge(null);
  }, [captchaChallenge, ownsPage]);

  const handleCaptchaVerified = useCallback(() => {
    if (!ownsPage() || !captchaChallenge || challengeRef.current?.id !== captchaChallenge.id) return;
    const actions = [...captchaActionsRef.current];
    captchaActionsRef.current.clear();
    challengeRef.current = null;
    setCaptchaChallenge(null);
    for (const action of actions) {
      if (!ownsPage()) return;
      if (action === 'dashboard') void fetchDashboard();
      else void syncToVikunja();
    }
  }, [captchaChallenge, fetchDashboard, syncToVikunja, ownsPage]);

  const closeSettings = useCallback(() => {
    if (!ownsPage() || settingsCycle === null || settingsCycleIdRef.current !== settingsCycle) return;
    settingsCycleIdRef.current += 1;
    setSettingsCycle(null);
  }, [ownsPage, settingsCycle]);

  useEffect(() => {
    void loadDashboard();
  }, [loadDashboard]);

  const rawItems = data[activeView];
  const visibleItems = useMemo(() => {
    const available = rawItems.filter(item => !ignored[itemKey(item)]);
    return sortLearningItems(filterByStatus(available, statusFilter));
  }, [rawItems, ignored, statusFilter]);
  const statusCounts = useMemo(() => {
    const available = rawItems.filter(item => !ignored[itemKey(item)]);
    return {
      all: available.length,
      active: available.filter(isActiveItem).length,
      ended: available.filter(isEndedItem).length,
      finished: available.filter(item => item.finished).length
    };
  }, [rawItems, ignored]);
  const ignoredItems = useMemo(() => Object.values(ignored), [ignored]);

  const ignoreItem = (item: LearningItem) => {
    setIgnored(prev => ({ ...prev, [itemKey(item)]: item }));
  };

  const restoreItem = (key: string) => {
    setIgnored(prev => {
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  const openItem = (item: LearningItem) => {
    if (!item.link) {
      toast.error('该条目没有可打开的学习通链接');
      return;
    }
    window.open(item.link, '_blank', 'noopener,noreferrer');
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
        <h2 className="font-bold text-slate-900 text-lg">学习仪表盘</h2>
        <div className="flex items-center -mr-2">
          <button
            onClick={() => void syncToVikunja()}
            disabled={isSyncing}
            className="p-2 text-slate-600 hover:bg-slate-50 rounded-lg transition-colors disabled:opacity-50"
            title="同步未提交作业到 Vikunja"
          >
            <ListChecks size={20} className={isSyncing ? 'animate-smooth-spin' : ''} />
          </button>
          <button
            onClick={() => {
              if (ownsPage()) setSettingsCycle(++settingsCycleIdRef.current);
            }}
            className="p-2 text-slate-600 hover:bg-slate-50 rounded-lg transition-colors"
            title="Vikunja 同步设置"
          >
            <Settings size={19} />
          </button>
          <button
            onClick={() => void fetchDashboard()}
            disabled={isLoading}
            className="p-2 text-blue-600 hover:bg-blue-50 rounded-lg transition-colors disabled:opacity-50"
          >
            <RefreshCw size={20} className={isLoading ? 'animate-smooth-spin' : ''} />
          </button>
        </div>
      </div>

      <div className="bg-white border-b border-slate-100 px-3 py-3 shrink-0">
        <div className="grid grid-cols-4 gap-2">
          {views.map(view => {
            const Icon = view.icon;
            const count = data[view.key].filter(item => !ignored[itemKey(item)]).length;
            const active = activeView === view.key;
            return (
              <button
                key={view.key}
                onClick={() => setActiveView(view.key)}
                className={`min-w-0 rounded-xl px-2 py-2.5 text-xs font-bold transition-colors flex flex-col items-center gap-1 ${
                  active ? 'bg-blue-600 text-white shadow-lg shadow-blue-100' : 'bg-slate-50 text-slate-600 hover:bg-slate-100'
                }`}
              >
                <Icon size={16} />
                <span className="w-full truncate">{view.label}</span>
                <span className={`text-[10px] ${active ? 'text-blue-100' : 'text-slate-400'}`}>{count}</span>
              </button>
            );
          })}
        </div>
      </div>

      <div className="bg-white border-b border-slate-100 px-3 py-2 shrink-0">
        <div className="grid grid-cols-4 gap-2">
          {statusFilters.map(filter => {
            const active = statusFilter === filter.key;
            return (
              <button
                key={filter.key}
                onClick={() => setStatusFilter(filter.key)}
                className={`min-w-0 rounded-lg px-2 py-2 text-xs font-bold transition-colors ${
                  active ? 'bg-slate-900 text-white' : 'bg-slate-50 text-slate-600 hover:bg-slate-100'
                }`}
              >
                <span className="block truncate">{filter.label}</span>
                <span className={`block text-[10px] mt-0.5 ${active ? 'text-slate-300' : 'text-slate-400'}`}>
                  {statusCounts[filter.key]}
                </span>
              </button>
            );
          })}
        </div>
      </div>

      <PullToRefresh onRefresh={fetchDashboard} isRefreshing={isLoading} className="p-4">
        <div className="space-y-3 pb-[calc(80px+var(--sab))]">
          {data.errors.length > 0 && (
            <div className="rounded-2xl border border-amber-100 bg-amber-50 px-4 py-3 text-sm text-amber-800 flex items-start gap-2">
              <AlertTriangle size={18} className="mt-0.5 shrink-0" />
              <div className="min-w-0">
                <div className="font-bold">部分来源暂时不可用</div>
                <div className="mt-1 text-xs leading-5 break-words">{data.errors.join('；')}</div>
              </div>
            </div>
          )}

          <div className="flex items-center justify-between px-1">
            <div className="text-xs text-slate-500">
              显示 {visibleItems.length} 条，按状态和结束时间排序，已忽略 {ignoredItems.length} 条
            </div>
            <button
              onClick={() => setShowIgnored(v => !v)}
              className="text-xs font-bold text-blue-600 px-2 py-1 rounded-lg hover:bg-blue-50"
            >
              {showIgnored ? '返回列表' : '管理忽略'}
            </button>
          </div>

          {showIgnored ? (
            ignoredItems.length === 0 ? (
              <div className="py-16 flex flex-col items-center justify-center text-slate-400 bg-white rounded-2xl border border-dashed border-slate-200">
                <CheckCircle2 size={42} className="mb-3 opacity-30" />
                <p className="text-sm font-bold">暂无已忽略条目</p>
              </div>
            ) : (
              ignoredItems.map(item => {
                const key = itemKey(item);
                return (
                  <div key={key} className="bg-white rounded-2xl border border-slate-100 shadow-sm p-4 flex items-center gap-3">
                    <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 ${kindAccent(item.kind)}`}>
                      <X size={18} />
                    </div>
                    <div className="flex-1 min-w-0">
                      <div className="font-bold text-sm text-slate-900 truncate">{item.title}</div>
                      <div className="text-xs text-slate-500 mt-1 truncate">{item.course_name || item.type}</div>
                    </div>
                    <button
                      onClick={() => restoreItem(key)}
                      className="p-2 text-blue-600 hover:bg-blue-50 rounded-lg"
                    >
                      <RotateCcw size={18} />
                    </button>
                  </div>
                );
              })
            )
          ) : isLoading && visibleItems.length === 0 ? (
            <div className="py-16 flex flex-col items-center justify-center text-slate-400">
              <Loader2 size={34} className="animate-spin mb-3" />
              <p className="text-sm font-bold">正在获取学习通数据</p>
            </div>
          ) : visibleItems.length === 0 ? (
            <div className="py-16 flex flex-col items-center justify-center text-slate-400 bg-white rounded-2xl border border-dashed border-slate-200">
              <CheckCircle2 size={42} className="mb-3 opacity-30" />
              <p className="text-sm font-bold">当前没有需要显示的内容</p>
            </div>
          ) : (
            visibleItems.map(item => (
              <motion.div
                key={itemKey(item)}
                whileTap={{ scale: 0.97 }}
                onClick={() => openItem(item)}
                className="bg-white rounded-2xl border border-slate-100 shadow-sm p-4 cursor-pointer"
              >
                <div className="flex items-start gap-3">
                  <div className={`w-11 h-11 rounded-xl flex items-center justify-center shrink-0 ${kindAccent(item.kind)}`}>
                    {item.kind === 'exam' ? <GraduationCap size={20} /> : item.kind === 'homework' ? <FileText size={20} /> : <BookOpen size={20} />}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 min-w-0">
                      <span className={`px-2 py-0.5 rounded-lg border text-[11px] font-bold shrink-0 ${statusClass(item)}`}>
                        {item.status || item.type}
                      </span>
                      <span className="text-[11px] text-slate-400 truncate">{item.type}</span>
                    </div>
                    <h3 className="font-bold text-slate-900 text-sm leading-5 mt-2 break-words">{item.title}</h3>
                    <div className="flex items-center gap-2 mt-2 text-xs text-slate-500 min-w-0">
                      <span className="truncate">{item.course_name || '未知课程'}</span>
                      {item.info && <span className="shrink-0 text-slate-300">/</span>}
                      {item.info && <span className="truncate">{item.info}</span>}
                    </div>
                  </div>
                  <ExternalLink size={17} className="text-slate-300 shrink-0 mt-1" />
                </div>
                <div className="flex justify-end mt-3">
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      ignoreItem(item);
                    }}
                    className="px-3 py-1.5 rounded-lg bg-slate-50 text-xs font-bold text-slate-500 hover:bg-slate-100"
                  >
                    忽略
                  </button>
                </div>
              </motion.div>
            ))
          )}
        </div>
      </PullToRefresh>

      <AnimatePresence>
        {captchaChallenge && (
          <CaptchaModal
            key={captchaChallenge.id}
            owner={captchaChallenge.owner}
            onClose={handleCaptchaClose}
            onVerified={handleCaptchaVerified}
          />
        )}
      </AnimatePresence>
      <AnimatePresence>
        {settingsCycle !== null && (
          <VikunjaSettingsModal key={settingsCycle} owner={owner} onClose={closeSettings} onSync={syncToVikunja} isSyncing={isSyncing} />
        )}
      </AnimatePresence>
    </div>
  );
};

export default LearningDashboard;
