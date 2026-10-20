import { lazy, Suspense, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { AnimatePresence, motion } from 'framer-motion';
import { CheckCircle2, ChevronRight, LocateFixed, Loader2, Map, MapPin, X } from 'lucide-react';
import toast from 'react-hot-toast';
import { presetToSelectedLocation, isSameLocation, locateWithBrowserSelection, describeSignDistance, type SignLocationCenter } from '../../utils/location';
import type { CourseLocationPreset, SelectedLocation } from '../../types';

const LocationMapPicker = lazy(() => import('./LocationMapPicker'));

export type LocationPickerView = 'list' | 'map';

interface LocationPickerSheetProps {
  open: boolean;
  initialView?: LocationPickerView;
  value: SelectedLocation | null;
  presets: CourseLocationPreset[];
  center?: SignLocationCenter | null;
  radius?: number | null;
  allowEmpty?: boolean;
  onClose: () => void;
  onChange: (location: SelectedLocation | null) => void;
}

const MapLoadingFallback = () => (
  <motion.div
    initial={{ opacity: 0 }}
    animate={{ opacity: 1 }}
    exit={{ opacity: 0 }}
    className="fixed inset-0 z-[160] flex items-end justify-center bg-slate-900/60 backdrop-blur-md"
  >
    <div className="bg-white w-full max-w-[480px] rounded-t-[3rem] px-8 py-14 pb-[calc(32px+var(--sab))] shadow-2xl flex flex-col items-center gap-3">
      <Loader2 size={26} className="animate-spin text-blue-600" />
      <p className="text-xs font-bold text-slate-500">正在加载地图…</p>
      <p className="text-[10px] font-medium text-slate-400">地图资源仅在选择地图选点时加载</p>
    </div>
  </motion.div>
);

export const LocationPickerSheet = ({
  open,
  initialView = 'list',
  value,
  presets,
  center,
  radius,
  allowEmpty = false,
  onClose,
  onChange,
}: LocationPickerSheetProps) => {
  const [view, setView] = useState<LocationPickerView>(initialView);
  const [isLocating, setIsLocating] = useState(false);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useLayoutEffect(() => {
    if (open) setView(initialView);
  }, [open, initialView]);

  // Give the picker one history entry so the Android/browser back key closes it
  // instead of navigating away from the sign page.
  useEffect(() => {
    if (!open) return;
    const previousState = window.history.state;
    window.history.pushState({
      ...(previousState && typeof previousState === 'object' ? previousState : {}),
      xbtLocationPicker: true,
    }, '');
    let active = true;
    const handlePop = () => {
      if (!active) return;
      active = false;
      onCloseRef.current();
    };
    window.addEventListener('popstate', handlePop);
    return () => {
      window.removeEventListener('popstate', handlePop);
      if (active) {
        active = false;
        window.history.back();
      }
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      if (view === 'map') {
        setView('list');
      } else {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onClose, open, view]);

  const handleBrowserLocation = async () => {
    if (isLocating) return;
    setIsLocating(true);
    try {
      onChange(await locateWithBrowserSelection());
      onClose();
    } catch (error: unknown) {
      toast.error(error instanceof Error ? error.message : '获取当前位置失败');
    } finally {
      setIsLocating(false);
    }
  };

  const currentDistance = value ? describeSignDistance(value.lat, value.lng, center, radius) : null;

  return (
    <AnimatePresence>
      {open && view === 'map' && (
        <Suspense key="map" fallback={<MapLoadingFallback />}>
          <LocationMapPicker
            current={value}
            presets={presets}
            center={center}
            radius={radius}
            onCancel={() => setView('list')}
            onConfirm={(location) => {
              onChange(location);
              onClose();
            }}
          />
        </Suspense>
      )}

      {open && view === 'list' && (
        <motion.div
          key="list"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          className="fixed inset-0 z-[150] flex items-end justify-center bg-slate-900/60 backdrop-blur-md p-0"
          onClick={onClose}
          role="presentation"
        >
          <motion.div
            initial={{ y: '100%' }}
            animate={{ y: 0 }}
            exit={{ y: '100%' }}
            transition={{ type: 'spring', damping: 28, stiffness: 250 }}
            className="bg-white w-full max-w-[480px] rounded-t-[3rem] p-8 shadow-2xl overflow-hidden flex flex-col max-h-[80vh]"
            onClick={(event) => event.stopPropagation()}
            role="dialog"
            aria-modal="true"
            aria-label="选择签到位置"
          >
            <div className="w-12 h-1.5 bg-slate-200 rounded-full mx-auto mb-8 shrink-0" />
            <div className="flex items-center justify-between mb-6 shrink-0">
              <h3 className="text-xl font-bold text-slate-900">选择签到位置</h3>
              <button
                type="button"
                onClick={onClose}
                aria-label="关闭位置选择"
                className="w-8 h-8 flex items-center justify-center bg-slate-100 text-slate-400 rounded-full active:scale-95 transition-transform"
              >
                <X size={16} />
              </button>
            </div>

            <div className="flex-1 overflow-y-auto space-y-3 pr-1 pb-[calc(40px+var(--sab))] custom-scrollbar px-1 overscroll-contain">
              {currentDistance && (
                <div className={`rounded-2xl px-4 py-3 text-xs font-bold ${currentDistance.outOfRange ? 'bg-red-50 text-red-600' : 'bg-emerald-50 text-emerald-700'}`}>
                  {currentDistance.label}
                  {currentDistance.outOfRange && '，请重新选择签到位置'}
                </div>
              )}
              <motion.button
                type="button"
                whileTap={{ scale: 0.98 }}
                onClick={() => setView('map')}
                className={`w-full p-5 rounded-[1.5rem] border-2 transition-all cursor-pointer flex items-center justify-between text-left ${value?.source === 'map' ? 'border-blue-500 bg-blue-50/30' : 'border-blue-100 bg-blue-50/40 hover:bg-white'}`}
              >
                <div className="flex-1 min-w-0 pr-4">
                  <div className="font-bold text-slate-800 mb-0.5 text-sm flex items-center gap-1.5">
                    <Map size={15} className="text-blue-600" />
                    地图选点
                  </div>
                  <div className="text-[10px] text-slate-400 font-medium truncate">
                    {value?.source === 'map' ? `${value.description}（${value.lng}, ${value.lat}）` : '在地图上点按或拖动图钉选择精确位置'}
                  </div>
                </div>
                {value?.source === 'map' ? (
                  <CheckCircle2 size={20} className="text-blue-600 shrink-0" />
                ) : (
                  <ChevronRight size={20} className="text-blue-500 shrink-0" />
                )}
              </motion.button>

              {allowEmpty && (
                <motion.button
                  type="button"
                  whileTap={{ scale: 0.98 }}
                  onClick={() => {
                    onChange(null);
                    onClose();
                  }}
                  className={`w-full p-5 rounded-[1.5rem] border-2 transition-all cursor-pointer flex items-center justify-between text-left ${!value ? 'border-blue-500 bg-blue-50/30' : 'border-slate-50 bg-slate-100/50 hover:bg-white'}`}
                >
                  <div className="flex-1 min-w-0 pr-4">
                    <div className="font-bold text-slate-800 text-sm">不使用位置</div>
                    <div className="text-[10px] text-slate-400 font-medium">不发送地理位置信息进行签到</div>
                  </div>
                  {!value && <CheckCircle2 size={20} className="text-blue-600 shrink-0" />}
                </motion.button>
              )}

              <motion.button
                type="button"
                whileTap={{ scale: 0.98 }}
                onClick={handleBrowserLocation}
                disabled={isLocating}
                className={`w-full p-5 rounded-[1.5rem] border-2 transition-all cursor-pointer flex items-center justify-between text-left disabled:opacity-70 ${value?.source === 'browser' ? 'border-blue-500 bg-blue-50/30' : 'border-slate-50 bg-slate-100/50 hover:bg-white'}`}
              >
                <div className="flex-1 min-w-0 pr-4">
                  <div className="font-bold text-slate-800 mb-0.5 text-sm">使用浏览器当前位置</div>
                  <div className="text-[10px] text-slate-400 font-medium truncate">
                    {isLocating ? '正在请求定位权限并转换为 BD-09' : '从系统定位服务读取并转换为 BD-09'}
                  </div>
                </div>
                {isLocating ? (
                  <Loader2 size={20} className="text-blue-600 shrink-0 animate-spin" />
                ) : (
                  <LocateFixed size={20} className="text-blue-600 shrink-0" />
                )}
              </motion.button>

              {presets.map((preset) => {
                const presetLocation = presetToSelectedLocation(preset);
                const isSelected = isSameLocation(value, presetLocation);
                const presetDistance = describeSignDistance(preset.lat, preset.lng, center, radius);
                return (
                  <motion.button
                    type="button"
                    key={`${preset.name}-${preset.lng}-${preset.lat}`}
                    whileTap={{ scale: 0.98 }}
                    onClick={() => {
                      onChange(presetLocation);
                      onClose();
                    }}
                    className={`w-full p-5 rounded-[1.5rem] border-2 transition-all cursor-pointer flex items-center justify-between text-left ${isSelected ? 'border-blue-500 bg-blue-50/30' : 'border-slate-50 bg-slate-50/50 hover:bg-white'}`}
                  >
                    <div className="flex-1 min-w-0 pr-4">
                      <div className="font-bold text-slate-800 mb-0.5 text-sm flex items-center gap-1.5">
                        <MapPin size={14} className="text-slate-400 shrink-0" />
                        <span className="truncate">{preset.name}</span>
                      </div>
                      <div className="text-[10px] text-slate-400 font-medium truncate">{preset.description}</div>
                      {presetDistance && (
                        <div className={`text-[10px] font-bold truncate ${presetDistance.outOfRange ? 'text-red-500' : 'text-emerald-600'}`}>
                          {presetDistance.label}
                        </div>
                      )}
                    </div>
                    {isSelected && <CheckCircle2 size={20} className="text-blue-600 shrink-0" />}
                  </motion.button>
                );
              })}
            </div>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
};

export default LocationPickerSheet;
