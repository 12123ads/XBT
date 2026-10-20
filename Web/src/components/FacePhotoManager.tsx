import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AlertTriangle, Camera, Download, ScanFace, Trash2, Upload } from 'lucide-react';
import toast from 'react-hot-toast';
import client from '../api/client';
import type { ApiResponse } from '../types';

export interface FacePhoto {
  id: number;
  user_uid: number;
  object_id: string;
  use_count: number;
  is_failure_before: boolean;
  source: string;
  created_at: string;
  image: string;
}

interface FacePhotoList {
  user_uid: number;
  max: number;
  photos: FacePhoto[];
}

interface FacePhotoManagerProps {
  targetUid?: number;
  onChanged?: (count: number) => void;
}

const MAX_UPLOAD_BYTES = 20 * 1024 * 1024;

const targetPath = (path: string, targetUid?: number) => (
  targetUid ? `${path}?target_uid=${targetUid}` : path
);

const FacePhotoManager = ({ targetUid, onChanged }: FacePhotoManagerProps) => {
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);
  const onChangedRef = useRef(onChanged);
  const [photos, setPhotos] = useState<FacePhoto[]>([]);
  const [max, setMax] = useState(5);
  const [isLoading, setIsLoading] = useState(true);
  const [busy, setBusy] = useState<'upload' | 'import' | number | null>(null);

  useEffect(() => {
    onChangedRef.current = onChanged;
  }, [onChanged]);

  const applyList = useCallback((list: FacePhotoList) => {
    setPhotos(list.photos || []);
    setMax(list.max || 5);
    onChangedRef.current?.((list.photos || []).length);
  }, []);

  const fetchPhotos = useCallback(async () => {
    setIsLoading(true);
    try {
      const res = await client.get<ApiResponse<FacePhotoList>>(targetPath('/user/faces', targetUid));
      applyList(res.data.data);
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : '读取人脸照片失败');
    } finally {
      setIsLoading(false);
    }
  }, [applyList, targetUid]);

  useEffect(() => {
    void fetchPhotos();
  }, [fetchPhotos]);

  const handleFile = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (!file) return;
    if (!file.type.startsWith('image/')) {
      toast.error('请选择图片文件');
      return;
    }
    if (file.size > MAX_UPLOAD_BYTES) {
      toast.error('图片不能超过 20MB');
      return;
    }
    setBusy('upload');
    try {
      const formData = new FormData();
      formData.append('file', file);
      await client.post<ApiResponse<FacePhoto>>(targetPath('/user/faces', targetUid), formData);
      toast.success('人脸照片已保存');
      await fetchPhotos();
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : '上传失败');
    } finally {
      setBusy(null);
    }
  };

  const handleImport = async () => {
    setBusy('import');
    try {
      await client.post<ApiResponse<FacePhoto>>(targetPath('/user/faces/import', targetUid));
      toast.success('已导入官方照片，签到时自动裁剪/旋转/压缩');
      await fetchPhotos();
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : '导入官方照片失败');
    } finally {
      setBusy(null);
    }
  };

  const handleDelete = async (photo: FacePhoto) => {
    setBusy(photo.id);
    try {
      await client.delete(`/user/faces/${photo.id}`);
      toast.success('已删除该照片');
      await fetchPhotos();
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : '删除失败');
    } finally {
      setBusy(null);
    }
  };

  const handleCapture = () => {
    navigate('/face-capture', { state: { faceTargetUid: targetUid } });
  };

  const atLimit = photos.length >= max;
  const disabled = busy !== null;

  return (
    <div className="w-full">
      <div className="flex items-center justify-between mb-3">
        <p className="text-xs font-bold text-slate-500">
          {photos.length}/{max} 张，签到时会随机选择一张
        </p>
        <button
          onClick={() => void fetchPhotos()}
          className="text-[11px] font-bold text-blue-600 disabled:opacity-50"
          disabled={isLoading}
        >
          刷新
        </button>
      </div>

      {isLoading ? (
        <div className="h-32 flex items-center justify-center">
          <div className="w-8 h-8 border-2 border-slate-300 border-t-blue-500 rounded-full animate-spin" />
        </div>
      ) : photos.length === 0 ? (
        <div className="h-32 rounded-2xl border-2 border-dashed border-slate-200 flex flex-col items-center justify-center gap-2 text-slate-400">
          <ScanFace size={32} />
          <span className="text-xs font-medium">还没有人脸照片</span>
        </div>
      ) : (
        <div className="grid grid-cols-3 gap-2.5">
          {photos.map((photo) => (
            <div key={photo.id} className="relative rounded-2xl overflow-hidden border-2 border-slate-100 bg-slate-50 aspect-[3/4]">
              <img src={photo.image} alt="人脸照片" className="w-full h-full object-cover" />
              <div className="absolute top-1 left-1 flex flex-col gap-1">
                {photo.is_failure_before && (
                  <span className="flex items-center gap-0.5 rounded-md bg-rose-600/90 px-1.5 py-0.5 text-[9px] font-bold text-white">
                    <AlertTriangle size={9} /> 失败过
                  </span>
                )}
                {photo.source === 'official' && (
                  <span className="rounded-md bg-blue-600/90 px-1.5 py-0.5 text-[9px] font-bold text-white">官方</span>
                )}
              </div>
              <div className="absolute bottom-0 inset-x-0 flex items-center justify-between bg-slate-900/55 px-1.5 py-1">
                <span className="text-[9px] font-bold text-white">用过 {photo.use_count} 次</span>
                <button
                  onClick={() => void handleDelete(photo)}
                  disabled={disabled}
                  className="text-white/80 hover:text-white disabled:opacity-50"
                  title="删除"
                >
                  {busy === photo.id
                    ? <div className="w-3 h-3 border border-white border-t-transparent rounded-full animate-spin" />
                    : <Trash2 size={12} />}
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      <input ref={inputRef} type="file" accept="image/*" className="hidden" onChange={handleFile} />

      <div className="grid grid-cols-2 gap-3 mt-4">
        <button
          onClick={handleCapture}
          disabled={disabled || atLimit}
          className="flex items-center justify-center gap-2 py-3 rounded-2xl bg-blue-600 text-white font-bold text-sm shadow-lg shadow-blue-100 transition-all active:scale-[0.97] disabled:opacity-40"
        >
          <Camera size={16} /> 拍照上传
        </button>
        <button
          onClick={() => inputRef.current?.click()}
          disabled={disabled || atLimit}
          className="flex items-center justify-center gap-2 py-3 rounded-2xl bg-slate-100 text-slate-700 font-bold text-sm transition-all active:scale-[0.97] disabled:opacity-40"
        >
          {busy === 'upload'
            ? <div className="w-4 h-4 border-2 border-slate-400 border-t-transparent rounded-full animate-spin" />
            : <Upload size={16} />}
          相册选取
        </button>
      </div>

      <button
        onClick={() => void handleImport()}
        disabled={disabled || atLimit}
        className="mt-3 w-full flex items-center justify-center gap-2 py-3 rounded-2xl bg-emerald-50 text-emerald-700 font-bold text-sm transition-all active:scale-[0.97] disabled:opacity-40"
      >
        {busy === 'import'
          ? <div className="w-4 h-4 border-2 border-emerald-500 border-t-transparent rounded-full animate-spin" />
          : <Download size={16} />}
        拉取学习通官方照片（保存原图，签到时自动变换）
      </button>
      <p className="mt-2 text-center text-[11px] text-slate-400">
        需要该账号已在学习通设置过人脸照片；导入的是原图，每次签到前会重新变换。
      </p>
      {atLimit && (
        <p className="mt-2 text-center text-[11px] font-medium text-slate-400">已达 5 张上限，请先删除一张</p>
      )}
    </div>
  );
};

export default FacePhotoManager;
