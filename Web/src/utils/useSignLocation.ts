import { useCallback, useRef, useState } from 'react';
import toast from 'react-hot-toast';
import { locateWithBrowserSelection } from './location';
import type { SelectedLocation } from '../types';

export const useSignLocation = () => {
  const [location, setLocation] = useState<SelectedLocation | null>(null);
  const [isLocating, setIsLocating] = useState(false);
  const locatingRef = useRef(false);

  const locateWithBrowser = useCallback(async () => {
    if (locatingRef.current) return false;
    locatingRef.current = true;
    setIsLocating(true);
    try {
      setLocation(await locateWithBrowserSelection());
      return true;
    } catch (error: unknown) {
      toast.error(error instanceof Error ? error.message : '获取当前位置失败');
      return false;
    } finally {
      locatingRef.current = false;
      setIsLocating(false);
    }
  }, []);

  return { location, setLocation, isLocating, locateWithBrowser };
};
