import axios, { AxiosError, CanceledError, type AxiosRequestConfig, type InternalAxiosRequestConfig } from 'axios';
import type { ApiResponse } from '../types';
import { useAuthStore } from '../store/auth';
import config from '../../config.yaml';

export type RequestAccount = { uid: number; token: string };

export const withAccount = (
  owner: RequestAccount,
  signal?: AbortSignal
): AxiosRequestConfig<unknown> & { account: RequestAccount } => ({ account: owner, signal });

type AccountRequestConfig = InternalAxiosRequestConfig<unknown> & { account?: RequestAccount };

const client = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || config.api?.base_url || '/api',
  timeout: config.api?.timeout || 10000,
});

client.interceptors.request.use((config) => {
  const request = config as AccountRequestConfig;
  if (request.url === '/auth/login') {
    delete request.account;
    request.headers.delete('Authorization');
    return request;
  }

  const { activeUid, token } = useAuthStore.getState();
  if (request.account) {
    if (request.account.uid !== activeUid || request.account.token !== token) {
      throw new CanceledError('请求账号已变化');
    }
  } else if (activeUid && token) {
    request.account = { uid: activeUid, token };
  }
  if (request.account) {
    request.headers.Authorization = `Bearer ${request.account.token}`;
  }
  return request;
});

client.interceptors.response.use(
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
    if (axios.isCancel(error) || !axios.isAxiosError<ApiResponse<unknown>>(error)) {
      return Promise.reject(error);
    }

    if (error.response?.status === 401) {
      const account = (error.config as AccountRequestConfig | undefined)?.account;
      const state = useAuthStore.getState();
      const savedAccount = account && state.accounts.find(item => (
        item.user.uid === account.uid && item.token === account.token
      ));
      if (account && savedAccount) {
        const wasActive = state.activeUid === account.uid && state.token === account.token;
        state.removeAccount(account.uid);
        if (wasActive) {
          window.location.hash = useAuthStore.getState().accounts.length ? '#/' : '#/login';
        }
      }
      error.message = error.response.data?.message || '登录已失效，请重新登录';
    } else if (error.response?.data?.message) {
      error.message = error.response.data.message;
    }
    return Promise.reject(error);
  }
);

export default client;
