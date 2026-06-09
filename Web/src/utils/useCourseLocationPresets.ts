import { useEffect, useState } from 'react';
import publicClient from '../api/publicClient';
import type { ApiResponse, CourseLocationPreset } from '../types';

export const useCourseLocationPresets = () => {
  const [presets, setPresets] = useState<CourseLocationPreset[]>([]);

  useEffect(() => {
    let disposed = false;

    publicClient
      .get<ApiResponse<{ items: CourseLocationPreset[] }>>('/sign/location-presets')
      .then((response) => {
        if (!disposed) {
          setPresets(response.data.data?.items || []);
        }
      })
      .catch(() => {
        if (!disposed) {
          setPresets([]);
        }
      });

    return () => {
      disposed = true;
    };
  }, []);

  return presets;
};
