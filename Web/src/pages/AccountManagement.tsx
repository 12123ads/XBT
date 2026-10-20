import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { motion, AnimatePresence } from 'framer-motion';
import { 
  ChevronLeft, 
  Loader2,
  LogOut, 
  QrCode,
  Trash2, 
  User as UserIcon,
  Plus,
  RefreshCw
} from 'lucide-react';
import { useAuthStore } from '../store/auth';
import { getNativeUpdateBridge, readNativeAppVersion } from '../utils/nativeUpdate';
import { getMyFakerExport, type FakerExportSingle } from '../api/fakerExport';
import FakerExportDialog from '../components/FakerExportDialog';
import toast from 'react-hot-toast';

type ExportDialogState = {
  uid: number;
  name: string;
  phone: string;
  data: FakerExportSingle;
};

const AccountManagement = () => {
  const navigate = useNavigate();
  const { accounts, activeUid, user, switchAccount, removeAccount } = useAuthStore();
  const updateBridge = useMemo(() => getNativeUpdateBridge(), []);
  const [appVersion] = useState(() => readNativeAppVersion());
  const [isExporting, setIsExporting] = useState(false);
  const [exportDialog, setExportDialog] = useState<ExportDialogState | null>(null);

  const sortedAccounts = useMemo(() => {
    return [...accounts].sort((a, b) => {
      if (a.user.uid === activeUid) return -1;
      if (b.user.uid === activeUid) return 1;
      return 0;
    });
  }, [accounts, activeUid]);

  const handleSwitch = (uid: number) => {
    if (uid === activeUid) return;
    switchAccount(uid);
    toast.success('已切换账号');
    navigate('/');
  };

  const handleCheckUpdate = () => {
    const bridge = getNativeUpdateBridge();
    if (!bridge?.checkForUpdate) {
      toast.error('当前环境不支持应用内更新');
      return;
    }
    bridge.checkForUpdate();
    toast.success('正在检查更新…');
  };

  const handleRemove = (e: React.MouseEvent, uid: number) => {
    e.stopPropagation();
    if (!confirm('确定要移除此账号吗？')) return;
    removeAccount(uid);
    toast.success('已移除账号');
  };

  const handleExport = async () => {
    if (!user || isExporting) return;
    setIsExporting(true);
    try {
      const data = await getMyFakerExport();
      setExportDialog({
        uid: user.uid,
        name: user.name,
        phone: user.mobile,
        data,
      });
    } catch (error: unknown) {
      toast.error(error instanceof Error ? error.message : '导出到 CSF 失败，请稍后重试');
    } finally {
      setIsExporting(false);
    }
  };

  return (
    <div className="flex-1 flex flex-col bg-slate-50 relative overflow-hidden">
      {/* Header */}
      <div className="bg-white sticky top-0 z-10 border-b border-slate-100 px-4 h-[calc(80px+var(--sat))] pt-[var(--sat)] flex items-center shrink-0">
        <div className="flex items-center">
          <button 
            onClick={() => navigate(-1)} 
            className="p-2 -ml-2 text-slate-600 hover:bg-slate-50 rounded-lg transition-colors"
          >
            <ChevronLeft size={24} />
          </button>
          <h2 className="ml-2 font-bold text-slate-900 text-lg">账号管理</h2>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-4 space-y-4 pb-[calc(40px+var(--sab))] custom-scrollbar">
        <div className="text-xs font-semibold text-slate-400 uppercase tracking-wider px-1">
          已保存的账号
        </div>

        <div className="space-y-3">
          <AnimatePresence initial={false}>
            {sortedAccounts.map((account) => (
              <motion.div
                key={account.user.uid}
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, scale: 0.95 }}
                whileTap={{ scale: 0.92 }}
                onClick={() => handleSwitch(account.user.uid)}
                className={`p-4 rounded-3xl border transition-all cursor-pointer flex items-center justify-between group ${
                  account.user.uid === activeUid 
                    ? 'border-blue-500 bg-blue-50/30 shadow-sm' 
                    : 'bg-white border-slate-100 hover:border-slate-200 shadow-sm'
                }`}
              >
                <div className="flex items-center space-x-4">
                  <div className="w-12 h-12 rounded-xl bg-slate-100 overflow-hidden border-2 border-white shadow-sm">
                    {account.user.avatar ? (
                      <img src={account.user.avatar} alt={account.user.name} referrerPolicy="no-referrer" className="w-full h-full object-cover" />
                    ) : (
                      <div className="w-full h-full flex items-center justify-center text-slate-400">
                        <UserIcon size={24} />
                      </div>
                    )}
                  </div>
                  <div>
                    <div className="font-bold text-slate-900 flex items-center">
                      {account.user.name}
                      {account.user.uid === activeUid && (
                        <span className="ml-2 px-1.5 py-0.5 bg-blue-600 text-[10px] text-white rounded-md font-medium uppercase">
                          当前
                        </span>
                      )}
                    </div>
                    <div className="text-xs text-slate-500 mt-0.5">{account.user.mobile}</div>
                  </div>
                </div>

                <div className="flex items-center space-x-2">
                  <button
                    onClick={(e) => handleRemove(e, account.user.uid)}
                    className={`w-8 h-8 flex items-center justify-center rounded-full transition-all ${
                      account.user.uid === activeUid 
                        ? 'text-red-500 hover:bg-red-50' 
                        : 'text-slate-300 hover:text-red-500 hover:bg-red-50'
                    }`}
                    title={account.user.uid === activeUid ? "退出登录" : "移除账号"}
                  >
                    {account.user.uid === activeUid ? <LogOut size={18} /> : <Trash2 size={18} />}
                  </button>
                </div>
              </motion.div>
            ))}
          </AnimatePresence>

          <motion.button
            whileTap={{ scale: 0.92 }}
            onClick={() => navigate('/login')}
            className="w-full p-4 border-2 border-dashed border-slate-200 rounded-3xl flex items-center justify-center space-x-2 text-slate-500 hover:border-blue-300 hover:text-blue-600 hover:bg-blue-50/30 transition-all"
          >
            <Plus size={20} />
            <span className="font-bold">添加新账号</span>
          </motion.button>
        </div>

        {user && (
          <div className="p-4 bg-white rounded-3xl border border-slate-100 shadow-sm">
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0">
                <div className="flex items-center gap-2 font-bold text-slate-900">
                  <QrCode size={18} className="text-blue-600" />
                  CSF 导入
                </div>
                <p className="mt-1 text-xs leading-relaxed text-slate-500">
                  将当前账号 {user.name} 导入 ChaoxingSignFaker，二维码仅在本次页面显示。
                </p>
              </div>
              <button
                type="button"
                onClick={() => void handleExport()}
                disabled={isExporting}
                className="flex h-10 shrink-0 items-center gap-1.5 rounded-full bg-blue-600 px-4 text-xs font-black text-white active:scale-95 transition-transform disabled:opacity-50"
              >
                {isExporting ? <Loader2 size={16} className="animate-spin" /> : <QrCode size={16} />}
                <span>导出到 CSF</span>
              </button>
            </div>
          </div>
        )}

        {updateBridge && (
          <div className="p-4 bg-white rounded-3xl border border-slate-100 shadow-sm flex items-center justify-between">
            <div>
              <div className="font-bold text-slate-900">应用更新</div>
              <div className="text-xs text-slate-500 mt-0.5">
                {appVersion
                  ? `当前版本 ${appVersion.versionName}（${appVersion.versionCode}）`
                  : '当前版本未知'}
              </div>
            </div>
            <button
              onClick={handleCheckUpdate}
              className="flex items-center space-x-1 px-3 h-9 rounded-full bg-blue-50 text-blue-600 font-bold text-sm active:scale-95 transition-transform"
            >
              <RefreshCw size={16} />
              <span>检查更新</span>
            </button>
          </div>
        )}

        <div className="pt-8 text-center pb-8">
          <p className="text-xs text-slate-400 leading-relaxed px-8">
            移除账号仅会从本地清除登录状态，不会影响您的学习通账号数据。
          </p>
        </div>
      </div>
      <AnimatePresence>
        {exportDialog && (
          <FakerExportDialog
            uid={exportDialog.uid}
            name={exportDialog.name}
            phone={exportDialog.phone}
            data={exportDialog.data}
            onClose={() => setExportDialog(null)}
          />
        )}
      </AnimatePresence>
      <style>{`.custom-scrollbar::-webkit-scrollbar { width: 0px; }`}</style>
    </div>
  );
};

export default AccountManagement;
