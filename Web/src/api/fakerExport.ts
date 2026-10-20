import axios from 'axios';
import client from './client';
import type { ApiResponse } from '../types';

export interface FakerExportItem {
  uid: number;
  name: string;
  phone: string;
  url: string;
  qr_png: string;
  error?: string;
}

export interface FakerExportSingle {
  url: string;
  qr_png: string;
  warnings: string[];
}

export interface FakerExportBatch {
  items: FakerExportItem[];
  warnings: string[];
}

export const getMyFakerExport = async (): Promise<FakerExportSingle> => {
  const response = await client.get<ApiResponse<FakerExportSingle>>('/account/faker-export');
  return response.data.data;
};

export const getFakerExportBatch = async (uids?: number[]): Promise<FakerExportBatch> => {
  const response = await client.get<ApiResponse<FakerExportBatch>>('/admin/accounts/faker-export', {
    params: uids && uids.length > 0 ? { uids: uids.join(',') } : undefined,
  });
  return response.data.data;
};

const readBlobMessage = async (blob: Blob): Promise<string> => {
  try {
    const payload: unknown = JSON.parse(await blob.text());
    if (
      payload
      && typeof payload === 'object'
      && 'message' in payload
      && typeof payload.message === 'string'
    ) {
      return payload.message;
    }
  } catch {
    // Error body is not JSON; keep the axios message.
  }
  return '';
};

export const downloadFakerExportZip = async (): Promise<void> => {
  try {
    const response = await client.get<Blob>('/admin/accounts/faker-export.zip', {
      responseType: 'blob',
    });
    const objectUrl = URL.createObjectURL(response.data);
    const link = document.createElement('a');
    link.href = objectUrl;
    link.download = `csf-export-${new Date().toISOString().slice(0, 10)}.zip`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(objectUrl), 1000);
  } catch (error: unknown) {
    if (axios.isAxiosError(error) && error.response?.data instanceof Blob) {
      const message = await readBlobMessage(error.response.data);
      if (message) error.message = message;
    }
    throw error;
  }
};
