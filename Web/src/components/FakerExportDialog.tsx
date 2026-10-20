import { useEffect, useRef } from 'react';
import { motion } from 'framer-motion';
import { X } from 'lucide-react';
import FakerExportQrCard from './FakerExportQrCard';
import type { FakerExportSingle } from '../api/fakerExport';

type FakerExportDialogProps = {
  uid: number;
  name: string;
  phone: string;
  data: FakerExportSingle;
  onClose: () => void;
};

const FakerExportDialog = ({ uid, name, phone, data, onClose }: FakerExportDialogProps) => {
  const closeButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeButtonRef.current?.focus();
    return () => previousFocus?.focus();
  }, []);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [onClose]);

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/60 backdrop-blur-md"
      onClick={onClose}
    >
      <motion.div
        role="dialog"
        aria-modal="true"
        aria-label="导出到 CSF"
        initial={{ y: '100%' }}
        animate={{ y: 0 }}
        exit={{ y: '100%' }}
        transition={{ type: 'spring', damping: 28, stiffness: 260 }}
        className="max-h-[92dvh] w-full max-w-[480px] overflow-y-auto rounded-t-[2rem] bg-slate-50 p-5 pb-[calc(24px+var(--sab))] shadow-2xl custom-scrollbar"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="mx-auto mb-5 h-1.5 w-12 rounded-full bg-slate-200" />
        <div className="mb-4 flex items-center justify-between gap-3">
          <div className="min-w-0">
            <h3 className="text-lg font-black text-slate-900">导出到 CSF</h3>
            <p className="mt-0.5 truncate text-xs text-slate-400">
              {name} · UID {uid}
            </p>
          </div>
          <button
            type="button"
            ref={closeButtonRef}
            onClick={onClose}
            aria-label="关闭导出面板"
            className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-white text-slate-500 shadow-sm"
          >
            <X size={18} />
          </button>
        </div>

        <FakerExportQrCard
          uid={uid}
          name={name}
          phone={phone}
          url={data.url}
          qrPng={data.qr_png}
          showMeta={false}
        />

        <button
          type="button"
          onClick={onClose}
          className="mt-3 w-full rounded-2xl py-3 text-sm font-bold text-slate-400"
        >
          关闭
        </button>
      </motion.div>
    </motion.div>
  );
};

export default FakerExportDialog;
