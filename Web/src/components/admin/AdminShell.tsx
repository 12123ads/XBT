import type { ReactNode } from 'react';
import { ChevronLeft } from 'lucide-react';
import { useNavigate } from 'react-router-dom';

type AdminShellProps = {
  title: string;
  subtitle: string;
  children: ReactNode;
  action?: ReactNode;
  backTo?: string;
};

const AdminShell = ({ title, subtitle, children, action, backTo = '/admin/panel' }: AdminShellProps) => {
  const navigate = useNavigate();

  return (
    <div className="h-full min-h-0 flex flex-col bg-slate-50 overflow-hidden">
      <div className="bg-white border-b border-slate-100 px-4 h-[calc(80px+var(--sat))] pt-[var(--sat)] flex items-center justify-between shrink-0">
        <div className="flex items-center min-w-0">
          <button
            onClick={() => navigate(backTo)}
            className="p-2 -ml-2 text-slate-600 hover:bg-slate-50 rounded-lg"
          >
            <ChevronLeft size={24} />
          </button>
          <div className="ml-2 min-w-0">
            <h2 className="font-bold text-slate-900 text-lg truncate">{title}</h2>
            <p className="text-[10px] text-slate-400 font-bold truncate">{subtitle}</p>
          </div>
        </div>
        {action}
      </div>
      {children}
    </div>
  );
};

export default AdminShell;
