import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AnimatePresence, motion } from 'framer-motion';
import type { ReactNode } from 'react';
import {
  BookOpen,
  CheckCircle2,
  ChevronLeft,
  Circle,
  Copy,
  GraduationCap,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  Save,
  Search,
  ShieldCheck,
  Trash2,
  UserPlus,
  Users,
  X,
} from 'lucide-react';
import toast from 'react-hot-toast';
import client from '../api/client';
import type {
  AdminAccount,
  AdminClassGroup,
  AdminClassGroupSyncMode,
  AdminClassGroupSyncResponse,
  AdminCreateAccountResponse,
  AdminManagedCourse,
  ApiResponse,
} from '../types';

type CourseRef = Pick<AdminManagedCourse, 'course_id' | 'class_id'>;

const courseKey = (course: CourseRef) => `${course.course_id}:${course.class_id}`;
const emptyGroupForm = { name: '', description: '' };

const getErrorMessage = (error: unknown, fallback: string) => {
  return error instanceof Error ? error.message : fallback;
};

const AdminPanel = () => {
  const navigate = useNavigate();
  const [accounts, setAccounts] = useState<AdminAccount[]>([]);
  const [classGroups, setClassGroups] = useState<AdminClassGroup[]>([]);
  const [selectedUid, setSelectedUid] = useState<number | null>(null);
  const [courses, setCourses] = useState<AdminManagedCourse[]>([]);
  const [selectedCourseKeys, setSelectedCourseKeys] = useState<string[]>([]);
  const [search, setSearch] = useState('');

  const [isLoadingAccounts, setIsLoadingAccounts] = useState(true);
  const [isLoadingGroups, setIsLoadingGroups] = useState(true);
  const [isLoadingCourses, setIsLoadingCourses] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [isSyncing, setIsSyncing] = useState(false);
  const [isAdding, setIsAdding] = useState(false);
  const [isCopying, setIsCopying] = useState(false);
  const [isSavingGroup, setIsSavingGroup] = useState(false);
  const [isSyncingGroup, setIsSyncingGroup] = useState(false);

  const [showAddAccount, setShowAddAccount] = useState(false);
  const [showAddCourse, setShowAddCourse] = useState(false);
  const [showCopyPanel, setShowCopyPanel] = useState(false);
  const [showGroupForm, setShowGroupForm] = useState(false);
  const [editingGroup, setEditingGroup] = useState<AdminClassGroup | null>(null);
  const [memberGroup, setMemberGroup] = useState<AdminClassGroup | null>(null);
  const [syncGroup, setSyncGroup] = useState<AdminClassGroup | null>(null);
  const [syncSourceUid, setSyncSourceUid] = useState<number | null>(null);
  const [syncMode, setSyncMode] = useState<AdminClassGroupSyncMode>('replace');

  const [newAccount, setNewAccount] = useState({ mobile: '', password: '' });
  const [manualCourse, setManualCourse] = useState({ courseId: '', classId: '', name: '', teacher: '' });
  const [copyTargetUids, setCopyTargetUids] = useState<number[]>([]);
  const [groupForm, setGroupForm] = useState(emptyGroupForm);
  const [memberDraftUids, setMemberDraftUids] = useState<number[]>([]);

  const accountByUid = useMemo(() => {
    return new Map(accounts.map((account) => [account.uid, account]));
  }, [accounts]);

  const selectedAccount = useMemo(
    () => accounts.find((account) => account.uid === selectedUid) || null,
    [accounts, selectedUid],
  );

  const filteredAccounts = useMemo(() => {
    const keyword = search.trim().toLowerCase();
    if (!keyword) return accounts;
    return accounts.filter((account) => (
      account.name.toLowerCase().includes(keyword)
      || account.mobile_masked.includes(keyword)
      || String(account.uid).includes(keyword)
    ));
  }, [accounts, search]);

  const loadAccounts = useCallback(async () => {
    setIsLoadingAccounts(true);
    try {
      const response = await client.get<ApiResponse<AdminAccount[]>>('/admin/accounts');
      const data = response.data.data || [];
      setAccounts(data);
      setSelectedUid((current) => (
        current && data.some((account) => account.uid === current)
          ? current
          : data[0]?.uid ?? null
      ));
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取账号失败'));
    } finally {
      setIsLoadingAccounts(false);
    }
  }, []);

  const loadClassGroups = useCallback(async () => {
    setIsLoadingGroups(true);
    try {
      const response = await client.get<ApiResponse<AdminClassGroup[]>>('/admin/class-groups');
      setClassGroups(response.data.data || []);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取班级分组失败'));
    } finally {
      setIsLoadingGroups(false);
    }
  }, []);

  const loadCourses = useCallback(async (uid: number) => {
    setIsLoadingCourses(true);
    try {
      const response = await client.get<ApiResponse<AdminManagedCourse[]>>(`/admin/accounts/${uid}/courses`);
      const data = response.data.data || [];
      setCourses(data);
      setSelectedCourseKeys(data.filter((course) => course.is_selected).map(courseKey));
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '获取课程失败'));
    } finally {
      setIsLoadingCourses(false);
    }
  }, []);

  useEffect(() => {
    void loadAccounts();
    void loadClassGroups();
  }, [loadAccounts, loadClassGroups]);

  useEffect(() => {
    if (selectedUid) {
      void loadCourses(selectedUid);
    } else {
      setCourses([]);
      setSelectedCourseKeys([]);
    }
  }, [loadCourses, selectedUid]);

  const buildSelectedRefs = () => {
    return courses
      .filter((course) => selectedCourseKeys.includes(courseKey(course)))
      .map((course) => ({ course_id: course.course_id, class_id: course.class_id }));
  };

  const saveSelection = async (showToast: boolean) => {
    if (!selectedUid) return false;
    const selectedRefs = buildSelectedRefs();
    await client.put(`/admin/accounts/${selectedUid}/courses/selection`, { courses: selectedRefs });
    if (showToast) toast.success(`已保存 ${selectedRefs.length} 门代签课程`);
    await loadAccounts();
    return true;
  };

  const handleAddAccount = async () => {
    if (!newAccount.mobile.trim() || !newAccount.password) {
      toast.error('请填写手机号和密码');
      return;
    }
    setIsAdding(true);
    try {
      const response = await client.post<ApiResponse<AdminCreateAccountResponse>>('/admin/accounts', newAccount);
      const created = response.data.data;
      setShowAddAccount(false);
      setNewAccount({ mobile: '', password: '' });
      await loadAccounts();
      setSelectedUid(created.account.uid);
      if (created.sync_message) {
        toast.success('账号已添加，课程同步失败，可稍后手动同步');
      } else {
        toast.success(`账号已添加，同步 ${created.sync_count} 门课程`);
      }
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '添加账号失败'));
    } finally {
      setIsAdding(false);
    }
  };

  const handleSyncCourses = async () => {
    if (!selectedUid) return;
    setIsSyncing(true);
    try {
      const response = await client.post<ApiResponse<{ count: number }>>(`/admin/accounts/${selectedUid}/courses/sync`);
      toast.success(`已同步 ${response.data.data.count} 门课程`);
      await loadCourses(selectedUid);
      await loadAccounts();
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '同步课程失败'));
    } finally {
      setIsSyncing(false);
    }
  };

  const handleSaveSelection = async () => {
    setIsSaving(true);
    try {
      await saveSelection(true);
      if (selectedUid) await loadCourses(selectedUid);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '保存课程失败'));
    } finally {
      setIsSaving(false);
    }
  };

  const handleAddManualCourse = async () => {
    if (!selectedUid) return;
    const courseId = Number(manualCourse.courseId);
    const classId = Number(manualCourse.classId);
    if (!Number.isFinite(courseId) || !Number.isFinite(classId) || courseId <= 0 || classId <= 0) {
      toast.error('请填写有效的 course_id 和 class_id');
      return;
    }

    setIsSaving(true);
    try {
      await client.post(`/admin/accounts/${selectedUid}/courses`, {
        course_id: courseId,
        class_id: classId,
        name: manualCourse.name,
        teacher: manualCourse.teacher,
        is_selected: true,
      });
      toast.success('课程已添加并设为代签课程');
      setShowAddCourse(false);
      setManualCourse({ courseId: '', classId: '', name: '', teacher: '' });
      await loadCourses(selectedUid);
      await loadAccounts();
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '添加课程失败'));
    } finally {
      setIsSaving(false);
    }
  };

  const handleCopySelection = async () => {
    if (!selectedUid) return;
    if (selectedCourseKeys.length === 0) {
      toast.error('请先选择要套用的课程');
      return;
    }
    if (copyTargetUids.length === 0) {
      toast.error('请选择目标账号');
      return;
    }

    setIsCopying(true);
    try {
      await saveSelection(false);
      const response = await client.post<ApiResponse<{ target_count: number; course_count: number }>>('/admin/courses/copy-selection', {
        source_uid: selectedUid,
        target_uids: copyTargetUids,
      });
      toast.success(`已给 ${response.data.data.target_count} 个账号套用 ${response.data.data.course_count} 门课程`);
      setCopyTargetUids([]);
      setShowCopyPanel(false);
      await loadAccounts();
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '套用课程失败'));
    } finally {
      setIsCopying(false);
    }
  };

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
    const defaultSource = selectedUid && accountByUid.has(selectedUid)
      ? selectedUid
      : memberSource ?? accounts[0]?.uid ?? null;
    setSyncGroup(group);
    setSyncSourceUid(defaultSource);
    setSyncMode('replace');
  };

  const handleSyncClassGroup = async () => {
    if (!syncGroup || !syncSourceUid) return;

    setIsSyncingGroup(true);
    try {
      if (syncSourceUid === selectedUid) {
        await saveSelection(false);
      }
      const response = await client.post<ApiResponse<AdminClassGroupSyncResponse>>(
        `/admin/class-groups/${syncGroup.id}/courses/copy-selection`,
        { source_uid: syncSourceUid, mode: syncMode },
      );
      const result = response.data.data;
      const modeText = syncMode === 'replace' ? '覆盖同步' : '追加同步';
      toast.success(`${modeText}完成：${result.target_count} 个成员，${result.course_count} 门课程`);
      setSyncGroup(null);
      await loadAccounts();
      if (selectedUid) await loadCourses(selectedUid);
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '同步班级课程失败'));
    } finally {
      setIsSyncingGroup(false);
    }
  };

  const toggleCourse = (course: AdminManagedCourse) => {
    const key = courseKey(course);
    setSelectedCourseKeys((current) => (
      current.includes(key)
        ? current.filter((item) => item !== key)
        : [...current, key]
    ));
  };

  const toggleCopyTarget = (uid: number) => {
    setCopyTargetUids((current) => (
      current.includes(uid)
        ? current.filter((item) => item !== uid)
        : [...current, uid]
    ));
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
    <div className="flex-1 min-h-0 flex flex-col bg-slate-50 overflow-hidden">
      <div className="bg-white sticky top-0 z-20 border-b border-slate-100 px-4 h-[calc(80px+var(--sat))] pt-[var(--sat)] flex items-center justify-between shrink-0">
        <div className="flex items-center min-w-0">
          <button onClick={() => navigate(-1)} className="p-2 -ml-2 text-slate-600 hover:bg-slate-50 rounded-lg">
            <ChevronLeft size={24} />
          </button>
          <div className="ml-2 min-w-0">
            <h2 className="font-bold text-slate-900 text-lg truncate">管理面板</h2>
            <p className="text-[10px] text-slate-400 font-bold truncate">账号、课程与班级分组</p>
          </div>
        </div>
        <button
          onClick={() => setShowAddAccount(true)}
          className="h-10 px-3 bg-blue-600 text-white rounded-xl font-bold text-xs flex items-center gap-2 active:scale-95 transition-transform"
        >
          <UserPlus size={16} />
          添加账号
        </button>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto p-4 space-y-4 pb-[calc(40px+var(--sab))] custom-scrollbar">
        <section className="bg-white rounded-[1.75rem] border border-slate-100 shadow-sm p-4">
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center gap-2">
              <Users size={18} className="text-blue-600" />
              <h3 className="font-black text-slate-900">账号列表</h3>
            </div>
            <span className="text-[11px] font-bold text-slate-400">{accounts.length} 个</span>
          </div>
          <div className="relative mb-3">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" size={16} />
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="搜索姓名 / UID / 手机号"
              className="w-full pl-9 pr-3 py-3 rounded-2xl bg-slate-50 border border-slate-100 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>

          {isLoadingAccounts ? (
            <div className="h-24 rounded-2xl bg-slate-50 animate-pulse" />
          ) : filteredAccounts.length === 0 ? (
            <div className="py-8 text-center text-sm text-slate-400">暂无账号</div>
          ) : (
            <div className="space-y-2 max-h-64 overflow-y-auto pr-1 custom-scrollbar">
              {filteredAccounts.map((account) => {
                const active = account.uid === selectedUid;
                return (
                  <button
                    key={account.uid}
                    onClick={() => setSelectedUid(account.uid)}
                    className={`w-full p-3 rounded-2xl border text-left transition-all flex items-center gap-3 ${
                      active ? 'border-blue-500 bg-blue-50/50' : 'border-slate-100 bg-slate-50/50 hover:bg-white'
                    }`}
                  >
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
                        <p className="font-bold text-slate-900 truncate">{account.name || '未命名账号'}</p>
                        {account.permission >= 2 && <ShieldCheck size={13} className="text-blue-600 shrink-0" />}
                      </div>
                      <p className="text-[11px] text-slate-500 font-mono">{account.mobile_masked} · UID {account.uid}</p>
                    </div>
                    <div className="text-right shrink-0">
                      <p className="text-xs font-black text-slate-800">{account.selected_count}/{account.course_count}</p>
                      <p className="text-[10px] text-slate-400">课程</p>
                    </div>
                  </button>
                );
              })}
            </div>
          )}
        </section>

        <section className="bg-white rounded-[1.75rem] border border-slate-100 shadow-sm p-4">
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center gap-2">
              <GraduationCap size={19} className="text-blue-600" />
              <h3 className="font-black text-slate-900">班级分组</h3>
            </div>
            <button
              onClick={openCreateGroup}
              className="h-9 px-3 rounded-xl bg-slate-900 text-white text-xs font-black flex items-center gap-1.5"
            >
              <Plus size={14} />
              新建
            </button>
          </div>

          {isLoadingGroups ? (
            <div className="h-24 rounded-2xl bg-slate-50 animate-pulse" />
          ) : classGroups.length === 0 ? (
            <div className="py-8 text-center rounded-2xl bg-slate-50 text-sm text-slate-400">
              暂无班级，先新建一个分组再添加成员。
            </div>
          ) : (
            <div className="space-y-3">
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
                    <button
                      onClick={() => openEditGroup(group)}
                      className="py-2.5 rounded-xl bg-white text-slate-700 text-xs font-black flex items-center justify-center gap-1"
                    >
                      <Pencil size={13} />
                      编辑
                    </button>
                    <button
                      onClick={() => openMembersPanel(group)}
                      className="py-2.5 rounded-xl bg-white text-slate-700 text-xs font-black flex items-center justify-center gap-1"
                    >
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

        <section className="bg-white rounded-[1.75rem] border border-slate-100 shadow-sm overflow-hidden">
          <div className="p-4 border-b border-slate-100">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <div className="flex items-center gap-2 mb-1">
                  <BookOpen size={18} className="text-blue-600" />
                  <h3 className="font-black text-slate-900 truncate">
                    {selectedAccount ? selectedAccount.name : '选择账号'}
                  </h3>
                </div>
                <p className="text-xs text-slate-400">
                  勾选的课程会出现在首页代签同学列表中；套用会把这些课程添加给目标账号。
                </p>
              </div>
              <button
                onClick={handleSyncCourses}
                disabled={!selectedUid || isSyncing}
                className="w-10 h-10 rounded-xl bg-slate-50 text-blue-600 flex items-center justify-center disabled:opacity-40 shrink-0"
                title="同步该账号课程"
              >
                <RefreshCw size={18} className={isSyncing ? 'animate-smooth-spin' : ''} />
              </button>
            </div>
          </div>

          <div className="p-4 space-y-3">
            <div className="grid grid-cols-3 gap-2">
              <button
                onClick={handleSaveSelection}
                disabled={!selectedUid || isSaving}
                className="py-3 rounded-2xl bg-blue-600 text-white text-xs font-black flex items-center justify-center gap-1.5 disabled:opacity-50"
              >
                {isSaving ? <Loader2 size={15} className="animate-spin" /> : <Save size={15} />}
                保存
              </button>
              <button
                onClick={() => setShowAddCourse(true)}
                disabled={!selectedUid}
                className="py-3 rounded-2xl bg-slate-900 text-white text-xs font-black flex items-center justify-center gap-1.5 disabled:opacity-50"
              >
                <Plus size={15} />
                加课程
              </button>
              <button
                onClick={() => setShowCopyPanel(true)}
                disabled={!selectedUid || selectedCourseKeys.length === 0}
                className="py-3 rounded-2xl bg-slate-100 text-slate-800 text-xs font-black flex items-center justify-center gap-1.5 disabled:opacity-50"
              >
                <Copy size={15} />
                套用
              </button>
            </div>

            <div className="flex items-center justify-between px-1 text-xs">
              <span className="font-bold text-slate-500">已选 {selectedCourseKeys.length} 门</span>
              {courses.length > 0 && (
                <button
                  onClick={() => {
                    setSelectedCourseKeys(
                      selectedCourseKeys.length === courses.length ? [] : courses.map(courseKey),
                    );
                  }}
                  className="font-bold text-blue-600"
                >
                  {selectedCourseKeys.length === courses.length ? '取消全选' : '全选'}
                </button>
              )}
            </div>

            {isLoadingCourses ? (
              <div className="space-y-2">
                {[1, 2, 3].map((item) => <div key={item} className="h-16 rounded-2xl bg-slate-50 animate-pulse" />)}
              </div>
            ) : courses.length === 0 ? (
              <div className="py-10 text-center rounded-2xl bg-slate-50 text-sm text-slate-400">
                该账号暂无课程，可先同步或手动添加。
              </div>
            ) : (
              <div className="space-y-2">
                {courses.map((course) => {
                  const active = selectedCourseKeys.includes(courseKey(course));
                  return (
                    <button
                      key={courseKey(course)}
                      onClick={() => toggleCourse(course)}
                      className={`w-full p-4 rounded-2xl border text-left flex items-center gap-3 transition-all ${
                        active ? 'border-blue-500 bg-blue-50/40' : 'border-slate-100 bg-slate-50/50'
                      }`}
                    >
                      <div className={`shrink-0 ${active ? 'text-blue-600' : 'text-slate-300'}`}>
                        {active ? <CheckCircle2 size={24} /> : <Circle size={24} />}
                      </div>
                      <div className="min-w-0 flex-1">
                        <p className="font-bold text-slate-900 truncate">{course.name}</p>
                        <p className="text-[11px] text-slate-400 truncate">
                          {course.teacher || '未知教师'} · course {course.course_id} / class {course.class_id}
                        </p>
                      </div>
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        </section>
      </div>

      <AnimatePresence>
        {showAddAccount && (
          <Modal title="添加学习通账号" onClose={() => setShowAddAccount(false)}>
            <div className="space-y-3">
              <input
                value={newAccount.mobile}
                onChange={(event) => setNewAccount((current) => ({ ...current, mobile: event.target.value }))}
                placeholder="手机号"
                className="w-full px-4 py-3 rounded-2xl bg-slate-50 border border-slate-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
              <input
                value={newAccount.password}
                onChange={(event) => setNewAccount((current) => ({ ...current, password: event.target.value }))}
                placeholder="学习通密码"
                type="password"
                className="w-full px-4 py-3 rounded-2xl bg-slate-50 border border-slate-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
              <button
                onClick={handleAddAccount}
                disabled={isAdding}
                className="w-full py-3.5 rounded-2xl bg-blue-600 text-white font-black flex items-center justify-center gap-2 disabled:opacity-50"
              >
                {isAdding && <Loader2 size={18} className="animate-spin" />}
                添加并同步课程
              </button>
            </div>
          </Modal>
        )}

        {showGroupForm && (
          <Modal
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
          </Modal>
        )}

        {memberGroup && (
          <Modal title={`设置成员：${memberGroup.name}`} onClose={() => setMemberGroup(null)}>
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
          </Modal>
        )}

        {syncGroup && (
          <Modal title={`同步班级：${syncGroup.name}`} onClose={() => setSyncGroup(null)}>
            <div className="space-y-4">
              <p className="text-xs text-slate-500 leading-relaxed">
                从源账号读取当前已选代签课程，并同步到该班级成员。若源账号就是当前正在编辑的账号，会先保存当前勾选结果。
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
          </Modal>
        )}

        {showAddCourse && (
          <Modal title="手动添加课程" onClose={() => setShowAddCourse(false)}>
            <div className="space-y-3">
              <div className="grid grid-cols-2 gap-2">
                <input
                  value={manualCourse.courseId}
                  onChange={(event) => setManualCourse((current) => ({ ...current, courseId: event.target.value }))}
                  placeholder="course_id"
                  inputMode="numeric"
                  className="w-full px-4 py-3 rounded-2xl bg-slate-50 border border-slate-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
                <input
                  value={manualCourse.classId}
                  onChange={(event) => setManualCourse((current) => ({ ...current, classId: event.target.value }))}
                  placeholder="class_id"
                  inputMode="numeric"
                  className="w-full px-4 py-3 rounded-2xl bg-slate-50 border border-slate-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
              <input
                value={manualCourse.name}
                onChange={(event) => setManualCourse((current) => ({ ...current, name: event.target.value }))}
                placeholder="课程名称，可选"
                className="w-full px-4 py-3 rounded-2xl bg-slate-50 border border-slate-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
              <input
                value={manualCourse.teacher}
                onChange={(event) => setManualCourse((current) => ({ ...current, teacher: event.target.value }))}
                placeholder="教师，可选"
                className="w-full px-4 py-3 rounded-2xl bg-slate-50 border border-slate-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
              <button
                onClick={handleAddManualCourse}
                disabled={isSaving}
                className="w-full py-3.5 rounded-2xl bg-slate-900 text-white font-black flex items-center justify-center gap-2 disabled:opacity-50"
              >
                {isSaving && <Loader2 size={18} className="animate-spin" />}
                添加到当前账号
              </button>
            </div>
          </Modal>
        )}

        {showCopyPanel && (
          <Modal title="一键套用课程" onClose={() => setShowCopyPanel(false)}>
            <div className="space-y-3">
              <p className="text-xs text-slate-500 leading-relaxed">
                将当前账号已勾选的 {selectedCourseKeys.length} 门课程添加并选中到目标账号。
              </p>
              <div className="max-h-72 overflow-y-auto space-y-2 pr-1 custom-scrollbar">
                {accounts.filter((account) => account.uid !== selectedUid).map((account) => {
                  const active = copyTargetUids.includes(account.uid);
                  return (
                    <button
                      key={account.uid}
                      onClick={() => toggleCopyTarget(account.uid)}
                      className={`w-full p-3 rounded-2xl border flex items-center justify-between text-left ${
                        active ? 'border-blue-500 bg-blue-50/40' : 'border-slate-100 bg-slate-50'
                      }`}
                    >
                      <div className="min-w-0">
                        <p className="font-bold text-slate-900 truncate">{account.name}</p>
                        <p className="text-[11px] text-slate-400">{account.mobile_masked}</p>
                      </div>
                      <div className={active ? 'text-blue-600' : 'text-slate-300'}>
                        {active ? <CheckCircle2 size={22} /> : <Circle size={22} />}
                      </div>
                    </button>
                  );
                })}
              </div>
              <button
                onClick={handleCopySelection}
                disabled={isCopying || copyTargetUids.length === 0}
                className="w-full py-3.5 rounded-2xl bg-blue-600 text-white font-black flex items-center justify-center gap-2 disabled:opacity-50"
              >
                {isCopying && <Loader2 size={18} className="animate-spin" />}
                套用到 {copyTargetUids.length} 个账号
              </button>
            </div>
          </Modal>
        )}
      </AnimatePresence>
    </div>
  );
};

const Modal = ({ title, children, onClose }: { title: string; children: ReactNode; onClose: () => void }) => (
  <motion.div
    initial={{ opacity: 0 }}
    animate={{ opacity: 1 }}
    exit={{ opacity: 0 }}
    className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/60 backdrop-blur-md"
    onClick={onClose}
  >
    <motion.div
      initial={{ y: '100%' }}
      animate={{ y: 0 }}
      exit={{ y: '100%' }}
      transition={{ type: 'spring', damping: 28, stiffness: 260 }}
      className="w-full max-w-[480px] bg-white rounded-t-[2rem] p-6 pb-[calc(24px+var(--sab))] shadow-2xl"
      onClick={(event) => event.stopPropagation()}
    >
      <div className="w-12 h-1.5 rounded-full bg-slate-200 mx-auto mb-5" />
      <div className="flex items-center justify-between mb-5">
        <h3 className="text-lg font-black text-slate-900">{title}</h3>
        <button onClick={onClose} className="w-9 h-9 rounded-full bg-slate-100 text-slate-500 flex items-center justify-center">
          <X size={18} />
        </button>
      </div>
      {children}
    </motion.div>
  </motion.div>
);

export default AdminPanel;
