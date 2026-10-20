# 二维码识别：xbt 与 ChaoxingSignFaker（CSF）对比

- 日期：2026-10-20
- xbt 基线：`feature/captcha-protocol-fix`（`df9e825`）
- CSF 基线：`9ae5398`
- 性质：静态代码对比；未在真机做识别率/耗时对比，也未用真实签到二维码跑通端到端。

## 结论

Android 上两边的**像素级解码器是同一个**（Google ML Kit Barcode Scanning，CameraX 取帧），所以「能不能把二维码画面读成一串文本」差别不大。真正的差别在解码之后：

1. **认什么格式**：xbt 只认含 `mobilelearn.chaoxing.com` 的网址；CSF 还认 `SIGNIN:` 前缀的官方码，且不限定域名。
2. **码里的活动 id 用不用**：xbt 丢掉码里的 `id/aid`，一律用当前页面的活动；CSF 用码里的活动 id 提交。
3. **过期怎么处理**：xbt 不判断过期，直接拿同一串 `enc` 重试 15 次；CSF 签到前和连续扫码时主动查 `signDetail`，过期就停用并等新码。
4. **多账号怎么扫**：xbt 一次扫码后所有账号并发用同一串 `enc`；CSF 按账号串行，账号之间有间隔，过期后要求重新扫码。

一句话：**xbt 的短板不是“识别不出图”，而是“读出来的码没有被正确校验和使用”**。

## 1. 解码层

| 项 | xbt | CSF |
|---|---|---|
| Android 取帧 | CameraX `ImageAnalysis`（`STRATEGY_KEEP_ONLY_LATEST`） | CameraX + `MlKitAnalyzer`，最高可用分辨率 |
| Android 解码 | ML Kit 默认 client，**未限定码制**；取第一条非空 `rawValue` | ML Kit，**限定 `FORMAT_QR_CODE`** |
| 原生是否解析 | 否。只把原文通过 `xbt-native-qr` 事件丢给 WebView | 是。Kotlin 内直接解析成签到参数 |
| 去重 | 同一原文 700ms 内不重复发 | 不做时间节流；由业务状态决定是否处理 |
| 浏览器 | html5-qrcode；若浏览器支持 `BarcodeDetector` 再并行解一遍 | 无浏览器端（原生 App） |
| 相册选图识别 | 不支持 | 支持（ML Kit 处理选中图片） |

证据：

- `Android/app/src/main/java/com/github/enderwolf006/xbt/MainActivity.kt:169`（`BarcodeScanning.getClient()` 未设格式）、`:247-252`（ImageAnalysis）、`:333-338`（取第一条、700ms 去重）
- `Web/src/pages/FullScanner.tsx:401-440`（html5-qrcode + BarcodeDetector）
- CSF `components/QRCodeScanComponent.kt:124-126`（仅 QR）、`:138-150`（相册）、`:190-210`（分析器回调）

> 因此「识别率」在 Android 上主要取决于相机对焦/曝光，而不是两套不同的视觉模型。浏览器端 xbt 用的是 JS 解码库，弱于 ML Kit，属于另一条路径。

## 2. 解析规则

| 项 | xbt | CSF |
|---|---|---|
| 接受格式 | 文本必须含 `mobilelearn.chaoxing.com`；用 `URL` 取 `enc`/`c`，失败再用正则兜底 | ① `SIGNIN:` 前缀（官方 rcode 形态）；② 任意 `http(s)` URL；`enc` 必需，`id`/`aid`/`c` 可选 |
| 解析位置 | Web JS：`Web/src/utils/qr.ts` | 原生 Kotlin：`ChaoxingQRCodeSigner.parseQRCode` |
| 解析失败 | 返回 `null`，扫描回调直接丢弃；**用户看不到任何提示** | 抛 `QRCodeParseException`；显示「二维码解析失败」，开发模式展示原文 |
| 活动 id | 忽略码里的 `id/aid`，用路由上的 `activity_id` | `parseResult.aid ?: 页面 activeId`，提交和过期查询都用码里的 id |
| `SIGNIN:` 码 | 不认。讽刺的是 xbt 自己生成 preSign `rcode` 时用的就是 `SIGNIN:...`（`client.go:660`） | 认，并能解析出 `id/c/enc` |
| `c` 缺失 | 容错；preSign 里 `Code=` 为空 | 允许；无法做过期查询时按「未知」处理，不阻断签到 |

证据：

- `Web/src/utils/qr.ts:7-22`
- `Server/internal/xxt/client.go:660`（xbt 自产 `SIGNIN:`）
- CSF `signer/ChaoxingQRCodeSigner.kt:40-63`（两种格式）、`:72-78`（无 `code` 返回 `null`）、`:127`（用码内 `aid` 提交）

**直接后果**：扫到一个别的手误二维码/旧二维码时，xbt 可能拿着 A 活动的 `enc` 去给 B 活动做 preSign 和提交，最后只得到一条上游错误；CSF 会用码里的活动 id，或至少在选择活动时对不上就解析失败。

## 3. 过期处理

- **xbt**：没有本地过期判定。`PreSign` 发完请求不读响应体（`client.go:645-694`），结果被丢弃；Web 对每个账号固定重试最多 15 次、间隔 1–2 秒，且执行期间新扫到的码会被 `isExecuting` 拦掉，重试始终用旧 `enc`（`FullScanner.tsx:581-640, 662`）。
- **CSF**：扫到码后调用 `signDetail(activePrimaryId, type=1, msg=c)`，`isOver==1` 或返回的 `signCode` 与 `c` 不一致即判过期；连续扫码模式每 1.5 秒复查一次；过期码不再复用，扫描器暂停等待新码（`QRCodeSignScreen.kt:632-640, 1075-1093, 1128`；`ChaoxingQRCodeSigner.kt:72-94`）。

**表现差异**：二维码轮换场景下，xbt 会表现为「一直失败 + 十几次无效请求」，用户不知道要重扫；CSF 会明确提示「二维码已过期，请重新扫码」。

## 4. 多账号流程

- **xbt**：一次扫码 → `/sign/check` → 对全部未签账号 `Promise.all` 并发提交，所有账号共用同一串 `enc`；每个账号独立 15 次重试，没有账号间节奏（`FullScanner.tsx:581-640`）。
- **CSF**：队列串行（自己 → 代签账号），账号之间有延迟（正常 `signTimeSpan`，模型解出验证码时用更短间隔）；同一串 `enc` 可依次给多个账号用，一旦过期就停下来等新码；不可重试的错误跳过当前账号、继续下一个（`ChaoxingSignHandler.kt:110-230`）。

**取舍**：xbt 快，但并发 + 固定重试会放大风控与上游压力；CSF 慢，但每个账号只打一次，过期/失败边界清楚。

## 5. 扫描交互

| 项 | xbt | CSF |
|---|---|---|
| 扫到后的反馈 | 无识别框、无震动、无「不是签到码」提示 | 识别框、震动、错误/过期提示 |
| 连续扫码 | 保持相机，但执行期间新码不处理 | 连续扫码模式，按队列换账号；过期时等待新码 |
| 相册导入 | 无 | 有 |
| 节流 | Web 2 秒 + 执行中忽略；原生 700ms 同串去重 | 无固定节流，按 `enc` 与过期状态去重 |

## 6. 对 xbt 的改法（按优先级）

1. **P0 解析兼容**：支持 `SIGNIN:` 与任意 `https` 链接；`enc` 必需，`id/aid/c` 可选；解析失败必须给用户可见提示（现在静默丢弃）。
2. **P0 活动绑定**：用码里的 `aid/id` 校验当前活动；不一致时拒绝并提示「扫错活动」，不要拿别的活动的 `enc` 去提交。
3. **P0 过期语义**：接入 `signDetail` 过期判定，映射为 S3 的 `qr_expired`；二维码过期**不重试**，提示重新扫码。
4. **P0 重试**：移除每个账号固定 15 次重试（S11/S3 已有范围），只对明确可重试的错误重试。
5. **P1 节奏**：全量并发改为小并发或串行，账号间加节流（S10 限流）。
6. **P2 体验**：相册导入、识别失败提示、连续扫码换账号。

## 7. 澄清：这里不是滑块验证码识别

如果「识别」指的是登录/签到时的滑块验证码，那是另一条链路：xbt 是手写图像打分，CSF 是 YOLO11n + ONNX Runtime，权重许可为 AGPL-3.0，不能直接搬。详见 `docs/sign-parity-plan.md` §8.1，以及已完成的验证码流程修复（`11ce5a5`）。本文只讨论二维码。

## 验证命令与结果

```bash
grep -n "mobilelearn.chaoxing.com\|searchParams.get('enc')\|match(/\[?&\]enc=" Web/src/utils/qr.ts
grep -n "BarcodeScanning.getClient()\|lastQrEmitAt\|emitFirstQr" Android/app/src/main/java/com/github/enderwolf006/xbt/MainActivity.kt
grep -n "MAX_RETRIES\|latestQrDataRef.current || initialQr\|lastScanTimeRef.current < 2000\|Promise.all" Web/src/pages/FullScanner.tsx
grep -n "startsWith(\"SIGNIN:\")\|queryParameter(\"enc\")\|activeId\", (parseResult.aid\|isOver\|signCode" /tmp/cs/app/src/main/java/org/aquamarine5/brainspark/chaoxingsignfaker/signer/ChaoxingQRCodeSigner.kt
grep -n "FORMAT_QR_CODE\|PickVisualMedia\|onScanResult(barcode)" /tmp/cs/app/src/main/java/org/aquamarine5/brainspark/chaoxingsignfaker/components/QRCodeScanComponent.kt
grep -n "delay(1.5.seconds)\|isQRCodeExpired(parseResult)\|parseQRCode(result)" /tmp/cs/app/src/main/java/org/aquamarine5/brainspark/chaoxingsignfaker/screen/QRCodeSignScreen.kt
```

结果：上述行为均能在对应行号直接读到；未运行 `go test`（本次仅文档），未做真机识别率对比。

## 限制

- 没有同批真实二维码做识别率/耗时统计；「Android 解码器相同」指组件相同，不代表两端相机参数、对焦策略、UI 遮挡完全一致。
- `SIGNIN:` 格式的兼容性来自 CSF 的解析实现，不是上游公开协议文档；实际以现场二维码为准。
- 未验证扫描别家活动二维码时上游是否一定拒绝；当前判断基于请求参数（`enc` 与 `activeId` 绑定）和 CSF 的显式校验逻辑。
