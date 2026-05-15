import axios from 'axios';
import type { ApiResponse } from '../types';
import config from '../../config.yaml';

const publicClient = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || config.api?.base_url || '/api',
  timeout: config.api?.timeout || 10000,
});

publicClient.interceptors.response.use(
  (response) => {
    const res = response.data as ApiResponse<any>;
    if (res.code !== 0) {
      return Promise.reject(new Error(res.message || '操作失败'));
    }
    return response;
  },
  (error) => {
    if (error.response?.data?.message) {
      return Promise.reject(new Error(error.response.data.message));
    }
    return Promise.reject(error);
  },
);

export default publicClient;
