import { AlertTriangle, Copy } from 'lucide-react';
import toast from 'react-hot-toast';

export type FakerExportQrCardProps = {
  uid?: number;
  name: string;
  phone?: string;
  url: string;
  qrPng: string;
  error?: string;
  showMeta?: boolean;
};

const copyLink = async (url: string) => {
  try {
    await navigator.clipboard.writeText(url);
    toast.success('导入链接已复制');
    return;
  } catch {
    // Clipboard API needs a secure context; fall back to a temporary selection.
  }

  const textarea = document.createElement('textarea');
  textarea.value = url;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.top = '0';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand('copy');
  textarea.remove();
  if (copied) {
    toast.success('导入链接已复制');
  } else {
    toast.error('复制失败，请手动选择链接复制');
  }
};

const FakerExportQrCard = ({ uid, name, phone, url, qrPng, error, showMeta = true }: FakerExportQrCardProps) => {
  const hasQr = Boolean(url && qrPng);

  return (
    <div className="rounded-[1.75rem] border border-slate-100 bg-white p-4 shadow-sm">
      {showMeta && (
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="font-black text-slate-900 truncate">{name || '未命名账号'}</p>
            <p className="mt-0.5 text-[11px] text-slate-400 font-mono">
              {uid !== undefined ? `UID ${uid}` : 'UID 未知'}
              {phone ? ` · ${phone}` : ''}
            </p>
          </div>
          <span className="shrink-0 rounded-full bg-blue-50 px-2.5 py-1 text-[10px] font-black text-blue-600">
            CSF
          </span>
        </div>
      )}

      {hasQr ? (
        <>
          <div className="mt-3 flex justify-center">
            <div className="w-full max-w-[240px] aspect-square overflow-hidden rounded-2xl border border-slate-100 bg-white">
              <img
                src={qrPng}
                alt={`${name || '账号'} 的 CSF 导入二维码`}
                width={240}
                height={240}
                decoding="async"
                className="h-full w-full object-contain"
              />
            </div>
          </div>

          <p className="mt-3 max-h-24 overflow-y-auto rounded-xl bg-slate-50 px-3 py-2 font-mono text-[11px] leading-relaxed text-slate-500 break-all select-text custom-scrollbar">
            {url}
          </p>

          <button
            type="button"
            onClick={() => void copyLink(url)}
            className="mt-3 flex w-full items-center justify-center gap-2 rounded-2xl bg-blue-600 py-3 text-sm font-black text-white active:scale-[0.99] transition-transform"
          >
            <Copy size={16} />
            复制链接
          </button>

          <p className="mt-3 text-center text-[11px] font-bold leading-relaxed text-amber-600">
            该二维码/链接等同账号密码，请勿截图外传
          </p>
        </>
      ) : (
        <div className="mt-3 rounded-2xl border border-amber-100 bg-amber-50 px-3 py-6 text-center">
          <AlertTriangle size={22} className="mx-auto text-amber-600" />
          <p className="mt-2 text-xs font-black text-amber-700">无法生成该账号的二维码</p>
          <p className="mt-1 text-[11px] leading-relaxed text-amber-600">
            {error || '导出数据不完整，请重试'}
          </p>
        </div>
      )}
    </div>
  );
};

export default FakerExportQrCard;
