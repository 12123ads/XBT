import axios, { AxiosError } from 'axios';
import type { ApiResponse } from '../types';
import config from '../../config.yaml';

const publicClient = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || config.api?.base_url || '/api',
  timeout: config.api?.timeout || 10000,
});

publicClient.interceptors.response.use(
  (response) => {
    const res = response.data as ApiResponse<unknown>;
    if (res.code !== 0) {
      return Promise.reject(new AxiosError<ApiResponse<unknown>>(
        res.message || '操作失败', 'ERR_API_RESPONSE', response.config, response.request, response
      ));
    }
    return response;
  },
  (error: unknown) => {
    if (axios.isCancel(error)) return Promise.reject(error);
    if (axios.isAxiosError<ApiResponse<unknown>>(error) && error.response?.data?.message) {
      error.message = error.response.data.message;
    }
    return Promise.reject(error);
  },
);

export default publicClient;
