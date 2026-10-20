# XBT 签到实现补全方案

> 对比对象：`aquamarine5/ChaoxingSignFaker`（下文简称 CSF）
> 对比基线：xbt `02c8ae5`（验证码修复见分支 `feature/captcha-protocol-fix` `11ce5a5`）；CSF `9ae5398`（2026-10-20）
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
| 人脸 | 单张固定照片；每次签到重传 `face_temp`；`currentFaceId/ifCFP/faceEnc/faceCode`，无 `faceEncAid`；不识别 `[face]`/`checkFace_` | 每账号最多 5 张、每次随机选一张；可下载官方人脸库照片并随机裁剪旋转后上传；`faceResult` 带设备签名；识别 `[face]` 并标记失败照片 | P1（协议）/P2（照片池） |
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
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：已落地——`Server/internal/xxt/sign_params.go` 的 `buildStuSignParams` 统一生成 5 类参数；`deviceCode=HMAC-SHA256(chaoxing_aes_key, "xbt-device-code:v1:<uid>")`（稳定、按账号唯一、不使用硬件标识）；`fid` 由 `chaoxing_fid` 配置；`enc2`/`validate` 只透传状态机写入值；二维码首发不再预取 validate；`if_open_address` 以 `getPPTActiveInfo` 元数据为准（30s 缓存），请求字段仅兜底。测试见 `Server/internal/xxt/sign_params_test.go`、`Server/internal/service/sign_test.go`。

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
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：已落地——`Server/internal/xxt/sign_result.go` 提供 `ParseSignResult` 与 `SignResultKind`（9 类）；`Client.Sign` 返回 `SignResult`，验证码分支保存 `enc2` 并在第二次请求同时携带 `validate`+`enc2`，最多两次请求；`service.ExecuteOne` 对人脸/位置各重试一次，`success/success2` 写签到记录，`already_signed` 写 `source_uid=-1` 记录，`/api/sign/execute` 新增 `error_code`、`retryable`、`distance_m`。位置随机化尚未落地（XBT-S6），当前 `errorLocation_` 重试仍发送用户选定点。测试见 `Server/internal/xxt/sign_result_test.go`、`Server/internal/service/sign_test.go`、`Server/internal/handler/sign_test.go`；接口说明见 `Server/API.md` §5.4。

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
- **S7a 协议补齐（P1）**：补 `faceEncAid=""`；`faceCode` 固定空串与服务端返回二选一以 staging 为准；解析 `[face]`/`checkFace_` 并映射为 `face_required`/`face_rejected` 错误码；同一活动内复用已上传的 `objectId`，不重复上传同一张照片；`check-face-result` 先判 HTTP 状态再解 JSON。
- **S7b 多照片池（P2，延后；产品决策 2026-10-20 更新）**：每账号最多 5 张、随机选择、记录使用次数/失败标记；**允许直接抓取该账号在学习通的官方人脸照片**（原图保存，不做裁剪/旋转/重压）。
- **S7b 实现边界**：仅对已保存凭据的账号抓取；操作者只能操作自己有权代签的账号；不提供按手机号任意查询；签到复用已上传 `objectId`。
- **明确不做**：对官方照片做裁剪/旋转/风格化以规避检测；批量枚举或抓取无关账号的照片。
- **决策记录**：协议层全量对齐 CSF；`signToken`/`cxcid` 仍按「缺失也能签」处理（见 §8.2）。
- 验收：`[face]` 响应触发一次带新照片的重试或明确失败提示；无照片返回明确提示；人脸请求字段与上游客户端一致；同一活动连续执行不重复上传同一张照片。

#### XBT-S7b 多照片池与官方人脸照片导入（P2，延后）
- **输入**：账号凭据、账号已保存的人脸照片；目标账号须是操作者本人或有代签权限的账号。
- **输出**：
  1. 每账号最多 5 张照片，本地保存图片与 `objectId`，记录 `useCount`、`isFailureBefore`。
  2. 每次签到随机选一张；上次失败过的账号优先选「未失败且用过」的照片。
  3. 官方照片导入：`collectionfilephotoEnc` 取 `oldObjectId` → `https://p.cldisk.com/star4/<objectId>/origin.jpg` 下载原图保存；签到前上传一次并复用 `objectId`。
  4. UI：照片列表、上传、删除、失败标记；代签页可管理目标账号照片。
- **验收标准**：
  - 第 6 张被拒绝；删除后计数下降。
  - 随机选择与失败降级可用固定种子在单测中断言。
  - 官方照片导入成功后可签到；对方未设置人脸照片时返回明确提示。
  - 越权访问他人照片返回 403；日志/响应不含密码与 token。
- **不做**：对官方照片做裁剪、旋转、风格化；按手机号任意查询/批量抓取；跨账号共享照片。

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

### 3.5 执行安排（2026-10-20 决策）

- 范围：**协议全量对齐 CSF**，即 XBT-S1..S10；XBT-S5 已在 `feature/captcha-protocol-fix` 完成。**S7b 多照片做，但排在 S12 之后（P2）**，且允许直接抓官方照片。
- 分支：全部在 `feature/captcha-protocol-fix` 上串行提交（未推送），每个 issue 一个 commit，禁止并行改同一工作区。
- 顺序：S1 → S2 → S3 → S4 → S6 → S7a → S8 → S9 → S10；前端「移除固定 5 次重试」在 S3 后并行；E2E 在最后。
- 归属：后端工程师执行 S1-S10，前端工程师执行重试/错误码 UI，测试工程师执行 E2E 回归；PM 只负责范围与验收，不做技术选型。
- 依赖：已用 Paperclip `blockedByIssueIds` 建链；S1→S2→S3→S4→S6→S7a→S8→S9→S10，S3→S11，S10+S11→S12。
- 已知调度缺口：S2 在依赖写入前已被调度器拉起到 `in_progress`，与 S1 并行；已在其 issue 留言要求「S1 完成前不改仓库」。若两者都产生改动，以 `git status` 与逐文件 `git add` 为准，PM 会按提交结果复核。

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

### 8.2 人脸：xbt 与 CSF 的差异（评估）

**大白话版**：两边都能做到「上传照片 → 换一张人脸凭证 → 带参数提交」。差别在照片怎么管：faker 每个账号最多存 5 张、每次签到随机挑一张，哪张曾经失败也记着；还能把学习通里已有的那张人脸照片下载下来，随机裁一下、转一点角度再上传，避免每次都是同一张图。xbt 只有一张固定照片，每次原样重传；服务器如果说「要人脸 / 人脸不对」，xbt 也分不清是哪种，只能把原文吐给用户。

**协议层差异（直接影响能不能签到成功）**

| 项 | xbt 现状 | CSF 做法 | 判断 |
|---|---|---|---|
| 人脸要求判定 | `signDetail` 的 `deepFind`，失败再 GET `preSign` 页面正则抓 `openCheckFaceFlag`（`Server/internal/xxt/client.go:403-480`） | 一次 `getPPTActiveInfo` 读 `openCheckFaceFlag`（`ChaoxingSigner.kt:189-191`） | 归入 S1，xbt 的多余请求可去掉 |
| 提交参数 | `currentFaceId`、`ifCFP=0`、`vpProbability=-1`、`vpStrategy=`，`faceEnc`/`faceCode` 非空才带（`client.go:798-809`） | `currentFaceId`、`ifCFP=0`、`courseId`、`faceEnc`、`faceCode=""`、`faceEncAid=""`（`ChaoxingSigner.kt:327-346`） | xbt 缺 `faceEncAid`；`courseId` 归入 S2 |
| `faceResult` 内容 | `currentFaceId/LiveDetectionStatus/collectStatus/cxtime`（`client.go:715-721`） | 同四项，另在能解出 `clientId` 时加 `cxcid` + `signToken`（`ChaoxingFaceHelper.kt:88-124`） | xbt 登录未保存 `clientId`，无法生成；是否必需待 staging 验证 |
| 校验响应 | 只取 `enc`，无 `enc` 就报错；不判 HTTP 状态（`client.go:739-757`） | 先判 HTTP 状态，再取 `enc`（`ChaoxingFaceHelper.kt:46-66`） | xbt 错误信息质量差，S7a 修 |
| 失败识别 | 无 `[face]`/`checkFace_` 分支（`service/sign.go:535-561` 仅文本映射） | `[face]` 抛 `ChaoxingFaceSignException`，`checkFace_` 单独处理（`ChaoxingSigner.kt:351-358`） | S7a 补，属于响应状态机（S3）的人脸分支 |
| 上传接口/时机 | 每次 execute 上传 `face_temp`，`uploadtype=face_temp`（`client.go:638-706`） | 存照片时上传一次到云盘 `/upload?_from=mobilelearn&_token=`，签到复用 `objectId`（`ChaoxingFaceHelper.kt:206-250`） | xbt 重复上传既慢又增加特征；S7a 至少按活动缓存 |

**产品层差异（影响稳定性与合规）**

| 项 | xbt | CSF |
|---|---|---|
| 照片数量 | 1 张（`users.face_photo` bytea，≤100KB、≤720×1280） | 每账号最多 5 张（`MAX_FACE_IMAGES`），本地文件 + objectId |
| 选择策略 | 固定 | 每次随机；曾失败则只挑「用过且未失败」的（`QRCodeSignScreen.kt:915-927`） |
| 图片来源 | 仅本人登录后手动上传（`/user/face`） | 手动拍照、相册、或直接拉取该账号官方人脸照片 |
| 复用处理 | 每次原样上传同一张 | 官方照片下载后随机裁剪 90–99%、旋转 ±5°、重压缩（`Bitmap.kt:35-80`）；自存照片随机轮换 |
| 代签账号 | 只能本人上传自己的照片 | 可对已导入的代签账号管理照片、抓取其官方照片 |
| 失败反馈 | 返回上游原文 | 标记具体哪张照片失败，提示重拍/换图 |

**结论**

1. **有差异，而且不止参数**：xbt 只实现了「能带人脸提交」的最小路径；CSF 把「人脸照片」当成一个需要轮换和反馈的资源来管理。
2. 协议层 3 处值得先补（`faceEncAid`、`[face]`/`checkFace_` 识别、按活动复用上传结果）。这三处与 S3/S7a 重叠，成本低、可测试。
3. `signToken`/`cxcid` 需要登录时保存并解密 `clientId`，属于新的设备信息链路；先按「缺失也能签」处理，staging 若出现人脸请求被拒再单独立项。
4. 多照片池和随机化能降低「同一张照片反复使用」的特征。**2026-10-20 产品决策更新：做多照片，优先级 P2、排在 S12 之后；允许直接抓取官方人脸照片**。仍不做图像变换规避检测，也不做跨账号批量枚举。

**验证方式**

```bash
grep -n "faceEncAid\|faceCode\|ifCFP" /tmp/cs/app/src/main/java/.../signer/ChaoxingSigner.kt
grep -n "MAX_FACE_IMAGES\|randomOrNull\|randomizeStylizeImage" /tmp/cs/app/src/main/java/.../api/ChaoxingFaceHelper.kt /tmp/cs/.../screen/QRCodeSignScreen.kt
grep -n "current_face_id\|faceEnc\|face_code\|UploadFaceTemp" Server/internal/xxt/client.go Server/internal/service/sign.go
```
结果：CSF `MAX_FACE_IMAGES=5`、随机选择与照片随机化均存在；xbt 无 `faceEncAid`、无 `[face]` 分支、每次 execute 重新上传。人脸相关端到端行为需要真实人脸活动验证，当前环境没有测试账号。

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
- xbt 人脸：`Server/internal/xxt/client.go:446-480`（判定）、`632-770`（上传/校验）、`798-809`（提交参数）、`Server/internal/service/sign.go:240-262`（执行链）、`Server/internal/handler/auth.go:203-242`（存储压缩）
- CSF 人脸：`api/ChaoxingFaceHelper.kt`（`MAX_FACE_IMAGES`、`checkFaceResultAndGetEnc`、`signToken`、照片存取）、`components/FaceRecognitionComponent.kt:136-176`（官方照片导入）、`utilities/Bitmap.kt:35-80`（随机裁剪旋转）、`screen/QRCodeSignScreen.kt:915-927`（随机选图）
- CSF 元数据：`app/.../signer/ChaoxingSigner.kt`（`URL_SIGN_INFO`、`isCaptchaRequired/isFaceRequired/isPositionRequired`）
- CSF 提交参数：`ChaoxingQRCodeSigner.kt`、`ChaoxingLocationSigner.kt`、`ChaoxingGestureSigner.kt`、`ChaoxingPasswordSigner.kt`、`ChaoxingPhotoSigner.kt`
- CSF 状态机：`ChaoxingSigner.kt`（`checkSignResult`、`checkAlreadySign`、`checkExpiredSign`）
- CSF 验证码：`ChaoxingSigner.kt`（`getCaptchaConf`、`getCaptchaImageV2`、`checkCaptchaResult`）
- CSF 位置随机化：`ChaoxingSigner.kt`（`addLocationDataParameter`）
- CSF 活动发现：`app/.../api/ChaoxingActivityHelper.kt:40-200`
- CSF 验证码模型：`app/src/main/java/.../api/ChaoxingCaptchaPredictor.kt`（640×640、score 0.25、NMS 0.45）、`app/src/main/assets/captcha.ort`（元数据：`Ultralytics YOLO11n model trained on config/block.yaml`、`AGPL-3.0 License`）、`components/CaptchaHandlerDialog.kt`（仅用 shadeImage 预测、单次校验、失败回落人工）
- xbt 候选轮询：`Server/internal/xxt/captcha.go:24-96`（`for _, x := range candidates` → `verifyCaptchaAtX`）、`captcha.go:310-345`（候选最多 40 个）
