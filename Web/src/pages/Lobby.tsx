import { useEffect, useState, useCallback, useMemo, useRef, type ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { motion, AnimatePresence, LayoutGroup } from 'framer-motion';
import {
  User as UserIcon,
  Users,
  Settings,
  ShieldCheck,
  RefreshCw,
  Clock,
  ChevronRight,
  ChevronDown,
  Activity,
  QrCode,
  MapPin,
  Camera,
  Fingerprint,
  BookOpen,
  CheckCircle2,
  RectangleEllipsis,
  Building2,
  BarChart3,
  Trophy,
  ScanFace,
  Upload,
  Trash2,
  X
} from 'lucide-react';
import toast from 'react-hot-toast';
import axios from 'axios';
import client, { withAccount, type RequestAccount } from '../api/client';
import { useAuthStore } from '../store/auth';
import { getChineseStringByDatetime } from '../utils/datetime';
import type { ApiResponse, CourseActivities, FeatureSettings } from '../types';
import PullToRefresh from '../components/PullToRefresh';

type PendingActivityEntry = {
  activity: CourseActivities['activities'][number];
  course: CourseActivities;
};

const RefreshIndicator = ({ spinning }: { spinning: boolean }) => {
  const rafRef = useRef<number | null>(null);
  const angleRef = useRef(0);
  const iconRef = useRef<SVGSVGElement>(null);

  const stopRaf = () => {
    if (rafRef.current !== null) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }
  };

  useEffect(() => {
    const renderAngle = () => {
      if (iconRef.current) iconRef.current.style.transform = `rotate(${angleRef.current}deg)`;
    };
    if (spinning) {
      stopRaf();
      let last = performance.now();
      const tick = (now: number) => {
        const delta = now - last;
        last = now;
        angleRef.current = (angleRef.current + delta * 0.36) % 360;
        renderAngle();
        rafRef.current = requestAnimationFrame(tick);
      };
      rafRef.current = requestAnimationFrame(tick);
      return () => stopRaf();
    }

    stopRaf();
    const current = ((angleRef.current % 360) + 360) % 360;
    if (current < 0.5) {
      angleRef.current = 0;
      renderAngle();
      return;
    }

    const remain = 360 - current;
    const duration = Math.max(140, Math.min(260, (remain / 360) * 260));
    const start = performance.now();
    const settle = (now: number) => {
      const t = Math.min((now - start) / duration, 1);
      const eased = 1 - Math.pow(1 - t, 3);
      const next = current + remain * eased;
      angleRef.current = next % 360;
      renderAngle();

      if (t < 1) {
        rafRef.current = requestAnimationFrame(settle);
      } else {
        angleRef.current = 0;
        renderAngle();
        rafRef.current = null;
      }
    };
    rafRef.current = requestAnimationFrame(settle);
    return () => stopRaf();
  }, [spinning]);

  useEffect(() => () => stopRaf(), []);

  return <RefreshCw ref={iconRef} size={20} />;
};

type QuickActionProps = {
  icon: ReactNode;
  label: string;
  onClick: () => void;
  tone: string;
  badge?: boolean;
  title?: string;
};

const QuickAction = ({ icon, label, onClick, tone, badge = false, title }: QuickActionProps) => (
  <motion.button
    type="button"
    whileTap={{ scale: 0.95 }}
    onClick={onClick}
    title={title || label}
    className="relative flex flex-col items-center justify-center gap-1.5 py-2.5 rounded-2xl bg-slate-50 border border-slate-100/70 active:bg-slate-100 transition-colors"
  >
    <span className={`w-9 h-9 rounded-xl flex items-center justify-center ${tone}`}>{icon}</span>
    <span className="text-[10px] font-bold text-slate-500 leading-none whitespace-nowrap">{label}</span>
    {badge && <span className="absolute top-2 right-2 w-2 h-2 rounded-full bg-red-500" />}
  </motion.button>
);

const Lobby = () => {
  const { user, activeUid, token } = useAuthStore();
  const navigate = useNavigate();
  const owner = useMemo<RequestAccount | null>(() => (
    activeUid !== null && token ? { uid: activeUid, token } : null
  ), [activeUid, token]);
  const mountedRef = useRef(false);
  const inFlightRef = useRef(false);
  const requestIdRef = useRef(0);
  const abortControllerRef = useRef<AbortController | null>(null);

  const [activities, setActivities] = useState<CourseActivities[]>(() => {
    const cached = localStorage.getItem(`cached_activities_${activeUid}`);
    return cached ? JSON.parse(cached) : [];
  });

  const [isLoading, setIsLoading] = useState(false);
  const [expandedCourses, setExpandedCourses] = useState<Record<string, boolean>>({});
  const [now, setNow] = useState(Date.now());
  const [pendingEntry, setPendingEntry] = useState<PendingActivityEntry | null>(null);

  const [contributionsEnabled, setContributionsEnabled] = useState(true);
  const [classAdminGroupId, setClassAdminGroupId] = useState(0);
  const [classAdminGroupName, setClassAdminGroupName] = useState('');
  const [showFacePanel, setShowFacePanel] = useState(false);
  const [hasFace, setHasFace] = useState<boolean | null>(null);
  const [faceDataUrl, setFaceDataUrl] = useState<string>('');
  const [isFaceLoading, setIsFaceLoading] = useState(false);
  const [isUploadingFace, setIsUploadingFace] = useState(false);
  const faceInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);

  const fetchActivities = useCallback(async () => {
    if (!owner || !mountedRef.current || inFlightRef.current) return;
    const account = useAuthStore.getState();
    if (account.activeUid !== owner.uid || account.token !== owner.token) return;

    inFlightRef.current = true;
    const requestId = ++requestIdRef.current;
    const controller = new AbortController();
    abortControllerRef.current = controller;
    const isCurrent = () => {
      const current = useAuthStore.getState();
      return mountedRef.current && requestIdRef.current === requestId && !controller.signal.aborted
        && current.activeUid === owner.uid && current.token === owner.token;
    };

    setIsLoading(true);
    try {
      const response = await client.get<ApiResponse<CourseActivities[]>>('/sign/activities', withAccount(owner, controller.signal));
      if (!isCurrent()) return;
      const data = response.data.data || [];
      setActivities(data);
      localStorage.setItem(`cached_activities_${owner.uid}`, JSON.stringify(data));
    } catch (error: unknown) {
      if (isCurrent() && !axios.isCancel(error)) {
        toast.error(error instanceof Error ? error.message : '获取签到活动失败');
      }
    } finally {
      if (isCurrent()) {
        abortControllerRef.current = null;
        inFlightRef.current = false;
        setIsLoading(false);
      }
    }
  }, [owner]);

  const fetchFaceFlag = useCallback(async () => {
    try {
      const res = await client.get<ApiResponse<{ has_face: boolean }>>('/user/face?status_only=1');
      setHasFace(res.data.data?.has_face ?? false);
    } catch {
      setHasFace(null);
    }
  }, []);

  const fetchFeatures = useCallback(async () => {
    if (!owner) return;
    try {
      const res = await client.get<ApiResponse<FeatureSettings>>('/features', withAccount(owner));
      const account = useAuthStore.getState();
      if (account.activeUid === owner.uid && account.token === owner.token) {
        setContributionsEnabled(res.data.data?.contributions_enabled !== false);
        setClassAdminGroupId(res.data.data?.class_admin_group_id ?? 0);
        setClassAdminGroupName(res.data.data?.class_admin_group_name || '');
      }
    } catch {
      // Keep the previous value when the feature endpoint is unavailable.
    }
  }, [owner]);

  const fetchFaceImage = useCallback(async () => {
    setIsFaceLoading(true);
    try {
      const res = await client.get<ApiResponse<{ has_face: boolean; face_data?: string }>>('/user/face');
      setHasFace(res.data.data?.has_face ?? false);
      setFaceDataUrl(res.data.data?.face_data || '');
    } catch {
      setFaceDataUrl('');
    } finally {
      setIsFaceLoading(false);
    }
  }, []);

  const handleFacePanelOpen = () => {
    setShowFacePanel(true);
    fetchFaceImage();
  };

  const handleFaceFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    e.target.value = '';

    if (!file.type.startsWith('image/')) {
      toast.error('请选择图片文件');
      return;
    }
    if (file.size > 20 * 1024 * 1024) {
      toast.error('图片不能超过 20MB');
      return;
    }

    setIsUploadingFace(true);
    try {
      const formData = new FormData();
      formData.append('file', file);
      const res = await client.post<ApiResponse<{ has_face: boolean; face_data?: string }>>('/user/face', formData);
      setHasFace(res.data.data?.has_face ?? true);
      setFaceDataUrl(res.data.data?.face_data || '');
      toast.success('人脸照片已保存');
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : '上传失败');
    } finally {
      setIsUploadingFace(false);
    }
  };

  useEffect(() => {
    mountedRef.current = true;
    void fetchActivities();
    void fetchFaceFlag();
    void fetchFeatures();
    return () => {
      mountedRef.current = false;
      requestIdRef.current += 1;
      abortControllerRef.current?.abort();
      abortControllerRef.current = null;
      inFlightRef.current = false;
    };
  }, [fetchActivities, fetchFaceFlag, fetchFeatures]);

  const toggleCourse = (courseId: number, classId: number) => {
    const key = `${courseId}-${classId}`;
    setExpandedCourses(prev => ({
      ...prev,
      [key]: !prev[key]
    }));
  };

  const getSignTypeIcon = (activity: CourseActivities['activities'][number]) => {
    if (activity.sign_type === 0 && activity.if_photo) return <Camera size={18} />;

    switch (activity.sign_type) {
      case 2: return <QrCode size={18} />;
      case 3: return <Fingerprint size={18} />;
      case 4: return <MapPin size={18} />;
      case 5: return <RectangleEllipsis size={18} />;
      default: return <CheckCircle2 size={18} />;
    }
  };

  const getSignTypeName = (activity: CourseActivities['activities'][number]) => {
    if (activity.sign_type === 0 && activity.if_photo) return '拍照';

    switch (activity.sign_type) {
      case 2: return '二维码';
      case 3: return '手势';
      case 4: return '位置';
      case 5: return '签到码';
      default: return '普通';
    }
  };

  const getSignState = (source: number, name: string) => {
    if (source === -1) return '学习通签到';
    if (source === user?.uid) return `本人签到`;
    return `${name}代签`;
  };

  const enterActivity = (activity: CourseActivities['activities'][number], course: CourseActivities) => {
    navigate(`/sign/${activity.active_id}`, { state: { activity, course } });
  };

  const handleActivityClick = (activity: CourseActivities['activities'][number], course: CourseActivities, shouldHighlight: boolean) => {
    if (shouldHighlight) {
      enterActivity(activity, course);
      return;
    }

    setPendingEntry({ activity, course });
  };

  return (
    <div className="flex-1 flex flex-col bg-slate-50 relative overflow-hidden">
      {/* Header */}
      <div className="bg-white sticky top-0 z-10 border-b border-slate-100 px-4 h-[calc(88px+var(--sat))] pt-[var(--sat)] flex items-center justify-between gap-3 shrink-0">
        <motion.div
          whileTap={{ scale: 0.92 }}
          onClick={() => navigate('/accounts')}
          className="flex items-center space-x-3 min-w-0 flex-1 cursor-pointer hover:opacity-80 transition-opacity"
        >
          <div className="w-12 h-12 rounded-xl bg-slate-100 overflow-hidden border-2 border-white shadow-sm shrink-0">
            {user?.avatar ? (
              <img src={user.avatar} alt={user.name} referrerPolicy="no-referrer" className="w-full h-full object-cover" />
            ) : (
              <div className="w-full h-full flex items-center justify-center text-slate-400">
                <UserIcon size={24} />
              </div>
            )}
          </div>
          <div className="min-w-0">
            <h2 className="font-bold text-slate-900 flex items-center min-w-0">
              <span className="truncate">{user?.name || '未登录'}</span>
              <ChevronRight size={14} className="ml-1 text-slate-400 shrink-0" />
            </h2>
            <p className="text-xs text-slate-500 truncate">{user?.mobile}</p>
          </div>
        </motion.div>
        <motion.button
          whileTap={{ scale: 0.92 }}
          onClick={fetchActivities}
          className="w-10 h-10 flex items-center justify-center text-slate-600 hover:bg-slate-50 rounded-xl transition-colors shrink-0"
          title="刷新活动"
        >
          <RefreshIndicator spinning={isLoading} />
        </motion.button>
      </div>

      {/* Main Content */}
      <PullToRefresh
        onRefresh={fetchActivities}
        isRefreshing={isLoading}
        className="p-4"
      >
        <div className="pb-[calc(80px+var(--sab))] space-y-4">
          <section className="rounded-[1.75rem] bg-white border border-slate-100 shadow-sm p-3">
            <div className="flex items-center justify-between px-1 mb-2">
              <h3 className="text-[10px] font-black text-slate-400 tracking-[0.16em] uppercase">快捷功能</h3>
            </div>
            <div className="grid grid-cols-4 gap-2">
              <QuickAction
                icon={<Settings size={18} />}
                label="课程配置"
                tone="bg-blue-50 text-blue-600"
                onClick={() => navigate('/courses')}
                title="课程配置"
              />
              <QuickAction
                icon={<BarChart3 size={18} />}
                label="学习面板"
                tone="bg-violet-50 text-violet-600"
                onClick={() => navigate('/learning')}
                title="学习仪表盘"
              />
              <QuickAction
                icon={<Building2 size={18} />}
                label="在寝签到"
                tone="bg-cyan-50 text-cyan-600"
                onClick={() => navigate('/room-check')}
                title="宿舍在寝签到"
              />
              <QuickAction
                icon={<ScanFace size={18} />}
                label="人脸照片"
                tone={hasFace === false ? 'bg-rose-50 text-rose-600' : 'bg-amber-50 text-amber-600'}
                badge={hasFace === false}
                onClick={handleFacePanelOpen}
                title={hasFace === false ? '人脸照片（未上传）' : '人脸照片'}
              />
              {contributionsEnabled && (
                <QuickAction
                  icon={<Trophy size={18} />}
                  label="贡献榜"
                  tone="bg-amber-50 text-amber-600"
                  onClick={() => navigate('/contributions')}
                  title="贡献榜"
                />
              )}
              {classAdminGroupId > 0 && (
                <QuickAction
                  icon={<Users size={18} />}
                  label="班级管理"
                  tone="bg-emerald-50 text-emerald-600"
                  onClick={() => navigate('/class-admin')}
                  title={classAdminGroupName ? `班级管理：${classAdminGroupName}` : '班级管理'}
                />
              )}
              {user && user.permission >= 2 && (
                <QuickAction
                  icon={<ShieldCheck size={18} />}
                  label="管理面板"
                  tone="bg-slate-100 text-slate-700"
                  onClick={() => navigate('/admin/panel')}
                  title="管理面板"
                />
              )}
            </div>
          </section>
          {isLoading && activities.length === 0 ? (
            <div className="space-y-4">
              {[1, 2, 3].map((i) => (
                <div key={i} className="h-20 bg-slate-50 rounded-2xl animate-pulse" />
              ))}
            </div>
          ) : !isLoading && activities.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-slate-400 bg-slate-50 rounded-2xl border border-dashed border-slate-200">
              <Clock size={48} className="mb-4 opacity-20" />
              <p>暂无正在进行的签到</p>
            </div>
          ) : (
            <LayoutGroup>
              {activities.map((course) => {
                const key = `${course.course_id}-${course.class_id}`;
                const isExpanded = expandedCourses[key];
                const activeCount = course.activities.filter(a =>
                  now < a.end_time && !a.record_source_name
                ).length;

                // Find the latest activity time for the header
                const latestActivityTime = course.activities.length > 0
                  ? Math.max(...course.activities.map(a => a.start_time))
                  : null;

                return (
                  <motion.div
                    layout
                    key={key}
                    className="bg-white rounded-3xl border border-slate-100 shadow-sm overflow-hidden"
                  >
                    {/* Course Header */}
                    <motion.div
                      layout="position"
                      onClick={() => toggleCourse(course.course_id, course.class_id)}
                      className={`p-4 flex items-center justify-between cursor-pointer active:bg-slate-50 transition-colors ${isExpanded ? 'bg-slate-50/50' : ''}`}
                    >
                      <div className="flex items-center space-x-4 flex-1 min-w-0">
                        <div className="relative flex-shrink-0">
                          <div className="w-12 h-12 rounded-xl bg-slate-100 overflow-hidden">
                            {course.icon ? (
                              <img src={course.icon} referrerPolicy="no-referrer" className="w-full h-full object-cover" />
                            ) : (
                              <div className="w-full h-full flex items-center justify-center text-slate-300">
                                <BookOpen size={20} />
                              </div>
                            )}
                          </div>
                          {activeCount > 0 && (
                            <div className="absolute -top-1.5 -right-1.5 min-w-[18px] h-[18px] bg-red-500 rounded-full flex items-center justify-center text-[13px] text-white font-bold px-0.5 z-10 shadow-sm">
                              {activeCount}
                            </div>
                          )}
                        </div>
                        <div className="flex-1 min-w-0">
                          <h3 className="font-bold text-slate-900 leading-tight truncate">{course.course_name}</h3>
                          <div className="flex items-center space-x-2 mt-1">
                            <p className="text-xs text-slate-500 truncate flex-1">{course.course_teacher}</p>
                            {latestActivityTime && !isExpanded && (
                              <span className={`text-[10px] font-medium whitespace-nowrap flex-shrink-0 ${activeCount > 0 ? 'text-blue-600' : 'text-slate-400'}`}>
                                {getChineseStringByDatetime(latestActivityTime)}
                              </span>
                            )}
                          </div>
                        </div>
                      </div>
                      <motion.div
                        animate={{ rotate: isExpanded ? 180 : 0 }}
                        transition={{ duration: 0.2 }}
                        className="text-slate-300 flex items-center ml-2 flex-shrink-0"
                      >
                        <ChevronDown size={20} />
                      </motion.div>
                    </motion.div>

                    {/* Activities List */}
                    <AnimatePresence initial={false}>
                      {isExpanded && (
                        <motion.div
                          initial={{ height: 0, opacity: 0 }}
                          animate={{ height: 'auto', opacity: 1 }}
                          exit={{ height: 0, opacity: 0 }}
                          transition={{ duration: 0.2, ease: "circOut" }}
                          className="overflow-hidden"
                        >
                          <div className="px-4 pb-4 space-y-2 border-t border-slate-50 pt-3">
                            {course.activities.length > 0 ? (
                              course.activities.map((activity) => {
                                const isOngoing = now < activity.end_time;
                                const isFinished = !!activity.record_source_name;
                                const shouldHighlight = isOngoing && !isFinished;

                                // Countdown logic
                                let countdownStr = "";
                                if (shouldHighlight) {
                                  const diff = Math.max(0, activity.end_time - now);
                                  const mins = Math.floor(diff / 60000);
                                  const secs = Math.floor((diff % 60000) / 1000);
                                  countdownStr = `${mins}:${secs.toString().padStart(2, '0')}`;
                                }

                                return (
                                  <motion.div
                                    key={activity.active_id}
                                    layout
                                    whileTap={{ scale: 0.92 }}
                                    onClick={() => handleActivityClick(activity, course, shouldHighlight)}
                                    className={`flex items-center justify-between p-3 rounded-2xl border transition-all group cursor-pointer ${shouldHighlight
                                      ? 'bg-purple-50 border-purple-600'
                                      : 'bg-blue-50/50 border-blue-100/50 hover:bg-blue-50'
                                      }`}
                                  >
                                    <div className="flex items-center space-x-3 flex-1 min-w-0">
                                      <div className={`w-10 h-10 rounded-xl flex items-center justify-center shadow-sm flex-shrink-0 bg-white ${shouldHighlight ? 'text-purple-600' : 'text-blue-600'
                                        }`}>
                                        {getSignTypeIcon(activity)}
                                      </div>
                                      <div className="flex-1 min-w-0">
                                        <div className={`font-bold text-sm truncate text-slate-900`}>{activity.activity_name}</div>
                                          <div className="flex items-center space-x-2 mt-0.5 overflow-hidden">
                                            <span className={`text-[10px] px-1.5 py-0.5 bg-blue-100 text-blue-700 rounded-md font-medium uppercase flex-shrink-0`}>
                                              {getSignTypeName(activity)}
                                            </span>
                                            {activity.if_face && (
                                              <span className="text-[10px] px-1.5 py-0.5 bg-orange-100 text-orange-700 rounded-md font-medium flex-shrink-0 flex items-center gap-0.5">
                                                <ScanFace size={10} />人脸
                                              </span>
                                            )}
                                          {shouldHighlight && (
                                            <span className="text-[10px] px-1.5 py-0.5 bg-purple-100 text-purple-700 rounded-md font-medium whitespace-nowrap flex-shrink-0">
                                              进行中 {countdownStr}
                                            </span>
                                          )}
                                          {activity.record_source_name && (
                                            <span className={`text-[10px] px-1.5 py-0.5 rounded-md font-medium truncate flex-shrink min-w-0 bg-green-100 text-green-700`}>
                                              {getSignState(activity.record_source, activity.record_source_name)}
                                            </span>
                                          )}
                                          <span className={`text-[10px] flex items-center flex-shrink-0 text-slate-500`}>
                                            <Clock size={10} className="mr-1" />
                                            {getChineseStringByDatetime(activity.start_time)}
                                          </span>
                                        </div>
                                      </div>
                                    </div>
                                    <ChevronRight size={18} className={`text-slate-300 group-hover:text-blue-500 transition-colors ml-2 flex-shrink-0`} />
                                  </motion.div>
                                );
                              })
                            ) : (
                              <div className="py-8 flex flex-col items-center justify-center text-slate-300">
                                <Activity size={32} className="mb-2 opacity-20" />
                                <p className="text-xs font-medium">暂无签到活动</p>
                              </div>
                            )}
                            {course.has_more && (
                              <div className="text-center pt-1">
                                <p className="text-[13px] text-slate-400 font-medium">
                                  仅显示最近 {course.activities.length} 条活动
                                </p>
                              </div>
                            )}
                          </div>
                        </motion.div>
                      )}
                    </AnimatePresence>
                  </motion.div>
                );
              })}
            </LayoutGroup>
          )}
        </div>
      </PullToRefresh>

      <AnimatePresence>
        {pendingEntry && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/45 backdrop-blur-sm p-6"
            onClick={() => setPendingEntry(null)}
          >
            <motion.div
              initial={{ opacity: 0, scale: 0.95, y: 12 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.95, y: 12 }}
              transition={{ type: 'spring', stiffness: 320, damping: 26 }}
              className="w-full max-w-sm rounded-[2rem] bg-white p-6 shadow-2xl border border-slate-100"
              onClick={(e) => e.stopPropagation()}
            >
              <div className="space-y-3">
                <div>
                  <p className="text-lg font-black text-slate-900">温馨提示</p>
                  <p className="text-sm text-slate-500 mt-1 leading-6">
                    当前点击的签到活动（{pendingEntry.activity.activity_name}）已结束或已完成，仍要进入详情页吗？
                  </p>
                </div>
                <div className="flex items-center gap-3 pt-2">
                  <button
                    onClick={() => {
                      enterActivity(pendingEntry.activity, pendingEntry.course);
                      setPendingEntry(null);
                    }}
                    className="flex-1 rounded-xl bg-slate-100 px-4 py-3 text-sm font-bold text-slate-600 transition-colors hover:bg-slate-200"
                  >
                    确认进入
                  </button>
                  <button
                    onClick={() => setPendingEntry(null)}
                    className="flex-1 rounded-xl bg-blue-600 px-4 py-3 text-sm font-bold text-white shadow-lg shadow-blue-100 transition-colors hover:bg-blue-700"
                  >
                    取消
                  </button>
                </div>
              </div>
            </motion.div>
          </motion.div>
        )}
        {showFacePanel && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/60 backdrop-blur-md p-0"
            onClick={() => setShowFacePanel(false)}
          >
            <motion.div
              initial={{ y: '100%' }}
              animate={{ y: 0 }}
              exit={{ y: '100%' }}
              transition={{ type: 'spring', damping: 28, stiffness: 250 }}
              className="bg-white w-full max-w-[480px] rounded-t-[3rem] p-8 shadow-2xl"
              onClick={(e) => e.stopPropagation()}
            >
              <div className="w-12 h-1.5 bg-slate-200 rounded-full mx-auto mb-6" />
              <div className="flex items-center justify-between mb-6">
                <h3 className="text-xl font-bold text-slate-900">人脸照片</h3>
                <button
                  onClick={() => setShowFacePanel(false)}
                  className="w-8 h-8 flex items-center justify-center bg-slate-100 text-slate-400 rounded-full"
                >
                  <X size={16} />
                </button>
              </div>

              <div className="flex flex-col items-center gap-5 pb-[calc(32px+var(--sab))]">
                <div className="w-28 h-28 rounded-3xl bg-slate-100 border-2 border-dashed border-slate-200 flex items-center justify-center overflow-hidden">
                  {isFaceLoading ? (
                    <div className="w-8 h-8 border-2 border-slate-300 border-t-blue-500 rounded-full animate-spin" />
                  ) : faceDataUrl ? (
                    <img src={faceDataUrl} className="w-full h-full object-cover" />
                  ) : (
                    <div className="flex flex-col items-center gap-1">
                      <ScanFace size={36} className="text-slate-300" />
                      <span className="text-[10px] text-slate-400 font-medium">未设置</span>
                    </div>
                  )}
                </div>

                <div className="text-center">
                  <p className="text-sm font-bold text-slate-700">
                    {faceDataUrl ? '人脸照片已设置' : '尚未设置人脸照片'}
                  </p>
                  <p className="text-xs text-slate-400 mt-1 leading-5">
                    {faceDataUrl
                      ? '需要人脸验证的签到将自动使用此照片。点击下方按钮可重新上传。'
                      : '上传后，需要人脸验证的签到将自动使用此照片完成验证。'}
                  </p>
                </div>

                <input
                  ref={faceInputRef}
                  type="file"
                  accept="image/*"
                  className="hidden"
                  onChange={handleFaceFileChange}
                />

                <div className="flex gap-3 w-full">
                  <button
                    onClick={() => {
                      setShowFacePanel(false);
                      navigate('/face-capture');
                    }}
                    className="flex-1 flex items-center justify-center gap-2 py-3 rounded-2xl bg-blue-600 text-white font-bold text-sm shadow-lg shadow-blue-100 transition-all active:scale-[0.97]"
                  >
                    <Camera size={16} />
                    拍照上传
                  </button>
                  <button
                    onClick={() => faceInputRef.current?.click()}
                    disabled={isUploadingFace}
                    className="flex-1 flex items-center justify-center gap-2 py-3 rounded-2xl bg-slate-100 text-slate-700 font-bold text-sm disabled:opacity-50 transition-all active:scale-[0.97]"
                  >
                    {isUploadingFace ? (
                      <div className="w-4 h-4 border-2 border-slate-400 border-t-transparent rounded-full animate-spin" />
                    ) : (
                      <Upload size={16} />
                    )}
                    相册选取
                  </button>
                  {faceDataUrl && (
                    <button
                      onClick={async () => {
                        try {
                          await client.delete('/user/face');
                          setFaceDataUrl('');
                          toast.success('已清除人脸照片');
                        } catch (err: unknown) {
                          toast.error(err instanceof Error ? err.message : '清除失败');
                        }
                      }}
                      className="w-12 flex items-center justify-center rounded-2xl bg-slate-100 text-slate-500 transition-all active:scale-[0.97]"
                    >
                      <Trash2 size={16} />
                    </button>
                  )}
                </div>
              </div>
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
};

export default Lobby;
