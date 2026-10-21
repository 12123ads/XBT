/**
 * 安卓 APK 下载地址。服务器不存 APK，走 GitHub Release 的固定资产名，
 * 通过 GH 加速镜像转发；链接不含版本号，换版本无需改前端。
 *
 * 加速域失效时只需改这里（或回退成 GitHub 直链）：
 * https://github.com/12123ads/XBT/releases/latest/download/xbt-latest.apk
 */
export const APK_DOWNLOAD_URL =
  'https://ghfast.top/https://github.com/12123ads/XBT/releases/latest/download/xbt-latest.apk';

// iPadOS 13+ 的 Safari 默认发送桌面 UA（含 Macintosh），故必须包含 Mac 系标识。
const APPLE_USER_AGENT_PATTERN = /iPhone|iPad|iPod|Macintosh|Mac OS X/i;

export const isAppleUserAgent = (userAgent: string): boolean =>
  APPLE_USER_AGENT_PATTERN.test(userAgent);

/** Apple 设备已能从 App Store 侧安装，不展示安卓下载提示。 */
export const shouldShowApkDownload = (userAgent: string): boolean =>
  !isAppleUserAgent(userAgent);
