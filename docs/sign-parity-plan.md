# XBT 签到实现补全方案

> 对比对象：`aquamarine5/ChaoxingSignFaker`（下文简称 CSF）
> 对比基线：xbt `02c8ae5`；CSF `9ae5398`（2026-10-20）
> 角色：产品方案，不含实现代码；技术选型由架构师确认。

## 0. 结论

xbt 的签到主链路（活动发现 → 选人 → check → execute → 落库）已经完整，但**协议层落后于 CSF**。差距集中在 6 处：

1. 缺少 `getPPTActiveInfo` 元数据（`ifNeedVCode`、`openCheckFaceFlag`、`ifopenAddress`、位置圆心/半径、签退信息）。
2. 提交参数不完整：无 `courseId`、`deviceCode`、`enc2`、`location`/`locationResult`、`mockData`、`ifCFP`/`faceEncAid`。
3. 响应只识别 `success` 和“您已签到过了”，未处理 `validate_`、`checkFace_`、`errorLocation_`、`success2`、`[face]`、二维码过期。
4. 验证码实现存在确定性缺陷：请求使用**独立 cookie jar**，未调用 `captcha/get/conf`，校验请求缺 `iv`、`t=a`，且同一 challenge 内多次试 x。
5. check 只查本地 `sign_records`，无法识别他人在官方客户端的自签；preSign 仅二维码类型、且不走 `ext`。
6. 缺少 CSF 已支持的签退（type 74）、位置随机化、全类型位置参数。

建议按 **P0 协议正确性 → P1 成功率/体验 → P2 签退与观测** 三期落地。最小可交付切片：**二维码签到 + 普通签到**跑通完整元数据、参数、状态机、验证码与上游状态校验，再复用到其余类型。

## 1. 目标 / 非目标

### 目标
- 在 xbt 三端架构内，把 CSF 已验证的签到协议能力以**服务端统一实现**的方式补齐，Web/Android/分享链接共用。
- 显著提升：签到成功率、验证码通过率、签到状态准确性（他签识别）、位置签到有效性。
- 所有行为可测试、可观测、可回滚。

### 非目标
- 不破解手势码/签到码，不做代答、不绕过监考或人脸核验。
- 不复制 CSF 的 AGPL 代码或 `captcha.ort` 模型资产（见 §7 许可证）。
- 不改变现有权限模型、白名单模型和账号加密方式。
- 不在本期做 IM 群聊签到、校园定制版适配。

## 2. 逐项对比

| 能力 | xbt 现状 | CSF 做法 | 差距等级 |
|---|---|---|---|
| 活动发现 | `taskactivelist`，仅取 `activeType==2` 或名称含“签到”，只保留 `id/name`（`Server/internal/xxt/client.go:321-400`） | 保留 `type 2/74`、`status=1`、`userStatus`、`ext`，按 20 分钟窗口推荐 | P1 |
| 活动元数据 | `/newsign/signDetail` + 额外 `preSign` 解析人脸标志（`client.go:403-445`） | `/v2/apis/active/getPPTActiveInfo`，一次返回 `ifNeedVCode`、`openCheckFaceFlag`、`ifopenAddress`、位置圆心/半径、`signInId/signOutId`、`ifphoto`、`ifrefreshewm`、`otherId` | P0 |
| 预签到 | 仅二维码；GET，无 `ext`；只发 `analysis`/`analysis2`（`client.go:481-548`） | 所有类型；POST `ext`；302/403 重登一次；解析 `primaryAttend.status`/`signstatus` 得到已签/截止 | P0 |
| 通用提交参数 | `activeId/uid/clientip/appType/fid/name/validate`；`fid` 为空，无 `courseId`、`deviceCode`、`enc2`、`vp*`（`client.go:777-810`） | 含 `courseId`、`deviceCode`、配置化 `fid`、`enc2`、`vpProbability=-1`、`vpStrategy=` | P0 |
| 类型专属参数 | 位置：`address/latitude/longitude/ifTiJiao`；二维码：`enc` + `location`；手势/码：`signCode` | 位置/手势/码/二维码均支持 `location` + `locationResult` + `mockData`；人脸含 `currentFaceId/ifCFP/faceEnc/faceCode/faceEncAid` | P0 |
| 响应状态机 | 仅 `success`、`您已签到过了`（`Server/internal/service/sign.go:268-288`） | 识别 `success/success2/validate_/checkFace_/errorLocation_/[face]/签到失败，请重新扫描/您已签到过了` | P0 |
| 验证码 | 自建灰度模板匹配；新 cookie jar；无 `get/conf`；校验缺 `iv/t`；同一 challenge 多次试 x（`Server/internal/xxt/captcha.go:24-130`） | 使用会话 cookie；`get/conf` 取 `t`；生成 `captchaKey/iv/token`；校验带 `iv/t=a`；模型只预测一次，失败重取 challenge | P0 |
| 位置 | 直接用用户选定坐标；无范围校验/随机化；仅二维码可带位置 | 在 `locationRange` 内随机取点；半径外返回 `errorLocation_<distance>`；首次失败关闭随机化重试 | P1 |
| 人脸 | 上传 `face_temp` + `check-face-result`，带 `currentFaceId/faceEnc/faceCode` | 同核心，另有 `faceEncAid`、`[face]` 失败重试、元数据提前判定 | P1 |
| 签到状态 | 只查本地 `sign_records`（`sign.go:564-600`） | preSign 解析上游状态，能识别官方自签 | P0 |
| 签退 | 无 | `type=74`，`signOutId/signOutPublishTimeStamp` | P2 |
| 客户端重试 | Web 对 `/sign/execute` 固定重试 5 次（`Web/src/pages/SignDetail.tsx:399-448`） | 二维码过期只重新扫码；验证码/位置按明确错误类型重试 | P0 |
| 会话失效 | 会话 24h；签到链路不处理 403（`client.go:172-190`） | 403 触发一次重新登录后重试 | P0 |

## 3. 方案

### 3.1 最小可交付切片 M0（1 个迭代）

范围：**二维码 + 普通签到**，只做服务端协议层，不改 UI 信息架构。

包含：XBT-S1、XBT-S2、XBT-S3、XBT-S4、XBT-S5。
完成标志：用两个真实测试账号（1 个活动）完成 staging E2E；录制的请求/响应断言全部通过；现有 `Server` 测试不回归。

### 3.2 P0：协议正确性

#### XBT-S1 统一签到元数据
- **输入**：`activity_id`、`course_id`、`class_id`、目标账号凭据。
- **输出**：扩展 `xxt.SignDetail`：`IfNeedVCode`、`IfOpenAddress`、`LocationLatitude`、`LocationLongitude`、`LocationRange`、`SignInID`、`SignOutID`、`SignOutPublishTime`、`Ext`。
- **实现边界**：新增上游方法读取 `getPPTActiveInfo`；`signDetail` 保留为降级路径；缓存加短 TTL，避免每活动重复请求。
- **不做**：不重写活动列表；不引入新表关系。
- **验收标准**：
  - 固定 fixture 下解析出上述全部字段；缺字段时返回零值且不 panic。
  - `/api/sign/activities` 新增 `if_need_vcode`、`if_open_address`、`location_latitude`、`location_longitude`、`location_range`，旧字段不变。
  - 同一活动 30s 内重复请求只打一次上游（日志计数断言）。

#### XBT-S2 请求参数构造器
- **输入**：活动元数据 + 用户选择的位置 + 二维码 `enc/c` + 已保存的 challenge 状态。
- **输出**：按签到类型生成完整参数集。
- **验收标准**（表驱动测试）：
  - 5 种类型均包含 `activeId/uid/courseId/appType/name/deviceCode`。
  - `if_open_address=true` 时，类型 2/3/4/5 均包含 `location`、`locationResult`；类型 4 另含 `address`、`ifTiJiao=1`。
  - `enc2` 仅在状态机写入后出现；不得凭空生成。
  - 同一账号多次调用 `deviceCode` 稳定；不同账号不同。
  - 二维码请求不再把 `validate` 预置为首发参数（首发由服务端状态机决定）。

#### XBT-S3 响应状态机
- **输出**：类型化结果：`success`、`success2`（迟到/已结束）、`already_signed`、`captcha_required(enc2)`、`face_required(enc2)`、`location_out_of_range(distance)`、`qr_expired`、`session_expired`、`upstream_error(message)`。
- **重试规则**：
  - 验证码：保存 `enc2`，取一次 validate 后重试**一次**。
  - 人脸：`[face]`/`checkFace_` 触发一次人脸重试。
  - 位置：`errorLocation_` 时关闭随机化重试**一次**，仍失败则返回距离。
  - 二维码过期：不自动重试，返回“请重新扫码”。
  - 其他错误：不自动重试。
- **验收标准**：
  - 9 类原始响应逐一映射正确（表驱动）。
  - `validate_abc` 后第二次请求携带 `validate` 与 `enc2=abc`；第二次失败不产生第三次。
  - 上游返回“您已签到过了”时写入 `source_uid=-1` 的 `sign_records`，后续 check 不再请求上游签到。
  - 仅 `success/success2` 写签到记录；`already_signed` 写学习通来源记录。
- **不做**：不做无限重试、不做并发去重以外的调度。

#### XBT-S4 preSign 全类型 + 上游状态 + 会话恢复
- **输入**：活动 `ext`、固定参数、用户凭据。
- **输出**：`preSign` 状态：`ready/expired/already_signed/no_permission`；`/api/sign/check` 合并本地记录与上游状态。
- **验收标准**：
  - 普通/二维码/手势/位置/签到码均执行 preSign（二维码额外带 `rcode`）。
  - 403 时重登一次并重试；重登失败返回 `session_expired`，不循环。
  - 上游已签、本地无记录时，`/sign/check` 返回 `signed=true`、`record_source=-1`、`record_source_name="学习通"`。
  - 已截止活动返回可读提示，不写记录。
  - 现有 `HasActivityInCourse` 授权逻辑不变。

#### XBT-S5 验证码流程修复
- **输出**：可配置的 `captchaId`；`get/conf` → 生成 `captchaKey/token/iv` → 取图 → 单次校验 → 失败重取 challenge（最多 N 次）。
- **必须修复**：
  1. 验证码请求复用用户会话 cookie jar，不再 `cookiejar.New(nil)`。
  2. 先请求 `captcha/get/conf` 获取 `t`，token 使用服务端 `t`。
  3. 校验请求携带 `iv`、`t=a`。
  4. 同一 challenge token 只提交一次 x；失败重新取图，不复用 token。
  5. 候选不再轮询：本地打分排序后只提交 top-1（现状 `candidates` 最多 40 个逐个试，见 §8.1）。
- **验收标准**：
  - 单测断言 token 格式 `md5(...)+":"+expiry`、`iv` 存在、校验参数含 `iv`/`t=a`。
  - 上游 mock 第一次返回 `result=false`，客户端重新取 challenge，而不是换 x 重试同一 token。
  - `captchaId` 可通过配置切换，默认值以 staging 实测为准。
  - 失败不把内部 `validate` 字符串直接暴露给前端，映射为可读错误码。
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：上述 1–5 已落地——`fetchCaptchaValidate` 复用会话 jar、`get/conf` 取 `t` 派生 `captchaKey`/`token`/`iv`、校验带 `iv`+`t=a`、每个 challenge 只提交一次 top-1、最多 3 个 challenge；埋点由 `logCaptchaAttempt` 输出 `challenge_id/candidate_count/top1_x/upstream_result/attempt`。回归测试见 `Server/internal/xxt/captcha_test.go`。

### 3.3 P1：成功率与体验

#### XBT-S6 位置随机化与范围校验
- 在 `locationRange` 内均匀取点（保留 6 位小数）；服务端校验预设点到圆心距离，超范围直接拒绝并提示。
- 请求携带 `location`、`locationResult`、`mockData`。
- 验收：10k 次采样全部落在半径内；`errorLocation_12` 返回 `distance_m=12` 且只重试一次；前端位置选择器展示“距签到点 N 米 / 超出范围”。

#### XBT-S7 人脸流程加固
- 用 `openCheckFaceFlag` 提前判定；补 `faceEncAid`；`[face]` 失败重试一次；人脸照片按活动缓存上传结果。
- 验收：`[face]` 响应触发一次重试；无照片返回明确提示；人脸请求字段齐全。

#### XBT-S8 活动发现补全
- 保留 `type=74`、`status`、`userStatus`、`ext`；过期/未开始活动标记而非静默丢弃。
- 验收：签退活动以 `is_sign_out=true` 出现在列表；`ext` 进入缓存；旧客户端字段不破坏。

### 3.4 P2：签退与可观测性

#### XBT-S9 签退
- 新增 `/api/sign/sign-out`，依赖主签到成功与 `signOutId`；分享链接是否支持需单独确认。
- 验收：主签到未完成时拒绝；签退成功后写入独立记录，不覆盖签到记录。

#### XBT-S10 指标与限流
- 结构化日志字段：`activity_id/user_uid/sign_type/result_code/attempt/upstream_status`；指标：成功率、验证码通过率、`validate` 触发率、`errorLocation` 率、上游 403 率、p95 时延。
- 验收：每次 execute 产生一条结果日志；可按结果码聚合；单用户并发超过阈值返回 429。

## 4. 契约变更

### 4.1 API
- `GET /api/sign/activities`：新增字段（向后兼容）：
  `if_need_vcode: boolean`、`if_open_address: boolean`、`location_latitude: number`、`location_longitude: number`、`location_range: number`。
- `POST /api/sign/execute`：
  - `special_params` 允许 `latitude/longitude/description` 用于所有类型（不再仅类型 4）。
  - 服务端不再接受客户端传入 `validate`；验证码由服务端完成。
  - 响应新增 `error_code`（见 XBT-S3）、`retryable: boolean`、`distance_m?: number`。
- `POST /api/sign/check`：新增 `checked_upstream: boolean`，`record_source=-1` 表示官方自签。
- 分享链接：`SharedSign` 需要传递位置参数；否则分享场景仅支持无位置活动（明确限制）。

### 4.2 DB
- `sign_activities` 增加列：`if_need_vcode`、`if_open_address`、`location_latitude`、`location_longitude`、`location_range`、`sign_out_id`、`sign_out_publish_time`、`ext_json`。全部可空/带默认值，向后兼容。
- `enc2` 与验证码 challenge 属于短时状态：默认进程内 TTL 缓存；仅当部署多副本时再引入共享存储。**不落业务表**。

### 4.3 Web / Android
- Web 移除 `/sign/execute` 的固定 5 次重试；按 `error_code` 决定“重新扫码 / 重试 / 提示”。
- 位置选择器对类型 2/3/4/5 在 `if_open_address=true` 时展示；显示范围校验结果。
- Android 壳无协议改动，仅随 Web 资源更新。

## 5. 验收与验证

### 5.1 自动化
- `Server/internal/xxt`：参数表测试、响应状态机表测试、验证码 token/校验参数测试、位置采样不变量测试。
- `Server/internal/service`：check 合并上游状态、already_signed 落库、重试次数上界。
- `Server/internal/handler`：activities 新字段、execute 错误码映射、分享链接位置参数。
- 所有上游交互使用 `httptest` fixture，禁止测试触网。

### 5.2 手工 / staging
1. 准备 2 个测试账号、1 门测试课程、5 种签到各 1 个活动。
2. 逐类型执行：本人 + 代签；记录请求参数与结果。
3. 专项：验证码活动、位置活动（范围内/范围外）、人脸活动、已截止、官方已签、会话过期。
4. 回归：分享链接、批量代签、并发 5 人、断网重试。

### 5.3 已执行的对比核验
```bash
git clone --depth 1 https://github.com/aquamarine5/ChaoxingSignFaker /tmp/cs
grep -n "getPPTActiveInfo\|enc2\|deviceCode\|locationResult" ...
grep -n "captchaID\|cookiejar.New\|runEnv" Server/internal/xxt/captcha.go
grep -n "activeType != 2\|taskactivelist\|signDetail" Server/internal/xxt/client.go
```
结果：上述差异均可在两份源码中定位；CSF 基线 `9ae5398`，xbt 基线 `02c8ae5`。Go 工具链不在当前执行环境 PATH 中，因此本次未运行 `go test`（方案阶段，无代码变更）。

## 6. 成功指标

| 指标 | 基线 | 目标（上线 2 周） |
|---|---|---|
| 签到成功率（不含已签） | 现状埋点缺失，先补 | ≥ 95% |
| 验证码通过率 | 现状未知 | ≥ 85% |
| 位置签到一次通过率 | 现状未知 | ≥ 90% |
| 他签识别准确率（check） | 仅本地 | 上游已签识别 ≥ 99% |
| 单次签到 p95 时延 | 现状未知 | ≤ 4s |
| 无效重试占比 | Web 固定 5 次 | 0（仅类型化重试） |

## 7. 风险与依赖

1. **许可证**：CSF 为 AGPLv3，xbt 为 GPLv3；AGPLv3 与 GPLv3 不兼容。禁止直接复制 CSF 代码或 `captcha.ort`。本方案只复用**协议行为**并独立实现；若引入模型，需单独授权评估。
2. **上游风控/接口漂移**：`getPPTActiveInfo`、验证码参数可能随时变化；所有端点与 `captchaId` 配置化，保留降级路径。
3. **deviceCode 隐私**：不使用硬件标识；按账号生成并持久化随机值。
4. **账号风险**：高频签到/重试可能触发风控；服务端限制单账号并发与重试次数。
5. **合规**：仅用于授权范围内的课程签到辅助；不扩展作弊能力。

## 8. 开放问题（附推荐）

| # | 问题 | 选项 | 推荐 |
|---|---|---|---|
| Q1 | `deviceCode` 生成策略 | A. 每账号稳定随机值；B. 模拟 Android 设备指纹 | A：隐私风险低、实现简单，先验证上游接受度 |
| Q2 | 验证码识别 | A. 保留现有启发式；B. 引入 CSF 的 `captcha.ort`（YOLO11n）；C. 自训/授权非 AGPL 检测器 | **A 先行**：先修 S5 协议 + 埋点，单 challenge 通过率 ≥85% 即维持 A。**B 排除**：权重元数据自述 `AGPL-3.0 License`，与 xbt 的 GPLv3 不兼容。仅当 A 修完仍 <70% 才进入 C。详见 §8.1 |
| Q3 | 签退是否本期做 | A. 本期；B. 下一期 | B：依赖主签到稳定性，且需产品确认分享场景 |
| Q4 | 多副本 `enc2` 状态 | A. 进程内；B. 共享存储 | A：当前单实例部署；扩容前再评估 |

### 8.1 验证码识别：CSF 模型 vs xbt 启发式（评估）

**大白话版**：xbt 现在靠「贴图比对」找缺口，faker 用训练好的 AI 直接看缺口在哪；AI 更准更稳，但不能用（许可冲突 + 部署变重）。xbt 验证码失败的真正原因是流程参数错了，不是看得不准。所以先修流程、加统计，用「一次就过」的比例决定要不要自己训一个模型。

**结论先行**

1. xbt 目前**没有识别模型**：`detectSlideXCandidates` 是两路手写打分（`cutoutImage` alpha 掩码的灰度差 + 列边缘剖面投票），无参数、无训练集。所以问题不是"换哪个模型"，而是"启发式 vs 学习式检测器"。
2. 单看识别能力，**CSF 的方案更强**：YOLO11n 单类检测器直接回归缺口框，只吃 `shadeImage`，不依赖 `cutoutImage` 的 alpha 与两图对齐；输出唯一 x 且带置信度，失败时回落人工滑动。
3. 但**这个模型不能进 xbt**：`captcha.ort` 元数据自述 `Ultralytics YOLO11n model trained on config/block.yaml` 与 `AGPL-3.0 License (https://ultralytics.com/license)`；xbt 为 GPLv3，分发该权重即许可冲突。另外 xbt 服务端是 Go，跑 `.ort` 需要 ONNX Runtime（CGO 绑定或独立 sidecar），成本不小。
4. **当前 xbt 验证码失败的主因在协议层，不在视觉层**：空 cookie jar、无 `get/conf`、校验缺 `iv`/`t=a`、同一 token 最多提交 40 个候选 x。这些不修，换任何模型都过不了。

**能力对比**

| 维度 | xbt 现状（启发式） | CSF（`captcha.ort`） |
|---|---|---|
| 形态 | 手写打分函数，零依赖 | 5.7 MB YOLO11n 单类检测器，ONNX Runtime |
| 输入 | `cutoutImage` alpha 掩码 + `shadeImage` 灰度 | 仅 `shadeImage`，补边至正方形后缩放到 640×640 |
| 输出 | 最多 40 个候选 x，逐个提交上游 | 单框 `(x,y,w,h,score)`，score<0.25 丢弃，NMS IoU 0.45 |
| 单 challenge 上游校验次数 | 1–40（同一 token） | 1，失败重取 challenge |
| 算力/依赖 | 纯 Go 标准库，毫秒级 | native 库 + 每次约 2.6 GFLOPs（Ultralytics 公布值，未实测）；ARM 端数十至百毫秒为估算 |
| 失败处理 | 换 x 猜 | 预测失败自动回落人工滑动 |
| 可解释/可调 | 可打印分数与候选 | 黑盒，需重训 |
| 许可 | GPLv3（自有） | 权重自述 AGPL-3.0 |
| 准确率证据 | 无数据集、无埋点 | 无公开指标，仅作者自用 |

**准确率目前无法给出数字**：需要同一批真实 challenge 的标注结果；当前无样本集，环境也无 `go` 工具链。计划用 S5 埋点采集（仅存图片 digest 与上游 `result`，不落原图或账号信息），在 ≥200 个 challenge 上比较 top-1 通过率、单 challenge 通过率、平均校验次数。

**决策门槛**

- 修完 S5 后单 challenge 通过率 ≥85%：维持启发式，不引入模型。
- 70%–85%：先做零许可风险的增强（尺寸/DPR 对齐、掩码采样步长、边缘剖面权重、候选收敛到 top-1）。
- <70%：进入选项 C——自行标注训练**非 AGPL** 检测器（Apache-2.0 实现）或采购授权；以 sidecar（Python + ONNX Runtime）部署，Go 主进程只传图取 x。

**对 XBT-S5 的影响**：候选轮询改为「本地排序取 top-1 提交，失败重取 challenge」，并新增埋点字段（`captcha_challenge_id`、`candidate_count`、`top1_x`、`upstream_result`、`attempts`）。

## 9. 排期估算（1 人后端 + 0.5 人前端）

| 阶段 | 内容 | 估算 |
|---|---|---|
| P0 | S1-S5 + 测试 | 5-7 人日 |
| P1 | S6-S8 | 3-5 人日 |
| P2 | S9-S10 | 3-5 人日 |
| 联调 | staging E2E + 回归 | 2 人日 |

## 附录：证据索引

- xbt 活动发现/过滤：`Server/internal/xxt/client.go:321-400`
- xbt 活动元数据：`Server/internal/xxt/client.go:403-445`
- xbt preSign：`Server/internal/xxt/client.go:481-548`
- xbt 提交参数与类型分支：`Server/internal/xxt/client.go:777-880`
- xbt 结果处理：`Server/internal/service/sign.go:232-288`
- xbt 本地状态：`Server/internal/service/sign.go:564-600`
- xbt 验证码：`Server/internal/xxt/captcha.go:24-130,170-190,199+`
- xbt 前端固定重试：`Web/src/pages/SignDetail.tsx:399-448`
- CSF 元数据：`app/.../signer/ChaoxingSigner.kt`（`URL_SIGN_INFO`、`isCaptchaRequired/isFaceRequired/isPositionRequired`）
- CSF 提交参数：`ChaoxingQRCodeSigner.kt`、`ChaoxingLocationSigner.kt`、`ChaoxingGestureSigner.kt`、`ChaoxingPasswordSigner.kt`、`ChaoxingPhotoSigner.kt`
- CSF 状态机：`ChaoxingSigner.kt`（`checkSignResult`、`checkAlreadySign`、`checkExpiredSign`）
- CSF 验证码：`ChaoxingSigner.kt`（`getCaptchaConf`、`getCaptchaImageV2`、`checkCaptchaResult`）
- CSF 位置随机化：`ChaoxingSigner.kt`（`addLocationDataParameter`）
- CSF 活动发现：`app/.../api/ChaoxingActivityHelper.kt:40-200`
- CSF 验证码模型：`app/src/main/java/.../api/ChaoxingCaptchaPredictor.kt`（640×640、score 0.25、NMS 0.45）、`app/src/main/assets/captcha.ort`（元数据：`Ultralytics YOLO11n model trained on config/block.yaml`、`AGPL-3.0 License`）、`components/CaptchaHandlerDialog.kt`（仅用 shadeImage 预测、单次校验、失败回落人工）
- xbt 候选轮询：`Server/internal/xxt/captcha.go:24-96`（`for _, x := range candidates` → `verifyCaptchaAtX`）、`captcha.go:310-345`（候选最多 40 个）
