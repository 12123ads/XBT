import { useNavigate } from 'react-router-dom';
import { BookOpen, CheckCircle2, ChevronLeft, GraduationCap, ShieldCheck, Users } from 'lucide-react';

const entries = [
  {
    title: '账号课程',
    description: '添加账号、同步课程、设置代签课程和套用课程。',
    path: '/admin/accounts',
    icon: Users,
    tone: 'bg-blue-600',
  },
  {
    title: '班级分组',
    description: '维护班级成员，并按班级同步课程设置。',
    path: '/admin/class-groups',
    icon: GraduationCap,
    tone: 'bg-slate-900',
  },
  {
    title: '签到记录',
    description: '查询 XBT 执行成功的签到记录和合并结果。',
    path: '/admin/sign-records',
    icon: CheckCircle2,
    tone: 'bg-emerald-600',
  },
  {
    title: '白名单管理',
    description: '维护允许登录的手机号和管理员权限。',
    path: '/admin/whitelist',
    icon: ShieldCheck,
    tone: 'bg-amber-600',
  },
];

const AdminPanel = () => {
  const navigate = useNavigate();

  return (
    <div className="h-full min-h-0 flex flex-col bg-slate-50 overflow-hidden">
      <div className="bg-white border-b border-slate-100 px-4 h-[calc(80px+var(--sat))] pt-[var(--sat)] flex items-center justify-between shrink-0">
        <div className="flex items-center min-w-0">
          <button onClick={() => navigate(-1)} className="p-2 -ml-2 text-slate-600 hover:bg-slate-50 rounded-lg">
            <ChevronLeft size={24} />
          </button>
          <div className="ml-2 min-w-0">
            <h2 className="font-bold text-slate-900 text-lg truncate">管理面板</h2>
            <p className="text-[10px] text-slate-400 font-bold truncate">选择一个管理模块继续</p>
          </div>
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto p-4 pb-[calc(32px+var(--sab))] custom-scrollbar">
        <div className="rounded-[2rem] bg-slate-900 p-5 text-white shadow-sm mb-4 overflow-hidden relative">
          <BookOpen size={72} className="absolute -right-4 -bottom-4 text-white/10" />
          <p className="text-xs font-bold text-blue-200">Admin Console</p>
          <h3 className="mt-2 text-2xl font-black">把管理拆开，别让一个页面变成长卷轴。</h3>
          <p className="mt-3 text-sm text-slate-300 leading-relaxed">
            账号、班级和记录现在分别进入二级页面，数据多时只滚动当前模块。
          </p>
        </div>

        <div className="grid grid-cols-1 gap-3">
          {entries.map((entry) => {
            const Icon = entry.icon;
            return (
              <button
                key={entry.path}
                onClick={() => navigate(entry.path)}
                className="w-full rounded-[1.5rem] border border-slate-100 bg-white p-4 text-left shadow-sm active:scale-[0.99] transition-transform flex items-center gap-4"
              >
                <div className={`w-12 h-12 rounded-2xl ${entry.tone} text-white flex items-center justify-center shrink-0`}>
                  <Icon size={22} />
                </div>
                <div className="min-w-0 flex-1">
                  <p className="font-black text-slate-900">{entry.title}</p>
                  <p className="mt-1 text-xs text-slate-400 leading-relaxed">{entry.description}</p>
                </div>
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
};

export default AdminPanel;
