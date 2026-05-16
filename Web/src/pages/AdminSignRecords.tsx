import { useCallback, useEffect, useState } from 'react';
import { CheckCircle2, RefreshCw, Search } from 'lucide-react';
import toast from 'react-hot-toast';
import AdminShell from '../components/admin/AdminShell';
import client from '../api/client';
import type { AdminAccount, AdminSignRecord, AdminSignRecordPage, ApiResponse } from '../types';

type RecordFilters = {
  keyword: string;
  userUid: string;
  sourceUid: string;
  signType: string;
  startTime: string;
  endTime: string;
};

const emptyRecordFilters: RecordFilters = {
  keyword: '',
  userUid: '',
  sourceUid: '',
  signType: '',
  startTime: '',
  endTime: '',
};
const RECORD_PAGE_SIZE = 10;

const getErrorMessage = (error: unknown, fallback: string) => (
  error instanceof Error ? error.message : fallback
);

const signTypeLabel = (type: number) => {
  switch (type) {
    case 2: return '二维码';
    case 3: return '手势';
    case 4: return '位置';
    case 5: return '签到码';
    default: return '普通';
  }
};

const formatRecordTime = (timeMs: number) => {
  if (!timeMs) return '未知时间';
  return new Date(timeMs).toLocaleString('zh-CN', { hour12: false });
};

const dateTimeLocalToMs = (value: string) => {
  if (!value) return null;
  const time = new Date(value).getTime();
  return Number.isFinite(time) ? time : null;
};

const AdminSignRecords = () => {
  const [accounts, setAccounts] = useState<AdminAccount[]>([]);
  const [records, setRecords] = useState<AdminSignRecord[]>([]);
  const [filters, setFilters] = useState<RecordFilters>(emptyRecordFilters);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [totalPages, setTotalPages] = useState(0);
  const [isLoading, setIsLoading] = useState(true);

  const loadAccounts = useCallback(async () => {
    try {
      const response = await client.get<ApiResponse<AdminAccount[]>>('/admin/accounts');
      setAccounts(response.data.data || []);
    } catch {
      setAccounts([]);
    }
  }, []);

  const loadRecords = useCallback(async () => {
    setIsLoading(true);
    try {
      const params: Record<string, string | number> = {
        page,
        page_size: RECORD_PAGE_SIZE,
      };
      const keyword = filters.keyword.trim();
      if (keyword) params.keyword = keyword;
      if (filters.userUid) params.user_uid = filters.userUid;
      if (filters.sourceUid) params.source_uid = filters.sourceUid;
      if (filters.signType) params.sign_type = filters.signType;
      const startTime = dateTimeLocalToMs(filters.startTime);
      const endTime = dateTimeLocalToMs(filters.endTime);
      if (startTime) params.start_time = startTime;
      if (endTime) params.end_time = endTime;

      const response = await client.get<ApiResponse<AdminSignRecordPage>>('/admin/sign-records', { params });
      const data = response.data.data;
      setRecords(data.items || []);
      setTotal(data.total || 0);
      setTotalPages(data.total_pages || 0);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取签到记录失败'));
    } finally {
      setIsLoading(false);
    }
  }, [filters, page]);

  useEffect(() => {
    void loadAccounts();
  }, [loadAccounts]);

  useEffect(() => {
    void loadRecords();
  }, [loadRecords]);

  const updateFilter = (key: keyof RecordFilters, value: string) => {
    setFilters((current) => ({ ...current, [key]: value }));
    setPage(1);
  };

  const resetFilters = () => {
    setFilters(emptyRecordFilters);
    setPage(1);
  };

  return (
    <AdminShell
      title="签到记录"
      subtitle={`XBT 成功签到记录，同一课程同次合并，共 ${total} 组`}
      action={(
        <button
          onClick={() => void loadRecords()}
          disabled={isLoading}
          className="h-10 px-3 rounded-xl bg-slate-100 text-slate-700 text-xs font-black flex items-center gap-1.5 disabled:opacity-50"
        >
          <RefreshCw size={14} className={isLoading ? 'animate-smooth-spin' : ''} />
          刷新
        </button>
      )}
    >
      <div className="flex-1 min-h-0 overflow-y-auto p-4 pb-[calc(16px+var(--sab))] space-y-3 custom-scrollbar">
        <section className="bg-white rounded-[1.75rem] border border-slate-100 shadow-sm p-4 shrink-0">
          <div className="grid grid-cols-2 gap-2">
            <div className="relative col-span-2">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" size={15} />
              <input
                value={filters.keyword}
                onChange={(event) => updateFilter('keyword', event.target.value)}
                placeholder="搜索课程 / 活动 / 姓名 / ID"
                className="w-full pl-9 pr-3 py-3 rounded-2xl bg-slate-50 border border-slate-100 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <select
              value={filters.userUid}
              onChange={(event) => updateFilter('userUid', event.target.value)}
              className="w-full px-3 py-3 rounded-2xl bg-slate-50 border border-slate-100 text-sm text-slate-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <option value="">全部账号</option>
              {accounts.map((account) => (
                <option key={account.uid} value={account.uid}>{account.name || account.uid}</option>
              ))}
            </select>
            <select
              value={filters.sourceUid}
              onChange={(event) => updateFilter('sourceUid', event.target.value)}
              className="w-full px-3 py-3 rounded-2xl bg-slate-50 border border-slate-100 text-sm text-slate-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <option value="">全部来源</option>
              {accounts.map((account) => (
                <option key={account.uid} value={account.uid}>{account.name || account.uid}</option>
              ))}
            </select>
            <select
              value={filters.signType}
              onChange={(event) => updateFilter('signType', event.target.value)}
              className="w-full px-3 py-3 rounded-2xl bg-slate-50 border border-slate-100 text-sm text-slate-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <option value="">全部类型</option>
              <option value="0">普通</option>
              <option value="2">二维码</option>
              <option value="3">手势</option>
              <option value="4">位置</option>
              <option value="5">签到码</option>
            </select>
            <button onClick={resetFilters} className="px-3 py-3 rounded-2xl bg-slate-900 text-white text-sm font-black">
              重置筛选
            </button>
            <input
              value={filters.startTime}
              onChange={(event) => updateFilter('startTime', event.target.value)}
              type="datetime-local"
              className="w-full px-3 py-3 rounded-2xl bg-slate-50 border border-slate-100 text-sm text-slate-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
            <input
              value={filters.endTime}
              onChange={(event) => updateFilter('endTime', event.target.value)}
              type="datetime-local"
              className="w-full px-3 py-3 rounded-2xl bg-slate-50 border border-slate-100 text-sm text-slate-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
        </section>

        <section className="bg-white rounded-[1.75rem] border border-slate-100 shadow-sm p-4">
          <div className="flex items-center justify-between mb-3 shrink-0">
            <div className="flex items-center gap-2">
              <CheckCircle2 size={18} className="text-blue-600" />
              <h3 className="font-black text-slate-900">记录列表</h3>
            </div>
            <span className="text-[11px] font-bold text-slate-400">第 {page} / {Math.max(totalPages, 1)} 页</span>
          </div>

          {isLoading ? (
            <div className="space-y-2">
              {[1, 2, 3].map((item) => <div key={item} className="h-20 rounded-2xl bg-slate-50 animate-pulse" />)}
            </div>
          ) : records.length === 0 ? (
            <div className="py-8 text-center rounded-2xl bg-slate-50 text-sm text-slate-400">
              暂无符合条件的签到记录。
            </div>
          ) : (
            <div className="space-y-2">
              {records.map((record) => (
                <div key={record.id} className="rounded-2xl border border-slate-100 bg-slate-50/70 p-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="font-black text-slate-900 truncate">{record.course_name}</p>
                      <p className="mt-0.5 text-xs text-slate-500 truncate">
                        {record.activity_name} · {signTypeLabel(record.sign_type)}
                      </p>
                    </div>
                    <div className="shrink-0 px-2.5 py-1 rounded-full bg-green-100 text-green-700 text-[11px] font-black">
                      {record.target_count} 人
                    </div>
                  </div>
                  <div className="mt-3 grid grid-cols-2 gap-2 text-[11px] text-slate-500">
                    <div className="min-w-0 col-span-2">
                      <span className="font-bold text-slate-700">目标：</span>
                      <span title={record.target_names}>{record.target_names}</span>
                    </div>
                    <div className="min-w-0 col-span-2">
                      <span className="font-bold text-slate-700">来源：</span>
                      <span title={record.source_names}>{record.source_names}</span>
                    </div>
                    <div className="col-span-2 font-mono text-slate-400">
                      {formatRecordTime(record.sign_time_ms)} · course {record.course_id || '-'} / class {record.class_id || '-'}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}

          <div className="mt-3 flex items-center justify-between shrink-0">
            <button
              onClick={() => setPage((current) => Math.max(1, current - 1))}
              disabled={page <= 1 || isLoading}
              className="h-9 px-3 rounded-xl bg-slate-100 text-slate-700 text-xs font-black disabled:opacity-40"
            >
              上一页
            </button>
            <span className="text-xs font-bold text-slate-400">{total} 组记录</span>
            <button
              onClick={() => setPage((current) => current + 1)}
              disabled={totalPages === 0 || page >= totalPages || isLoading}
              className="h-9 px-3 rounded-xl bg-slate-100 text-slate-700 text-xs font-black disabled:opacity-40"
            >
              下一页
            </button>
          </div>
        </section>
      </div>
    </AdminShell>
  );
};

export default AdminSignRecords;
