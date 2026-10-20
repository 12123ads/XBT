import React from 'react';
import { motion } from 'framer-motion';
import { Loader2, LocateFixed, Map, MapPin } from 'lucide-react';

interface LocationInputProps {
  name: string;
  description: string;
  distanceLabel?: string;
  outOfRange?: boolean;
  onOpen: () => void;
  onOpenMap?: () => void;
  onLocate?: () => void;
  isLocating?: boolean;
}

export const LocationInput: React.FC<LocationInputProps> = ({
  name,
  description,
  distanceLabel,
  outOfRange = false,
  onOpen,
  onOpenMap,
  onLocate,
  isLocating,
}) => (
  <div className="w-full space-y-4 px-1">
    <div className="text-center">
      <h3 className="text-sm font-bold text-slate-800 uppercase tracking-widest">请确认签到位置</h3>
    </div>
    <motion.div
      whileTap={{ scale: 0.98 }}
      onClick={onOpen}
      className="w-full p-4 bg-slate-50 rounded-2xl flex items-center justify-between cursor-pointer group hover:bg-slate-100 transition-colors"
    >
      <div className="min-w-0 px-1">
        <p className="font-bold text-slate-800 truncate text-sm">{name || '点击选择地点'}</p>
        <p className="text-[10px] text-slate-400 font-medium truncate">{description}</p>
        {distanceLabel && (
          <p className={`text-[10px] font-bold truncate ${outOfRange ? 'text-red-500' : 'text-emerald-600'}`}>
            {distanceLabel}
          </p>
        )}
      </div>
      <div className="w-9 h-9 rounded-full bg-white flex items-center justify-center shadow-sm shrink-0">
        <MapPin size={16} className="text-blue-500" />
      </div>
    </motion.div>
    {(onOpenMap || onLocate) && (
      <div className="grid grid-cols-2 gap-2">
        {onOpenMap && (
          <button
            type="button"
            onClick={onOpenMap}
            className="py-3 rounded-2xl bg-blue-600 text-white text-xs font-black flex items-center justify-center gap-2 shadow-lg shadow-blue-100 active:scale-[0.98] transition-transform"
          >
            <Map size={16} />
            地图选点
          </button>
        )}
        {onLocate && (
          <button
            type="button"
            onClick={onLocate}
            disabled={isLocating}
            className="py-3 rounded-2xl bg-blue-50 text-blue-600 text-xs font-black flex items-center justify-center gap-2 disabled:opacity-60 active:scale-[0.98] transition-transform"
          >
            {isLocating ? <Loader2 size={16} className="animate-spin" /> : <LocateFixed size={16} />}
            {isLocating ? '定位中' : '浏览器定位'}
          </button>
        )}
      </div>
    )}
  </div>
);
