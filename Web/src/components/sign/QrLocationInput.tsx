import React from 'react';
import { MapPin, X } from 'lucide-react';
import type { SelectedLocation } from '../../types';

interface QrLocationInputProps {
  mode: 'auto' | 'manual';
  value: SelectedLocation | null;
  distanceLabel?: string;
  outOfRange?: boolean;
  onModeChange: (mode: 'auto' | 'manual') => void;
  onOpen: () => void;
  onClear: () => void;
}

export const QrLocationInput: React.FC<QrLocationInputProps> = ({
  mode,
  value,
  distanceLabel,
  outOfRange = false,
  onModeChange,
  onOpen,
  onClear,
}) => (
  <div className="mt-4 w-full space-y-3 rounded-2xl border border-slate-100 bg-slate-50 p-4">
    <div className="flex items-center justify-between gap-3">
      <div className="flex items-center gap-2">
        <MapPin size={15} className="text-blue-500 shrink-0" />
        <span className="text-sm font-bold text-slate-800">定位</span>
      </div>
      <div className="flex rounded-full bg-slate-200 p-0.5" role="group" aria-label="定位方式">
        <button
          type="button"
          aria-pressed={mode === 'auto'}
          onClick={() => onModeChange('auto')}
          className={`px-3 py-1 rounded-full text-[11px] font-bold transition-colors ${mode === 'auto' ? 'bg-white text-slate-900 shadow-sm' : 'text-slate-500'}`}
        >
          自动
        </button>
        <button
          type="button"
          aria-pressed={mode === 'manual'}
          onClick={() => {
            onModeChange('manual');
            onOpen();
          }}
          className={`px-3 py-1 rounded-full text-[11px] font-bold transition-colors ${mode === 'manual' ? 'bg-white text-slate-900 shadow-sm' : 'text-slate-500'}`}
        >
          手动选点
        </button>
      </div>
    </div>

    {mode === 'auto' ? (
      <p className="text-[10px] leading-relaxed text-slate-400">
        不发送坐标，由服务端在活动范围内自动采样
      </p>
    ) : value ? (
      <div className="flex items-center justify-between gap-2 rounded-xl bg-white p-3">
        <div className="min-w-0 space-y-0.5">
          <p className="truncate text-xs font-bold text-slate-800">{value.name || '已选位置'}</p>
          <p className="truncate text-[10px] text-slate-400">{value.description}</p>
          <p className="font-mono text-[10px] text-slate-400">{value.lng}, {value.lat}</p>
          {distanceLabel && (
            <p className={`text-[10px] font-bold ${outOfRange ? 'text-red-500' : 'text-emerald-600'}`}>
              {distanceLabel}
            </p>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <button
            type="button"
            onClick={onOpen}
            className="rounded-lg px-2 py-1 text-[11px] font-bold text-blue-600 active:scale-95 transition-transform"
          >
            修改
          </button>
          <button
            type="button"
            onClick={onClear}
            aria-label="清除所选位置"
            className="flex h-7 w-7 items-center justify-center rounded-full bg-slate-100 text-slate-400 active:scale-95 transition-transform"
          >
            <X size={14} />
          </button>
        </div>
      </div>
    ) : (
      <button
        type="button"
        onClick={onOpen}
        className="w-full rounded-xl bg-white px-3 py-2.5 text-left text-[11px] font-bold text-amber-600 active:scale-[0.99] transition-transform"
      >
        尚未选点，点此选择位置
      </button>
    )}
  </div>
);

export default QrLocationInput;
