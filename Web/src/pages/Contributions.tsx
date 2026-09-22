import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { motion, AnimatePresence } from 'framer-motion';
import { ChevronDown, ChevronLeft, RefreshCw, Trophy, User as UserIcon } from 'lucide-react';
import toast from 'react-hot-toast';
import axios from 'axios';
import client, { withAccount, type RequestAccount } from '../api/client';
import { useAuthStore } from '../store/auth';
import PullToRefresh from '../components/PullToRefresh';
import type { ApiResponse, ContributionBoard } from '../types';

const rankBadgeClass = (index: number) => {
  switch (index) {
    case 0:
      return 'bg-amber-100 text-amber-700';
    case 1:
      return 'bg-slate-200 text-slate-700';
    case 2:
      return 'bg-orange-100 text-orange-700';
    default:
      return 'bg-slate-100 text-slate-500';
  }
};

const Contributions = () => {
  const navigate = useNavigate();
  const { activeUid, token } = useAuthStore();
  const owner = useMemo<RequestAccount>(() => ({ uid: activeUid ?? 0, token: token ?? '' }), [activeUid, token]);
  const [board, setBoard] = useState<ContributionBoard | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [expanded, setExpanded] = useState<Record<number, boolean>>({});
  const mountedRef = useRef(false);
  const controllerRef = useRef<AbortController | null>(null);
  const requestIdRef = useRef(0);

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

  const loadBoard = useCallback(async () => {
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
      const response = await client.get<ApiResponse<ContributionBoard>>(
        '/sign/contributions',
        withAccount(owner, controller.signal)
      );
      if (!isCurrent()) return;
      setBoard(response.data.data ?? { group: null, items: [] });
    } catch (error: unknown) {
      if (!isCurrent() || axios.isCancel(error)) return;
      const message = error instanceof Error ? error.message : '获取贡献榜失败';
      const response = axios.isAxiosError<ApiResponse<unknown>>(error) ? error.response : undefined;
      console.error('[Contributions] fetch failed', { message, status: response?.status, code: response?.data?.code });
      toast.error(message);
    } finally {
      if (isCurrent()) {
        controllerRef.current = null;
        setIsLoading(false);
      }
    }
  }, [owner, ownsPage]);

  const fetchBoard = useCallback(async () => {
    if (!ownsPage()) return;
    setIsLoading(true);
    await loadBoard();
  }, [loadBoard, ownsPage]);

  useEffect(() => {
    void loadBoard();
  }, [loadBoard]);

  const toggle = (sourceUid: number) => {
    setExpanded((prev) => ({ ...prev, [sourceUid]: !prev[sourceUid] }));
  };

  const group = board?.group ?? null;
  const items = board?.items ?? [];

  return (
    <div className="h-full flex flex-col bg-slate-50 relative overflow-hidden">
      <div className="bg-white sticky top-0 z-30 border-b border-slate-100 px-4 h-[calc(80px+var(--sat))] pt-[var(--sat)] flex items-center justify-between shrink-0">
        <button
          onClick={() => navigate(-1)}
          className="p-2 -ml-2 text-slate-600 hover:bg-slate-50 rounded-lg transition-colors"
        >
          <ChevronLeft size={24} />
        </button>
        <div className="min-w-0 text-center">
          <h2 className="font-bold text-slate-900 text-lg leading-tight">贡献榜</h2>
          {group && (
            <p className="text-[10px] text-slate-400 font-bold truncate">{group.name}</p>
          )}
        </div>
        <button
          onClick={() => void fetchBoard()}
          disabled={isLoading}
          className="p-2 -mr-2 text-blue-600 hover:bg-blue-50 rounded-lg transition-colors disabled:opacity-50"
        >
          <RefreshCw size={20} className={isLoading ? 'animate-smooth-spin' : ''} />
        </button>
      </div>

      <PullToRefresh onRefresh={fetchBoard} isRefreshing={isLoading} className="p-4">
        <div className="space-y-3 pb-[calc(80px+var(--sab))]">
          {isLoading && !board ? (
            Array.from({ length: 4 }).map((_, i) => (
              <div key={i} className="bg-white rounded-2xl border border-slate-100 p-4 animate-pulse">
                <div className="flex items-center gap-3">
                  <div className="w-8 h-8 rounded-lg bg-slate-100" />
                  <div className="w-9 h-9 rounded-full bg-slate-100" />
                  <div className="flex-1 space-y-2">
                    <div className="h-3 bg-slate-100 rounded w-1/3" />
                    <div className="h-2 bg-slate-100 rounded w-1/4" />
                  </div>
                </div>
              </div>
            ))
          ) : group == null ? (
            <div className="bg-white rounded-2xl border border-slate-100 shadow-sm p-8 text-center">
              <div className="w-14 h-14 rounded-full bg-slate-100 flex items-center justify-center mx-auto">
                <Trophy size={26} className="text-slate-400" />
              </div>
              <p className="mt-4 text-sm font-bold text-slate-700">你尚未被分配班级</p>
              <p className="mt-1 text-xs text-slate-400">请联系管理员分配班级后查看班级贡献榜</p>
            </div>
          ) : items.length === 0 ? (
            <div className="bg-white rounded-2xl border border-slate-100 shadow-sm p-8 text-center">
              <div className="w-14 h-14 rounded-full bg-slate-100 flex items-center justify-center mx-auto">
                <Trophy size={26} className="text-slate-400" />
              </div>
              <p className="mt-4 text-sm font-bold text-slate-700">本班暂无代签记录</p>
              <p className="mt-1 text-xs text-slate-400">当同班同学之间发生代签后，这里会展示排行</p>
            </div>
          ) : (
            items.map((source, index) => {
              const isOpen = !!expanded[source.source_uid];
              return (
                <div key={source.source_uid} className="bg-white rounded-2xl border border-slate-100 shadow-sm overflow-hidden">
                  <button
                    onClick={() => toggle(source.source_uid)}
                    className="w-full flex items-center gap-3 p-4 text-left"
                  >
                    <div className={`w-8 h-8 rounded-lg flex items-center justify-center text-sm font-black shrink-0 ${rankBadgeClass(index)}`}>
                      {index + 1}
                    </div>
                    {source.source_avatar ? (
                      <img
                        src={source.source_avatar}
                        alt={source.source_name}
                        referrerPolicy="no-referrer"
                        className="w-9 h-9 rounded-full object-cover bg-slate-100 shrink-0"
                      />
                    ) : (
                      <div className="w-9 h-9 rounded-full bg-slate-100 flex items-center justify-center shrink-0">
                        <UserIcon size={18} className="text-slate-400" />
                      </div>
                    )}
                    <div className="flex-1 min-w-0">
                      <p className="font-bold text-slate-900 truncate">{source.source_name}</p>
                      <p className="text-[11px] text-slate-400">代签 {source.details.length} 位同学</p>
                    </div>
                    <div className="px-2.5 py-1 rounded-lg bg-blue-50 text-blue-600 text-xs font-black shrink-0">
                      {source.total} 次
                    </div>
                    <ChevronDown
                      size={18}
                      className={`text-slate-400 shrink-0 transition-transform ${isOpen ? 'rotate-180' : ''}`}
                    />
                  </button>
                  <AnimatePresence initial={false}>
                    {isOpen && (
                      <motion.div
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: 'auto', opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        className="overflow-hidden"
                      >
                        <div className="border-t border-slate-100 divide-y divide-slate-50">
                          {source.details.map((target) => (
                            <div key={target.target_uid} className="flex items-center justify-between px-4 py-2.5 pl-[4.75rem]">
                              <span className="text-sm text-slate-600 truncate">{target.target_name}</span>
                              <span className="text-xs font-bold text-slate-400 shrink-0">{target.count} 次</span>
                            </div>
                          ))}
                        </div>
                      </motion.div>
                    )}
                  </AnimatePresence>
                </div>
              );
            })
          )}
        </div>
      </PullToRefresh>
    </div>
  );
};

export default Contributions;
