import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { motion, useIsPresent } from 'framer-motion';
import { Loader2, RefreshCw, ShieldAlert } from 'lucide-react';
import toast from 'react-hot-toast';
import axios from 'axios';
import client, { withAccount, type RequestAccount } from '../../api/client';
import { useAuthStore } from '../../store/auth';
import type { ApiResponse } from '../../types';

type CaptchaImageResponse = { image: string; blocked: boolean };
type CaptchaSubmitResponse = { verified: boolean; blocked?: boolean };

const CaptchaModal = ({ owner, onClose, onVerified }: {
  owner: RequestAccount;
  onClose: () => void;
  onVerified: () => void;
}) => {
  const isPresent = useIsPresent();
  const [image, setImage] = useState('');
  const [code, setCode] = useState('');
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [imageError, setImageError] = useState('');
  const mountedRef = useRef(false);
  const imageControllerRef = useRef<AbortController | null>(null);
  const imageRequestIdRef = useRef(0);
  const submitControllerRef = useRef<AbortController | null>(null);
  const submitRequestIdRef = useRef(0);

  const ownsDialog = useCallback(() => {
    const state = useAuthStore.getState();
    return mountedRef.current && state.activeUid === owner.uid && state.token === owner.token;
  }, [owner]);

  const invalidateRequests = useCallback(() => {
    mountedRef.current = false;
    imageControllerRef.current?.abort();
    submitControllerRef.current?.abort();
    imageControllerRef.current = null;
    submitControllerRef.current = null;
    imageRequestIdRef.current += 1;
    submitRequestIdRef.current += 1;
  }, []);

  useLayoutEffect(() => {
    mountedRef.current = isPresent && owner.uid > 0 && !!owner.token;
    return invalidateRequests;
  }, [invalidateRequests, isPresent, owner]);

  const close = () => {
    if (!ownsDialog()) return;
    invalidateRequests();
    onClose();
  };

  const finishChallenge = useCallback(() => {
    if (!ownsDialog()) return;
    invalidateRequests();
    onVerified();
  }, [invalidateRequests, onVerified, ownsDialog]);

  const fetchImage = useCallback(async () => {
    if (!ownsDialog()) return;
    imageControllerRef.current?.abort();
    const controller = new AbortController();
    const requestId = ++imageRequestIdRef.current;
    imageControllerRef.current = controller;
    const isCurrent = () => ownsDialog() && !controller.signal.aborted &&
      imageRequestIdRef.current === requestId && imageControllerRef.current === controller;
    try {
      const response = await client.get<ApiResponse<CaptchaImageResponse>>(
        '/learning/captcha', withAccount(owner, controller.signal)
      );
      if (!isCurrent()) return;
      const captcha = response.data.data;
      if (captcha?.blocked === false) {
        finishChallenge();
        return;
      }
      if (captcha?.blocked !== true || !captcha.image) {
        throw new Error('验证码图片不可用，请点击重试');
      }
      setImage(captcha.image);
      setImageError('');
    } catch (error: unknown) {
      if (!isCurrent() || axios.isCancel(error)) return;
      const message = error instanceof Error ? error.message : '获取验证码失败';
      setImage('');
      setImageError(message);
      toast.error(message);
    } finally {
      if (isCurrent()) {
        imageControllerRef.current = null;
        setLoading(false);
      }
    }
  }, [finishChallenge, owner, ownsDialog]);

  useEffect(() => {
    if (isPresent) void fetchImage();
  }, [fetchImage, isPresent]);

  const refreshImage = async () => {
    if (!ownsDialog() || loading || submitting || imageControllerRef.current || submitControllerRef.current) return;
    setCode('');
    setImage('');
    setImageError('');
    setLoading(true);
    await fetchImage();
  };

  const submit = async () => {
    if (!ownsDialog() || submitting || loading || !image || submitControllerRef.current || imageControllerRef.current) return;
    if (!/^[0-9a-zA-Z]{4}$/.test(code)) {
      toast.error('请输入 4 位验证码');
      return;
    }
    const controller = new AbortController();
    const requestId = ++submitRequestIdRef.current;
    submitControllerRef.current = controller;
    const isCurrent = () => ownsDialog() && !controller.signal.aborted &&
      submitRequestIdRef.current === requestId && submitControllerRef.current === controller;
    let refreshNeeded = false;
    setSubmitting(true);
    try {
      const response = await client.post<ApiResponse<CaptchaSubmitResponse>>(
        '/learning/captcha', { code }, withAccount(owner, controller.signal)
      );
      if (!isCurrent()) return;
      if (response.data.data?.verified !== true) {
        toast.error('验证码未通过，请重试');
        refreshNeeded = true;
        return;
      }
      toast.success('验证通过');
      finishChallenge();
    } catch (error: unknown) {
      if (!isCurrent() || axios.isCancel(error)) return;
      toast.error(error instanceof Error ? error.message : '验证失败，请重试');
      refreshNeeded = true;
    } finally {
      if (isCurrent()) {
        submitControllerRef.current = null;
        setSubmitting(false);
        if (refreshNeeded) {
          setCode('');
          setImage('');
          setImageError('');
          setLoading(true);
          void fetchImage();
        }
      }
    }
  };

  return (
        <motion.div
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/60 backdrop-blur-md"
          onClick={close}
        >
          <motion.div
            initial={{ y: '100%' }}
            animate={{ y: 0 }}
            exit={{ y: '100%' }}
            transition={{ type: 'spring', damping: 28, stiffness: 260 }}
            className="w-full max-w-[480px] bg-white rounded-t-[2rem] p-6 pb-[calc(24px+var(--sab))] shadow-2xl"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="w-12 h-1.5 rounded-full bg-slate-200 mx-auto mb-5" />
            <div className="flex items-center gap-3 mb-4">
              <div className="w-10 h-10 rounded-xl bg-amber-50 text-amber-600 flex items-center justify-center shrink-0">
                <ShieldAlert size={20} />
              </div>
              <div>
                <h3 className="text-lg font-black text-slate-900">学习通验证码</h3>
                <p className="text-xs text-slate-500 mt-0.5">请求触发学习通安全校验，输入图中 4 位字符后继续</p>
              </div>
            </div>

            <div className="rounded-2xl border border-slate-100 bg-slate-50 p-4">
              <button
                onClick={() => void refreshImage()}
                disabled={loading || submitting}
                className="w-full h-[88px] rounded-xl bg-white border border-slate-100 flex items-center justify-center overflow-hidden disabled:opacity-60"
                title="点击刷新验证码"
              >
                {loading ? (
                  <Loader2 size={26} className="text-slate-300 animate-spin" />
                ) : image ? (
                  <img src={image} alt="验证码" className="h-[72px]" referrerPolicy="no-referrer" />
                ) : (
                  <span className="px-3 text-sm text-slate-500">{imageError || '点击重新获取验证码'}</span>
                )}
              </button>
              <div className="flex items-center gap-2 mt-3">
                <input
                  value={code}
                  onChange={(e) => setCode(e.target.value.trim())}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
                      e.preventDefault();
                      void submit();
                    }
                  }}
                  disabled={loading || submitting}
                  maxLength={4}
                  autoFocus
                  placeholder="输入 4 位验证码"
                  className="flex-1 h-11 px-4 rounded-xl border border-slate-100 bg-white text-slate-900 text-sm font-bold tracking-[0.3em] uppercase placeholder:tracking-normal placeholder:font-normal placeholder:text-slate-400 focus:outline-none focus:border-blue-200"
                />
                <button
                  onClick={() => void refreshImage()}
                  disabled={loading || submitting}
                  className="w-11 h-11 rounded-xl bg-slate-100 text-slate-500 flex items-center justify-center shrink-0 disabled:opacity-60"
                  title="换一张"
                >
                  <RefreshCw size={17} className={loading ? 'animate-spin' : ''} />
                </button>
              </div>
            </div>

            <button
              onClick={() => void submit()}
              disabled={submitting || loading || !image || !/^[0-9a-zA-Z]{4}$/.test(code)}
              className="w-full h-12 mt-4 rounded-2xl bg-blue-600 text-white text-sm font-black flex items-center justify-center gap-2 disabled:opacity-50"
            >
              {submitting && <Loader2 size={16} className="animate-spin" />}
              提交验证
            </button>
          </motion.div>
        </motion.div>
  );
};

export default CaptchaModal;
