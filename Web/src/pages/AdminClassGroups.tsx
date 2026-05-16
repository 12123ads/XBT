import { useCallback, useEffect, useMemo, useState } from 'react';
import { AnimatePresence } from 'framer-motion';
import { CheckCircle2, Circle, Copy, GraduationCap, Loader2, Pencil, Plus, Trash2, Users } from 'lucide-react';
import toast from 'react-hot-toast';
import AdminModal from '../components/admin/AdminModal';
import AdminShell from '../components/admin/AdminShell';
import client from '../api/client';
import type { AdminAccount, AdminClassGroup, AdminClassGroupSyncMode, AdminClassGroupSyncResponse, ApiResponse } from '../types';

const emptyGroupForm = { name: '', description: '' };

const getErrorMessage = (error: unknown, fallback: string) => (
  error instanceof Error ? error.message : fallback
);

const AdminClassGroups = () => {
  const [accounts, setAccounts] = useState<AdminAccount[]>([]);
  const [classGroups, setClassGroups] = useState<AdminClassGroup[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isSavingGroup, setIsSavingGroup] = useState(false);
  const [isSyncingGroup, setIsSyncingGroup] = useState(false);

  const [showGroupForm, setShowGroupForm] = useState(false);
  const [editingGroup, setEditingGroup] = useState<AdminClassGroup | null>(null);
  const [memberGroup, setMemberGroup] = useState<AdminClassGroup | null>(null);
  const [syncGroup, setSyncGroup] = useState<AdminClassGroup | null>(null);
  const [syncSourceUid, setSyncSourceUid] = useState<number | null>(null);
  const [syncMode, setSyncMode] = useState<AdminClassGroupSyncMode>('replace');
  const [groupForm, setGroupForm] = useState(emptyGroupForm);
  const [memberDraftUids, setMemberDraftUids] = useState<number[]>([]);

  const accountByUid = useMemo(() => new Map(accounts.map((account) => [account.uid, account])), [accounts]);

  const loadAccounts = useCallback(async () => {
    const response = await client.get<ApiResponse<AdminAccount[]>>('/admin/accounts');
    setAccounts(response.data.data || []);
  }, []);

  const loadClassGroups = useCallback(async () => {
    const response = await client.get<ApiResponse<AdminClassGroup[]>>('/admin/class-groups');
    setClassGroups(response.data.data || []);
  }, []);

  const loadData = useCallback(async () => {
    setIsLoading(true);
    try {
      await Promise.all([loadAccounts(), loadClassGroups()]);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取班级分组失败'));
    } finally {
      setIsLoading(false);
    }
  }, [loadAccounts, loadClassGroups]);

  useEffect(() => {
    void loadData();
  }, [loadData]);

  const openCreateGroup = () => {
    setEditingGroup(null);
    setGroupForm(emptyGroupForm);
    setShowGroupForm(true);
  };

  const openEditGroup = (group: AdminClassGroup) => {
    setEditingGroup(group);
    setGroupForm({ name: group.name, description: group.description || '' });
    setShowGroupForm(true);
  };

  const handleSaveClassGroup = async () => {
    const payload = {
      name: groupForm.name.trim(),
      description: groupForm.description.trim(),
    };
    if (!payload.name) {
      toast.error('请填写班级名称');
      return;
    }

    setIsSavingGroup(true);
    try {
      if (editingGroup) {
        await client.put(`/admin/class-groups/${editingGroup.id}`, payload);
        toast.success('班级已更新');
      } else {
        await client.post('/admin/class-groups', payload);
        toast.success('班级已创建');
      }
      setShowGroupForm(false);
      setEditingGroup(null);
      setGroupForm(emptyGroupForm);
      await loadClassGroups();
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '保存班级失败'));
    } finally {
      setIsSavingGroup(false);
    }
  };

  const handleDeleteClassGroup = async (group: AdminClassGroup) => {
    if (!window.confirm(`删除班级「${group.name}」？账号、课程和签到记录不会被删除。`)) return;

    setIsSavingGroup(true);
    try {
      await client.delete(`/admin/class-groups/${group.id}`);
      toast.success('班级已删除');
      await loadClassGroups();
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '删除班级失败'));
    } finally {
      setIsSavingGroup(false);
    }
  };

  const openMembersPanel = (group: AdminClassGroup) => {
    setMemberGroup(group);
    setMemberDraftUids(group.member_uids || []);
  };

  const handleSaveGroupMembers = async () => {
    if (!memberGroup) return;

    setIsSavingGroup(true);
    try {
      await client.put(`/admin/class-groups/${memberGroup.id}/members`, { user_uids: memberDraftUids });
      toast.success('班级成员已更新');
      setMemberGroup(null);
      setMemberDraftUids([]);
      await loadClassGroups();
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '保存班级成员失败'));
    } finally {
      setIsSavingGroup(false);
    }
  };

  const openGroupSyncPanel = (group: AdminClassGroup) => {
    const memberSource = group.member_uids.find((uid) => accountByUid.has(uid));
    setSyncGroup(group);
    setSyncSourceUid(memberSource ?? accounts[0]?.uid ?? null);
    setSyncMode('replace');
  };

  const handleSyncClassGroup = async () => {
    if (!syncGroup || !syncSourceUid) return;

    setIsSyncingGroup(true);
    try {
      const response = await client.post<ApiResponse<AdminClassGroupSyncResponse>>(
        `/admin/class-groups/${syncGroup.id}/courses/copy-selection`,
        { source_uid: syncSourceUid, mode: syncMode },
      );
      const result = response.data.data;
      const modeText = syncMode === 'replace' ? '覆盖同步' : '追加同步';
      toast.success(`${modeText}完成：${result.target_count} 个成员，${result.course_count} 门课程`);
      setSyncGroup(null);
      await Promise.all([loadAccounts(), loadClassGroups()]);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '同步班级课程失败'));
    } finally {
      setIsSyncingGroup(false);
    }
  };

  const toggleMemberDraft = (uid: number) => {
    setMemberDraftUids((current) => (
      current.includes(uid)
        ? current.filter((item) => item !== uid)
        : [...current, uid]
    ));
  };

  const groupMemberSummary = (group: AdminClassGroup) => {
    const names = group.member_uids
      .map((uid) => accountByUid.get(uid)?.name || `UID ${uid}`)
      .filter(Boolean);
    if (names.length === 0) return '暂无成员';
    const visible = names.slice(0, 4).join('、');
    return names.length > 4 ? `${visible} 等 ${names.length} 人` : visible;
  };

  return (
    <AdminShell
      title="班级分组"
      subtitle="按班级维护成员并同步课程设置"
      action={(
        <button
          onClick={openCreateGroup}
          className="h-10 px-3 rounded-xl bg-slate-900 text-white text-xs font-black flex items-center gap-1.5"
        >
          <Plus size={14} />
          新建
        </button>
      )}
    >
      <div className="flex-1 min-h-0 overflow-hidden p-4 pb-[calc(16px+var(--sab))] flex flex-col">
        <section className="bg-white rounded-[1.75rem] border border-slate-100 shadow-sm p-4 flex-1 min-h-0 flex flex-col">
          <div className="flex items-center justify-between mb-3 shrink-0">
            <div className="flex items-center gap-2">
              <GraduationCap size={19} className="text-blue-600" />
              <h3 className="font-black text-slate-900">班级列表</h3>
            </div>
            <span className="text-[11px] font-bold text-slate-400">{classGroups.length} 个</span>
          </div>

          {isLoading ? (
            <div className="h-24 rounded-2xl bg-slate-50 animate-pulse" />
          ) : classGroups.length === 0 ? (
            <div className="py-8 text-center rounded-2xl bg-slate-50 text-sm text-slate-400">
              暂无班级，先新建一个分组再添加成员。
            </div>
          ) : (
            <div className="space-y-3 flex-1 min-h-0 overflow-y-auto pr-1 custom-scrollbar">
              {classGroups.map((group) => (
                <div key={group.id} className="rounded-2xl border border-slate-100 bg-slate-50/70 p-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="font-black text-slate-900 truncate">{group.name}</p>
                      <p className="text-xs text-slate-400 mt-0.5 line-clamp-2">
                        {group.description || '未填写说明'}
                      </p>
                    </div>
                    <div className="shrink-0 px-2.5 py-1 rounded-full bg-white text-[11px] font-black text-slate-600">
                      {group.member_count} 人
                    </div>
                  </div>
                  <p className="mt-2 text-[11px] text-slate-500 truncate">
                    成员：{groupMemberSummary(group)}
                  </p>
                  <div className="grid grid-cols-4 gap-2 mt-3">
                    <button onClick={() => openEditGroup(group)} className="py-2.5 rounded-xl bg-white text-slate-700 text-xs font-black flex items-center justify-center gap-1">
                      <Pencil size={13} />
                      编辑
                    </button>
                    <button onClick={() => openMembersPanel(group)} className="py-2.5 rounded-xl bg-white text-slate-700 text-xs font-black flex items-center justify-center gap-1">
                      <Users size={13} />
                      成员
                    </button>
                    <button
                      onClick={() => openGroupSyncPanel(group)}
                      disabled={group.member_count === 0 || accounts.length === 0}
                      className="py-2.5 rounded-xl bg-blue-600 text-white text-xs font-black flex items-center justify-center gap-1 disabled:opacity-40"
                    >
                      <Copy size={13} />
                      同步
                    </button>
                    <button
                      onClick={() => void handleDeleteClassGroup(group)}
                      disabled={isSavingGroup}
                      className="py-2.5 rounded-xl bg-red-50 text-red-600 text-xs font-black flex items-center justify-center gap-1 disabled:opacity-40"
                    >
                      <Trash2 size={13} />
                      删除
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>

      <AnimatePresence>
        {showGroupForm && (
          <AdminModal
            title={editingGroup ? '编辑班级' : '新建班级'}
            onClose={() => {
              setShowGroupForm(false);
              setEditingGroup(null);
            }}
          >
            <div className="space-y-3">
              <input
                value={groupForm.name}
                onChange={(event) => setGroupForm((current) => ({ ...current, name: event.target.value }))}
                placeholder="班级名称，例如 计科 2301"
                className="w-full px-4 py-3 rounded-2xl bg-slate-50 border border-slate-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
              <textarea
                value={groupForm.description}
                onChange={(event) => setGroupForm((current) => ({ ...current, description: event.target.value }))}
                placeholder="说明，可选"
                rows={3}
                className="w-full px-4 py-3 rounded-2xl bg-slate-50 border border-slate-100 focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
              />
              <button
                onClick={handleSaveClassGroup}
                disabled={isSavingGroup}
                className="w-full py-3.5 rounded-2xl bg-blue-600 text-white font-black flex items-center justify-center gap-2 disabled:opacity-50"
              >
                {isSavingGroup && <Loader2 size={18} className="animate-spin" />}
                保存班级
              </button>
            </div>
          </AdminModal>
        )}

        {memberGroup && (
          <AdminModal title={`设置成员：${memberGroup.name}`} onClose={() => setMemberGroup(null)}>
            <div className="space-y-3">
              <p className="text-xs text-slate-500 leading-relaxed">
                一个账号只能属于一个班级；选择后保存，会自动从原班级移动到这里。
              </p>
              <div className="max-h-72 overflow-y-auto space-y-2 pr-1 custom-scrollbar">
                {accounts.map((account) => {
                  const active = memberDraftUids.includes(account.uid);
                  const owner = classGroups.find((group) => (
                    group.id !== memberGroup.id && group.member_uids.includes(account.uid)
                  ));
                  return (
                    <button
                      key={account.uid}
                      onClick={() => toggleMemberDraft(account.uid)}
                      className={`w-full p-3 rounded-2xl border flex items-center justify-between text-left ${
                        active ? 'border-blue-500 bg-blue-50/40' : 'border-slate-100 bg-slate-50'
                      }`}
                    >
                      <div className="min-w-0">
                        <p className="font-bold text-slate-900 truncate">{account.name}</p>
                        <p className="text-[11px] text-slate-400 truncate">
                          {account.mobile_masked}{owner ? ` · 当前在 ${owner.name}` : ''}
                        </p>
                      </div>
                      <div className={active ? 'text-blue-600' : 'text-slate-300'}>
                        {active ? <CheckCircle2 size={22} /> : <Circle size={22} />}
                      </div>
                    </button>
                  );
                })}
              </div>
              <button
                onClick={handleSaveGroupMembers}
                disabled={isSavingGroup}
                className="w-full py-3.5 rounded-2xl bg-blue-600 text-white font-black flex items-center justify-center gap-2 disabled:opacity-50"
              >
                {isSavingGroup && <Loader2 size={18} className="animate-spin" />}
                保存 {memberDraftUids.length} 个成员
              </button>
            </div>
          </AdminModal>
        )}

        {syncGroup && (
          <AdminModal title={`同步班级：${syncGroup.name}`} onClose={() => setSyncGroup(null)}>
            <div className="space-y-4">
              <p className="text-xs text-slate-500 leading-relaxed">
                从源账号读取当前已选代签课程，并同步到该班级成员。
              </p>
              <div>
                <p className="text-xs font-black text-slate-500 mb-2">选择源账号</p>
                <div className="max-h-48 overflow-y-auto space-y-2 pr-1 custom-scrollbar">
                  {accounts.map((account) => {
                    const active = syncSourceUid === account.uid;
                    return (
                      <button
                        key={account.uid}
                        onClick={() => setSyncSourceUid(account.uid)}
                        className={`w-full p-3 rounded-2xl border flex items-center justify-between text-left ${
                          active ? 'border-blue-500 bg-blue-50/40' : 'border-slate-100 bg-slate-50'
                        }`}
                      >
                        <div className="min-w-0">
                          <p className="font-bold text-slate-900 truncate">{account.name}</p>
                          <p className="text-[11px] text-slate-400">
                            已选 {account.selected_count} / {account.course_count} 门
                          </p>
                        </div>
                        <div className={active ? 'text-blue-600' : 'text-slate-300'}>
                          {active ? <CheckCircle2 size={22} /> : <Circle size={22} />}
                        </div>
                      </button>
                    );
                  })}
                </div>
              </div>
              <div>
                <p className="text-xs font-black text-slate-500 mb-2">同步方式</p>
                <div className="grid grid-cols-2 gap-2">
                  <button
                    onClick={() => setSyncMode('replace')}
                    className={`p-3 rounded-2xl border text-left ${
                      syncMode === 'replace' ? 'border-blue-500 bg-blue-50/40' : 'border-slate-100 bg-slate-50'
                    }`}
                  >
                    <p className="text-sm font-black text-slate-900">覆盖一致</p>
                    <p className="text-[11px] text-slate-400 mt-1">先清空成员已选课程，再套用源账号课程。</p>
                  </button>
                  <button
                    onClick={() => setSyncMode('append')}
                    className={`p-3 rounded-2xl border text-left ${
                      syncMode === 'append' ? 'border-blue-500 bg-blue-50/40' : 'border-slate-100 bg-slate-50'
                    }`}
                  >
                    <p className="text-sm font-black text-slate-900">只追加</p>
                    <p className="text-[11px] text-slate-400 mt-1">只把源账号课程加给成员，不取消原选择。</p>
                  </button>
                </div>
              </div>
              <button
                onClick={handleSyncClassGroup}
                disabled={isSyncingGroup || !syncSourceUid}
                className="w-full py-3.5 rounded-2xl bg-blue-600 text-white font-black flex items-center justify-center gap-2 disabled:opacity-50"
              >
                {isSyncingGroup && <Loader2 size={18} className="animate-spin" />}
                同步到 {syncGroup.member_count} 个成员
              </button>
            </div>
          </AdminModal>
        )}
      </AnimatePresence>
    </AdminShell>
  );
};

export default AdminClassGroups;
