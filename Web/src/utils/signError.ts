import type { SignExecuteResult } from '../types';

export type SignFailureAction =
  | 'retry_captcha'
  | 'rescan_qr'
  | 'update_face'
  | 'relocate'
  | 'relogin'
  | 'fail';

export interface SignFailureDecision {
  action: SignFailureAction;
  message: string;
}

type SignFailureResult = Pick<SignExecuteResult, 'error_code' | 'message' | 'distance_m'>;

const textOr = (message: string | undefined, fallback: string) => {
  const trimmed = message?.trim();
  return trimmed ? trimmed : fallback;
};

/**
 * Maps the server error code (XBT-S3) to a client action. Only captcha_required
 * is retried, and the caller bounds that retry to one attempt.
 */
export const decideSignFailure = (result: SignFailureResult): SignFailureDecision => {
  switch (result.error_code) {
    case 'captcha_required':
      return { action: 'retry_captcha', message: textOr(result.message, '验证码校验未通过') };
    case 'qr_expired':
      return { action: 'rescan_qr', message: textOr(result.message, '二维码已过期，请重新扫码') };
    case 'face_required':
      return { action: 'update_face', message: textOr(result.message, '人脸验证未通过，请更新人脸照片后重试') };
    case 'face_photo_unavailable':
      return { action: 'update_face', message: textOr(result.message, '未设置人脸照片且无法获取官方照片，请先上传') };
    case 'location_point_required':
      return { action: 'relocate', message: textOr(result.message, '该签到要求定位，请先在地图选点或允许定位') };
    case 'location_out_of_range': {
      const distanceM = typeof result.distance_m === 'number' && Number.isFinite(result.distance_m) && result.distance_m > 0
        ? Math.round(result.distance_m)
        : undefined;
      if (distanceM === undefined) {
        return { action: 'relocate', message: textOr(result.message, '签到位置超出范围，请重新选择位置') };
      }
      return {
        action: 'relocate',
        message: `签到位置超出范围，距签到点 ${distanceM} 米，请重新选择位置`,
      };
    }
    case 'activity_ended':
      return { action: 'fail', message: textOr(result.message, '签到已结束（迟到或活动已关闭）') };
    case 'session_expired':
      return { action: 'relogin', message: textOr(result.message, '学习通登录已过期，请重新登录') };
    default:
      return { action: 'fail', message: textOr(result.message, '签到失败') };
  }
};
