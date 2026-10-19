import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Loader2, RefreshCw, UserPlus, Users } from 'lucide-react';
import toast from 'react-hot-toast';
import AdminShell from '../components/admin/AdminShell';
import client from '../api/client';
import type { AdminAccount, ApiResponse, ClassAdminCreateResponse, ClassAdminOverview } from '../types';

const ClassAdmin = () => {
  const navigate = useNavigate();
  const [overview, setOverview] = useState<ClassAdminOverview | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isCreating, setIsCreating] = useState(false);
  const [mobile, setMobile] = useState('');
  const [password, setPassword] = useState('');
  const [lastResult, setLastResult] = useState<ClassAdminCreateResponse | null>(null);

  const loadOverview = useCallback(async () => {
    setIsLoading(true);
    try {
      const response = await client.get<ApiResponse<ClassAdminOverview>>('/class-admin/overview');
      setOverview(response.data.data ?? null);
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : '获取班级信息失败';
      toast.error(message);
      navigate('/', { replace: true });
    } finally {
      setIsLoading(false);
    }
  }, [navigate]);

  useEffect(() => {
    void loadOverview();
  }, [loadOverview]);

  const handleCreate = async () => {
    if (isCreating) return;
    const nextMobile = mobile.trim();
    if (!nextMobile || !password) {
      toast.error('请填写手机号和密码');
      return;
    }

    setIsCreating(true);
    try {
      const response = await client.post<ApiResponse<ClassAdminCreateResponse>>('/class-admin/accounts', {
        mobile: nextMobile,
        password,
      });
      const result = response.data.data;
      setLastResult(result);
      setMobile('');
      setPassword('');

      const parts = [`已添加 ${result.account.name || nextMobile}`];
      if (result.sync_count > 0) parts.push(`同步 ${result.sync_count} 门课程`);
      if (result.copied_relations > 0) parts.push(`套用 ${result.copied_relations} 门已选课程`);
      if (result.sync_message) parts.push(`课程同步失败：${result.sync_message}`);
      if (result.copy_message) parts.push(`课程套用失败：${result.copy_message}`);
      toast.success(parts.join('，'));
      await loadOverview();
    } catch (error: unknown) {
      toast.error(error instanceof Error ? error.message : '添加账号失败');
    } finally {
      setIsCreating(false);
    }
  };

  const members: AdminAccount[] = overview?.members ?? [];

  return (
    <AdminShell
      title="班级管理"
      subtitle={overview?.group.name || '班级管理员'}
      action={(
        <button
          type="button"
          onClick={() => void loadOverview()}
          disabled={isLoading}
          className="h-10 w-10 rounded-xl bg-slate-50 text-blue-600 flex items-center justify-center disabled:opacity-50"
          title="刷新"
        >
          {isLoading ? <Loader2 size={18} className="animate-spin" /> : <RefreshCw size={18} />}
        </button>
      )}
    >
      <div className="flex-1 min-h-0 overflow-y-auto p-4 pb-[calc(24px+var(--sab))] space-y-4 custom-scrollbar">
        <section className="rounded-[1.75rem] bg-slate-950 text-white p-5 overflow-hidden relative">
          <Users size={72} className="absolute -right-4 -bottom-4 text-white/10" />
          <p className="text-[10px] font-black text-cyan-200 uppercase tracking-[0.18em]">Class Admin</p>
          <h2 className="mt-1 text-2xl font-black">{overview?.group.name || '班级管理'}</h2>
          <p className="mt-2 text-sm text-slate-300 leading-relaxed">
            添加的新账号会自动加入本班，并默认套用你的已选课程。
          </p>
        </section>

        <section className="rounded-[1.75rem] bg-white border border-slate-100 shadow-sm p-4">
          <div className="flex items-center gap-2 mb-4">
            <UserPlus size={18} className="text-blue-600" />
            <h3 className="font-black text-slate-900">添加账号</h3>
          </div>
          <div className="space-y-3">
            <label className="block">
              <span className="text-xs font-bold text-slate-500">学习通手机号</span>
              <input
                type="tel"
                value={mobile}
                onChange={(event) => setMobile(event.target.value)}
                className="mt-1 w-full h-12 rounded-2xl border border-slate-100 bg-slate-50 px-3 text-sm outline-none focus:ring-2 focus:ring-blue-500"
                placeholder="手机号"
                autoComplete="off"
              />
            </label>
            <label className="block">
              <span className="text-xs font-bold text-slate-500">学习通密码</span>
              <input
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                className="mt-1 w-full h-12 rounded-2xl border border-slate-100 bg-slate-50 px-3 text-sm outline-none focus:ring-2 focus:ring-blue-500"
                placeholder="密码"
                autoComplete="new-password"
              />
            </label>
            <button
              type="button"
              onClick={() => void handleCreate()}
              disabled={isCreating}
              className="w-full py-3.5 rounded-2xl bg-blue-600 text-white font-black flex items-center justify-center gap-2 shadow-lg shadow-blue-100 disabled:opacity-50"
            >
              {isCreating ? <Loader2 size={18} className="animate-spin" /> : <UserPlus size={18} />}
              添加并同步课程
            </button>
          </div>
          {lastResult && (
            <div className="mt-4 rounded-2xl bg-green-50 border border-green-100 p-3 text-xs text-green-700 font-bold leading-relaxed">
              最近添加：{lastResult.account.name}（{lastResult.account.mobile_masked}）
              {lastResult.sync_count > 0 ? `，同步 ${lastResult.sync_count} 门课程` : ''}
              {lastResult.copied_relations > 0 ? `，套用 ${lastResult.copied_relations} 门已选课程` : ''}
            </div>
          )}
        </section>

        <section className="rounded-[1.75rem] bg-white border border-slate-100 shadow-sm p-4">
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center gap-2">
              <Users size={18} className="text-blue-600" />
              <h3 className="font-black text-slate-900">班级成员</h3>
            </div>
            <span className="text-[11px] font-bold text-slate-400">{members.length} 人</span>
          </div>
          {isLoading && members.length === 0 ? (
            <div className="h-20 rounded-2xl bg-slate-50 animate-pulse" />
          ) : members.length === 0 ? (
            <div className="py-8 text-center rounded-2xl bg-slate-50 text-sm text-slate-400">
              暂无成员
            </div>
          ) : (
            <div className="space-y-2">
              {members.map((member) => (
                <div key={member.uid} className="p-3 rounded-2xl border border-slate-100 bg-slate-50 flex items-center gap-3">
                  <div className="w-10 h-10 rounded-xl bg-white overflow-hidden flex items-center justify-center shrink-0 text-slate-400">
                    {member.avatar ? (
                      <img src={member.avatar} alt={member.name} className="w-full h-full object-cover" referrerPolicy="no-referrer" />
                    ) : (
                      <Users size={18} />
                    )}
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="font-bold text-slate-900 truncate">{member.name}</p>
                    <p className="text-[11px] text-slate-400 font-mono">{member.mobile_masked}</p>
                  </div>
                  <div className="text-[10px] text-slate-400 font-bold shrink-0">
                    已选 {member.selected_count}
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </AdminShell>
  );
};

export default ClassAdmin;
