import { motion } from 'framer-motion';
import type { ReactNode } from 'react';
import { X } from 'lucide-react';

const AdminModal = ({ title, children, onClose }: { title: string; children: ReactNode; onClose: () => void }) => (
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

export default AdminModal;
