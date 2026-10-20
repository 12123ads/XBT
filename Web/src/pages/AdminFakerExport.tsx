import { useCallback, useEffect, useRef, useState } from 'react';
import { AlertTriangle, Download, Loader2, QrCode, RefreshCw } from 'lucide-react';
import toast from 'react-hot-toast';
import AdminShell from '../components/admin/AdminShell';
import FakerExportQrCard from '../components/FakerExportQrCard';
import {
  downloadFakerExportZip,
  getFakerExportBatch,
  type FakerExportBatch,
} from '../api/fakerExport';

const getErrorMessage = (error: unknown, fallback: string) => (
  error instanceof Error ? error.message : fallback
);

const AdminFakerExport = () => {
  const [data, setData] = useState<FakerExportBatch | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isDownloading, setIsDownloading] = useState(false);
  const [errorMessage, setErrorMessage] = useState('');
  const requestIdRef = useRef(0);

  const loadExport = useCallback(async () => {
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    setIsLoading(true);
    setErrorMessage('');
    try {
      const next = await getFakerExportBatch();
      if (requestIdRef.current !== requestId) return;
      setData(next);
    } catch (error: unknown) {
      if (requestIdRef.current !== requestId) return;
      const message = getErrorMessage(error, '获取 CSF 导出数据失败');
      setData(null);
      setErrorMessage(message);
      toast.error(message);
    } finally {
      if (requestIdRef.current === requestId) setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadExport();
    return () => {
      requestIdRef.current += 1;
    };
  }, [loadExport]);

  const handleDownload = async () => {
    if (isDownloading) return;
    setIsDownloading(true);
    try {
      await downloadFakerExportZip();
      toast.success('ZIP 已开始下载');
    } catch (error: unknown) {
      toast.error(getErrorMessage(error, '下载 ZIP 失败'));
    } finally {
      setIsDownloading(false);
    }
  };

  const items = data?.items ?? [];
  const readyCount = items.filter((item) => item.url && item.qr_png).length;
  const summary = !data
    ? '加载成功后可下载全部二维码与链接清单。'
    : readyCount === items.length
      ? `共 ${items.length} 个账号，二维码全部直接显示。`
      : `共 ${items.length} 个账号，${readyCount} 张二维码已生成，其余显示失败原因。`;

  return (
    <AdminShell
      title="导出到 CSF"
      subtitle="每个账号一张二维码，扫码导入 ChaoxingSignFaker"
      action={(
        <button
          type="button"
          onClick={() => void loadExport()}
          disabled={isLoading}
          className="flex h-10 w-10 items-center justify-center rounded-xl bg-slate-50 text-blue-600 disabled:opacity-40"
          title="刷新导出数据"
        >
          <RefreshCw size={18} className={isLoading ? 'animate-smooth-spin' : ''} />
        </button>
      )}
    >
      <div className="flex-1 min-h-0 overflow-y-auto p-4 pb-[calc(16px+var(--sab))] space-y-4 custom-scrollbar">
        <section className="rounded-[1.75rem] bg-slate-900 p-5 text-white shadow-sm overflow-hidden relative">
          <QrCode size={72} className="absolute -right-3 -bottom-3 text-white/10" />
          <p className="text-xs font-bold text-blue-200">CSF Import</p>
          <h3 className="mt-1 text-xl font-black">批量生成导入二维码</h3>
          <p className="mt-2 text-xs leading-relaxed text-slate-300">
            {isLoading ? '正在生成二维码…' : summary}
          </p>
          <button
            type="button"
            onClick={() => void handleDownload()}
            disabled={isLoading || isDownloading || readyCount === 0}
            className="mt-4 flex h-11 w-full items-center justify-center gap-2 rounded-2xl bg-white text-sm font-black text-slate-900 active:scale-[0.99] transition-transform disabled:opacity-40"
          >
            {isDownloading ? <Loader2 size={18} className="animate-spin" /> : <Download size={18} />}
            下载 ZIP
          </button>
          <p className="mt-2 text-center text-[10px] text-slate-400">
            内含每个账号的二维码 PNG 与 links.txt
          </p>
        </section>

        <div className="flex items-start gap-2 rounded-2xl border border-amber-200 bg-amber-50 px-3 py-2.5">
          <AlertTriangle size={16} className="mt-0.5 shrink-0 text-amber-600" />
          <p className="text-[11px] font-bold leading-relaxed text-amber-700">
            该二维码/链接等同账号密码，请勿截图外传。本页不缓存数据，刷新后需重新生成。
          </p>
        </div>

        {isLoading ? (
          <div className="space-y-3">
            {[1, 2, 3].map((item) => (
              <div key={item} className="h-72 rounded-[1.75rem] bg-white border border-slate-100 animate-pulse" />
            ))}
          </div>
        ) : errorMessage ? (
          <div className="rounded-[1.75rem] border border-slate-100 bg-white p-6 text-center shadow-sm">
            <p className="text-sm font-bold text-slate-900">无法生成导出二维码</p>
            <p className="mt-2 text-xs leading-relaxed text-slate-500">{errorMessage}</p>
            <button
              type="button"
              onClick={() => void loadExport()}
              className="mt-4 rounded-2xl bg-blue-600 px-5 py-2.5 text-xs font-black text-white active:scale-95 transition-transform"
            >
              重试
            </button>
          </div>
        ) : items.length === 0 ? (
          <div className="rounded-[1.75rem] border border-dashed border-slate-200 bg-slate-50 p-8 text-center">
            <QrCode size={40} className="mx-auto text-slate-300" />
            <p className="mt-3 text-sm font-bold text-slate-500">暂无可导出的账号</p>
          </div>
        ) : (
          <div className="space-y-4">
            {items.map((item) => (
              <FakerExportQrCard
                key={item.uid}
                uid={item.uid}
                name={item.name}
                phone={item.phone}
                url={item.url}
                qrPng={item.qr_png}
                error={item.error}
              />
            ))}
          </div>
        )}
      </div>
    </AdminShell>
  );
};

export default AdminFakerExport;
