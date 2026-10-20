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
- 不复制 CSF 的 Kotlin 源码。滑块模型 `captcha.ort` 已在 XBT-S16 按 AGPLv3 作为独立资产引入（整库同步转 AGPLv3，见 §8.1）。
- 不改变现有权限模型、白名单模型和账号加密方式。
- 不在本期做校园定制版适配；IM 群聊签到来源按 XBT-S14 范围做（`/sign/activities` 刷新时按需抓取，只发现 + 跳现有签到流程，不自动提交）。

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
  - `if_open_address=true` 时，5 种类型（0/2/3/4/5）均包含 `location`、`locationResult`；类型 4 另含 `address`、`ifTiJiao=1`。
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
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：已落地——`Server/internal/xxt/sign_result.go` 提供 `ParseSignResult` 与 `SignResultKind`（9 类）；`Client.Sign` 返回 `SignResult`，验证码分支保存 `enc2` 并在第二次请求同时携带 `validate`+`enc2`，最多两次请求；`service.ExecuteOne` 对人脸/位置各重试一次，`success/success2` 写签到记录，`already_signed` 写 `source_uid=-1` 记录，`/api/sign/execute` 新增 `error_code`、`retryable`、`distance_m`。位置随机化见 XBT-S6（`errorLocation_` 重试改为关闭随机化并发送未随机化的基准点）。测试见 `Server/internal/xxt/sign_result_test.go`、`Server/internal/service/sign_test.go`、`Server/internal/handler/sign_test.go`；接口说明见 `Server/API.md` §5.4。

#### XBT-S4 preSign 全类型 + 上游状态 + 会话恢复
- **输入**：活动 `ext`、固定参数、用户凭据。
- **输出**：`preSign` 状态：`ready/expired/already_signed/no_permission`；`/api/sign/check` 合并本地记录与上游状态。
- **验收标准**：
  - 普通/二维码/手势/位置/签到码均执行 preSign（二维码额外带 `rcode`）。
  - 403 时重登一次并重试；重登失败返回 `session_expired`，不循环。
  - 上游已签、本地无记录时，`/sign/check` 返回 `signed=true`、`record_source=-1`、`record_source_name="学习通"`。
  - 已截止活动返回可读提示，不写记录。
  - 现有 `HasActivityInCourse` 授权逻辑不变。
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：已落地——`Client.PreSign` 改为 POST `ext`（`FixedParams.Ext` 来自 `getPPTActiveInfo`），二维码额外带 `rcode`；403 时 `PreLogin` 一次并重试一次，第二次 403 或重登失败返回 `xxt.ErrSessionExpired`；`xxt/presign.go` 解析 `ready/expired/already_signed/no_permission`。`service.ExecuteOne` 对全部类型先 preSign：`already_signed` 写 `source_uid=-1` 记录，`expired` 返回 `activity_expired` 且不写记录，`session_expired` 映射为同名错误码。`CheckSignStates` 对本地无记录的用户用其凭据 preSign 合并上游状态，上游已签时落 `source_uid=-1` 记录并返回 `checked_upstream=true`；凭据/网络失败保留本地状态，不影响整次查询。测试见 `Server/internal/xxt/presign_test.go`、`Server/internal/service/sign_test.go`；接口说明见 `Server/API.md` §5.3/§5.4。

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
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：上述 1–5 已落地——`fetchCaptchaValidate` 复用会话 jar、`get/conf` 取 `t` 派生 `captchaKey`/`token`/`iv`、校验带 `iv`+`t=a`、每个 challenge 只提交一次 top-1、最多 3 个 challenge；埋点由 `logCaptchaAttempt` 输出 `challenge_id/captcha_model/candidate_count/top1_x/upstream_result/attempt`。S16 起模型可用时 top-1 即模型 x；回归测试见 `Server/internal/xxt/captcha_test.go`。S20 修正 `get/conf`：该端点缺 `callback` 时返回 200 + `{"10013":"CALLBACK ERROR"}`（无 `t`），现在固定携带 `callback=cx_captcha_function`，解析接受顶层及 `data/result/conf/config` 一层嵌套的 `t`，失败返回 `captcha_conf_*` 错误码并附带截断、脱敏的响应片段。

### 3.3 P1：成功率与体验

#### XBT-S6 位置随机化与范围校验
- 在 `locationRange` 内均匀取点（保留 6 位小数）；服务端校验预设点到圆心距离，超范围直接拒绝并提示。
- 请求携带 `location`、`locationResult`、`mockData`。
- 验收：10k 次采样全部落在半径内；`errorLocation_12` 返回 `distance_m=12` 且只重试一次；前端位置选择器展示“距签到点 N 米 / 超出范围”。
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：已落地——`Server/internal/xxt/location.go` 提供 haversine 距离、预设点范围校验与面积均匀圆内采样（6 位小数）；`service.ExecuteOne` 在 preSign 之前按活动元数据校验用户预设点，超范围直接返回 `location_out_of_range` + `distance_m`，未选点时以元数据圆心生成采样点；`errorLocation_` 首次重试发送未随机化的基准点（预设点或元数据圆心），第二次失败返回上游距离；`stuSignajax` 构造器对类型 0/2/3/4/5 在 `if_open_address=true` 时统一携带 `location`/`locationResult`（仅后者含 `mockData`）。Web 位置选择器、地图选点和已选位置展示“距签到点 N 米 / 超出范围”，地图选点超范围时禁止确认。测试见 `Server/internal/xxt/location_test.go`、`Server/internal/xxt/sign_params_test.go`、`Server/internal/service/sign_test.go`。

#### XBT-S19 位置圆心缺失防护
- 现象：`getPPTActiveInfo` 返回 `locationLatitude=0`/`locationLongitude=0` 且 `ifopenAddress=1` 时，未选点的位置签到不带有效坐标，上游按 `(0,0)` 计算距离（约 1200 万米）。
- 处理：解析层把 `(0,0)` 视为缺失，并在标量字段无效时依次读取 `location`（对象或 JSON 字符串）、`address`/`locationInfo` 对象与顶层 `latitude`/`longitude`；执行层对 `sign_type=4` 在未提供 `special_params.latitude/longitude` 且无可用圆心/半径（含元数据读取失败）时返回 `location_point_required`，不发起 `preSign`/`stuSignajax`。已选点行为不变。
- 实现状态（2026-10-20）：`Server/internal/xxt/client.go` 的 `parseActivityCenter`、`Server/internal/xxt/location.go` 的 `ValidGeoPoint`、`Server/internal/service/sign.go` 的 `prepareSignLocation`；Web 错误码映射为 `relocate` 并提示「请先在地图选点」。测试见 `client_sign_metadata_test.go`、`sign_test.go`。

#### XBT-S24 二维码签到缺坐标的错误区分
- 现象：二维码签到（`sign_type=2`）在活动开启定位但没有可用圆心（`0,0` 或元数据读取失败）且未选点时，构造器只发送 `latitude=-1/longitude=-1`，上游 `errorLocation_<千万米>` 被映射为 `location_out_of_range`。
- 处理：`ExecuteSignRequest.locationProvided` 记录本次执行是否携带可用坐标（采样成功或用户显式选点）；`signFailure` 仅在该值存在时返回 `location_out_of_range` + 距离，否则 `errorLocation_` 一律升级为 `location_point_required`，文案「该签到要求定位，请先在地图选点或允许定位」。没有基准点时 `submitSign` 不再发起无意义的 `errorLocation_` 重试。`sign_params.go` 字段形状不变。
- 前端：`FullScanner`/`SignDetail` 收到 `location_point_required` 时打开 `LocationPickerSheet`（含地图选点与浏览器定位），用户选中后带 `special_params.latitude/longitude/description` 重试一次；取消则停在错误提示。
- 测试：`TestSignExecuteQRWithoutCoordinatesReportsMissingPoint`、`TestSignExecuteQRWithCoordinatesKeepsOutOfRangeDistance`、`TestSignExecuteQRWithCoordinatesInsideCircleSigns`、`TestSignExecuteQRWithoutLocationRequirementStillSigns`。

#### XBT-S7 人脸流程加固
- **S7a 协议补齐（P1）**：补 `faceEncAid=""`；`faceCode` 固定空串与服务端返回二选一以 staging 为准；解析 `[face]`/`checkFace_` 并映射为 `face_required`/`face_rejected` 错误码；同一执行内复用已上传的 `objectId`，不重复上传同一份变换结果；`check-face-result` 先判 HTTP 状态再解 JSON。
- **S7b 多照片池（P2，延后；产品决策 2026-10-20 更新）**：每账号最多 5 张、随机选择、记录使用次数/失败标记；**允许直接抓取该账号在学习通的官方人脸照片**；池内保存**基线图**（官方照为下载原图），**每次签到执行前在内存中裁剪 + 旋转 + 重压一次**，变换结果只用于本次上传、不落库（用户评论 `396fa4c1`，方案 A）。
- **S7b 实现边界**：仅对已保存凭据的账号抓取；操作者只能操作自己有权代签的账号；不提供按手机号任意查询；账号无人像时签发流程自动拉取一次官方照，同一执行内重试复用变换结果。
- **明确不做**：批量枚举或抓取无关账号的照片；风格化/换脸类处理（裁剪/旋转/重压是强制项，不属于「不做」）。
- **决策记录**：协议层全量对齐 CSF；`signToken`/`cxcid` 仍按「缺失也能签」处理（见 §8.2）。
- 验收：`[face]` 响应触发一次带新照片的重试或明确失败提示；无人像且拉取失败返回 `face_photo_unavailable`；人脸请求字段与上游客户端一致；每次执行重新变换，同一执行内的重试不重复上传。
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：S7a 已落地——`buildStuSignParams` 在带 `currentFaceId` 时固定发送 `faceCode=""`、`faceEncAid=""`，不再回传 `check-face-result` 的 `code`；`CheckFaceResult` 先判 HTTP 2xx 再解析 JSON，非 2xx 错误带状态码，无 `enc` 保留可读错误；`SignService.applyFaceCredentials` 以本次变换结果的 `sha256` 作为 `face_temp` 缓存键（单飞合并并发上传、6h TTL）：同一次执行内的重试复用同一份变换结果、只上传一次；不同执行重新变换并上传（缓存键随变换结果变化）。`[face]`/`checkFace_` 仍由 S3 状态机映射为一次人脸恢复。回归测试见 `Server/internal/xxt/sign_params_test.go`、`face_check_test.go`、`Server/internal/service/sign_test.go`。`cxcid`/`signToken` 由并行的 XBT-S13 独立实现，不属于 S7a。

#### XBT-S7b 多照片池与官方人脸照片导入（P2，2026-10-20 落地）
- **输入**：账号凭据、账号已保存的人脸照片；目标账号须是操作者本人或有代签权限的账号。
- **输出**：
  1. 每账号最多 5 张照片，本地保存图片与 `objectId`，记录 `useCount`、`isFailureBefore`。
  2. 每次签到随机选一张；上次失败过的账号优先选「未失败且用过」的照片。
  3. 官方照片导入：`collectionfilephotoEnc` 取 `oldObjectId` → 下载 `https://p.cldisk.com/star4/<objectId>/origin.jpg`，**原样存为基线**（`source=official`，上限 8MB 且需可解码），不再在导入时变换。
  4. **签发时强制变换（不可跳过）**：每次执行对选中的基线做一次变换，结果只用于本次上传：
     1. 裁剪：宽高各取 90%–99% 的随机比例，起始偏移在该比例允许的范围内随机；
     2. 旋转：`[-5°, +5°]` 均匀随机角度，双线性/双三次插值；
     3. 去黑边：旋转后按安全矩形中心裁剪，保证无黑边/透明像素；
     4. 重压：JPEG 重编码，质量默认 85（可配 80–90）；若结果超过照片体积上限（100KB），按 85→80→70 逐步降质直到满足；70 仍超限时保持 70 按比例缩小尺寸（每次 80%，最多 8 次）直至满足，全程不报错；
     5. 重编码同时丢弃 EXIF（含方向、设备、时间），不保留原始元数据；
     6. 变换必须接受可注入随机源，便于单测确定性断言。
  5. 照片池只保存基线；变换结果仅在执行作用域内存在，上传缓存键为变换结果的 SHA-256。同一次执行内的重试复用同一份变换与 `objectId`；不同执行重新变换并上传。
  6. 无人像时的签发路径：`facePhotoForSign` 遇到空池，用目标账号凭据调用一次 `ImportOfficial`（请求 ctx + 5s 超时）；成功后当次直接用新基线，失败返回 `face_photo_unavailable`，其它签到类型不受影响。同一执行内不重复拉取。
  7. UI：照片列表、上传、删除、失败标记、重新拉取；代签页可管理目标账号照片。
  8. 实现状态（2026-10-20，OMP-52）：**全部落地**。导入只存基线；`SignService` 每次执行调用 `facephoto.Transform` 后上传；自动导入受 `facePhotoImportTimeout` 约束；详见 §8.3.3。
- **验收标准**：
  - 第 6 张被拒绝；删除后计数下降。
  - 随机选择与失败降级可用固定种子在单测中断言。
  - 官方照片导入成功后可签到；无人像时自动拉取并成功签一次；拉取失败返回 `face_photo_unavailable`，其它签到类型照常成功。
  - 用固定随机种子单测断言：裁剪比例 ∈ [0.90, 0.99]、角度 ∈ [-5°, 5°]、输出字节 ≠ 输入字节、输出无 EXIF、无黑边像素；对 3 组种子各跑一次。
  - 体积上限：接近上限的基线每次变换后输出 ≤100KB 且未返回错误（自动降质生效）。
  - `ImportOfficial` 后 DB 字节 == 上游下载字节；连续两次执行后 `face_photos.data` 的 SHA-256 不变。
  - 同一执行内重试只上传一次。
  - 越权访问他人照片返回 403；日志/响应不含密码与 token。
- **不做**：风格化/换脸；按手机号任意查询/批量抓取；跨账号共享照片。**裁剪/旋转/重压是强制项**，不是可选项。

#### XBT-S8 活动发现补全
- 保留 `type=74`、`status`、`userStatus`、`ext`；过期/未开始活动标记而非静默丢弃。
- 验收：签退活动以 `is_sign_out=true` 出现在列表；`ext` 进入缓存；旧客户端字段不破坏。
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：已落地——`xxt.Active` 解析 `type`（`type`/`atype`，2/74）、`status`、`userStatus`、列表时间、`otherId` 与 `ext`（活动级优先、回退列表级）；筛选不再依赖 `activeType==2`，`type=74` 或名称含“签退”的活动保留，无 `type` 的旧载荷继续走原启发式；`sign_activities` 新增 `type/status/user_status/ext_json`（AutoMigrate，兼容旧行），handler 在元数据成功时写入、失败时从列表同步；详情失败或时间缺失时使用列表/缓存数据继续返回，不再 `continue` 丢弃；响应新增向后兼容字段 `type/status/user_status/is_sign_out/activity_state`（`not_started|ongoing|expired`），排序与 `has_more` 规则不变；`SignService` 在详情缺失/`ext` 为空时回退读取 `ext_json` 供 preSign 使用。测试见 `Server/internal/xxt/client_activity_discovery_test.go`、`Server/internal/handler/sign_activity_view_test.go`、`Server/internal/service/sign_ext_cache_test.go`。

### 3.4 P2：签退与可观测性

#### XBT-S9 签退
- 新增 `/api/sign/sign-out`，依赖主签到成功与 `signOutId`；分享链接是否支持需单独确认。
- 验收：主签到未完成时拒绝；签退成功后写入独立记录，不覆盖签到记录。
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：已落地——`POST /api/sign/sign-out` 接收主签到 `activity_id`，从 `getPPTActiveInfo` 读取 `signOutId`/`signOutPublishTimeStamp`，先校验主签到（本地记录或上游 `preSign` 已签），再对 `signOutId` 活动执行 `preSign` + `stuSignajax`（复用 S3 状态机与 S2 参数构造）；未签到返回 `sign_in_required`，未发布返回 `sign_out_not_published`，无签退活动返回 `sign_out_unavailable`，上游结果沿用 S3 `error_code`。成功写独立表 `sign_out_records`（`user_uid + 主签到 activity_id` 唯一，`sign_out_id` 单独记录），重复签退幂等返回 `already_signed_out=true`、不再请求上游；`sign_records` 不被覆盖。分享链接对签退活动在创建和执行时都明确返回 `400`「分享链接不支持签退活动」。测试见 `Server/internal/service/sign_out_test.go`、`Server/internal/handler/sign_test.go`。

#### XBT-S10 指标与限流
- 结构化日志字段：`activity_id/user_uid/sign_type/result_code/attempt/upstream_status`；指标：成功率、验证码通过率、`validate` 触发率、`errorLocation` 率、上游 403 率、p95 时延。
- 验收：每次 execute 产生一条结果日志；可按结果码聚合；单用户并发超过阈值返回 429。
- **实现状态（2026-10-20，`feature/captcha-protocol-fix`）**：已落地——`service.ExecuteOne` 在 `defer` 中输出恰好一行 `sign_execute <json>`（含 `duration_ms`、`success/already_signed`、`validate_triggered/captcha_passed`、`location_error`、`upstream_403`、`has_cxcid`），`xxt.SignResult` 增加 `Attempts/HTTPStatus/CaptchaAttempted/CaptchaPassed`；进程内指标注册表 `internal/observe` 聚合成功率、验证码通过率、`validate` 触发率、`errorLocation` 率、403 率与最近 2048 样本 p95，经管理员接口 `GET /api/admin/sign-metrics` 暴露，并记录 `im_fetch_requests/skipped_stale/timeout/error_codes/latency` 与包含 IM 抓取的 `/sign/activities` p95；`internal/ratelimit` 按目标账号提供令牌桶 + 并发上限，`/sign/execute`、`/sign/photo`、分享执行超限返回 429 + `Retry-After`，阈值由 `sign_rate_limit_per_minute/sign_rate_limit_burst/sign_max_concurrent` 配置。验证：`cd Server && go test ./...`（含 `internal/observe`、`internal/ratelimit`、`internal/handler` 的新增断言）；不记录 token、密码、照片、`clientId`/`sc` 原文。

### 3.5 执行安排（2026-10-20 决策）

- 范围：**协议全量对齐 CSF**，即 XBT-S1..S10；XBT-S5 已在 `feature/captcha-protocol-fix` 完成。**S7b 多照片做，但排在 S12 之后（P2）**，允许直接抓官方照片；官方原图作为基线落库，**每次执行前在内存中裁剪/旋转/重压一次再上传**（2026-10-20 方案 A）。
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
- 位置选择器在类型 4 展示，并显示“距签到点 N 米 / 超出范围”；类型 0/2/3/5 在 `if_open_address=true` 时由服务端按活动元数据生成位置，无需手动选点。
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

1. **许可证**：CSF 为 AGPLv3。xbt 已于 2026-10-20 决定整库转为 AGPLv3（用户评论 `11eadf8b`），因此可以引入 `captcha.ort`（AGPLv3，未修改）。仍不复制 CSF 的 Kotlin 源码；推理行为按 `ChaoxingCaptchaPredictor.kt` 独立实现。仓库内不得再放入与 AGPLv3 不兼容的第三方代码。
2. **上游风控/接口漂移**：`getPPTActiveInfo`、验证码参数可能随时变化；所有端点与 `captchaId` 配置化，保留降级路径。
3. **deviceCode 隐私**：不使用硬件标识；按账号生成并持久化随机值。
4. **账号风险**：高频签到/重试可能触发风控；服务端限制单账号并发与重试次数。
5. **合规**：仅用于授权范围内的课程签到辅助；不扩展作弊能力。
6. **人脸照片变换后的可用性**：裁剪/旋转/重压会改变人脸特征，可能被上游 `check-face-result` 拒绝或降低通过率。必须用 staging 测试账号验证「变换后仍能通过」；若通过率明显下降，先调低变换强度（裁剪比例上限、角度上限），不得通过增加照片数量来掩盖。
7. **强制变换的可测试性**：变换必须支持注入随机源，否则「每次都变换」无法被单测证明；固定种子的断言是本项唯一可自动化的验收手段。
8. **官方原图落库（OMP-52，方案 A）**：`face_photos.data` 现在保存官方原始照片（明文 `bytea`），泄露面高于此前「只存变换结果」的方案。这是用户 2026-10-20 明确选择的取舍；如需收紧，后续单独评估列加密或外部密钥，不再回退「原图不得落盘」。
9. **上游上传量增加**：不同执行会重新变换并上传，同一活动的多次执行各产生一次 `face_temp` 上传；同一次执行内的重试仍只上传一次。需在 S10 的人脸上传计数中观察，避免高频重试放大风控风险。

## 8. 开放问题（附推荐）

| # | 问题 | 选项 | 推荐 |
|---|---|---|---|
| Q1 | `deviceCode` 生成策略 | A. 每账号稳定随机值；B. 模拟 Android 设备指纹 | A：隐私风险低、实现简单，先验证上游接受度 |
| Q2 | 验证码识别 | A. 保留现有启发式；B. 引入 CSF 的 `captcha.ort`（YOLO11n）；C. 自训/授权非 AGPL 检测器 | **B（2026-10-20 决策，用户评论 `11eadf8b`）**：整库转 AGPLv3，引入 CSF 模型；S16 已落地，模型失败仍回退 A。详见 §8.1 |
| Q3 | 签退是否本期做 | A. 本期；B. 下一期 | B：依赖主签到稳定性，且需产品确认分享场景 |
| Q4 | 多副本 `enc2` 状态 | A. 进程内；B. 共享存储 | A：当前单实例部署；扩容前再评估 |

### 8.1 验证码识别：CSF 模型 vs xbt 启发式（评估）

**大白话版**：xbt 原来靠「贴图比对」找缺口，faker 用训练好的 AI 直接看缺口在哪。2026-10-20 用户决定整库转 AGPLv3，许可障碍消除，S16 直接引入 CSF 模型；模型不可用时仍回退原有启发式。

**结论先行**

1. S16 之前 xbt **没有识别模型**：`detectSlideXCandidates` 是两路手写打分（`cutoutImage` alpha 掩码的灰度差 + 列边缘剖面投票），无参数、无训练集。S16 后模型作为首选，启发式保留为降级。
2. 单看识别能力，**CSF 的方案更强**：YOLO11n 单类检测器直接回归缺口框，只吃 `shadeImage`，不依赖 `cutoutImage` 的 alpha 与两图对齐；输出唯一 x 且带置信度，失败时回落人工滑动。
3. **模型现已可进 xbt**：`captcha.ort` 元数据自述 `Ultralytics YOLO11n model trained on config/block.yaml` 与 `AGPL-3.0 License (https://ultralytics.com/license)`；xbt 于 2026-10-20 整库转 AGPLv3，因此可将其作为未修改资产分发。代价是服务端引入 cgo + ONNX Runtime 共享库，部署变重（见 DEPLOY.md §3.1）。
4. **当前 xbt 验证码失败的主因在协议层，不在视觉层**：空 cookie jar、无 `get/conf`、校验缺 `iv`/`t=a`、同一 token 最多提交 40 个候选 x。这些不修，换任何模型都过不了。

**能力对比**

| 维度 | xbt S16（模型优先，启发式降级） | CSF（`captcha.ort`） |
|---|---|---|
| 形态 | `captcha.ort` + ONNX Runtime（cgo）；失败回退手写打分 | 5.7 MB YOLO11n 单类检测器，ONNX Runtime |
| 输入 | 模型仅 `shadeImage`；启发式用 `cutoutImage` alpha + `shadeImage` 灰度 | 仅 `shadeImage`，补边至正方形后缩放到 640×640 |
| 输出 | 模型单框；score<0.25 丢弃，NMS IoU 0.45；降级时启发式候选 | 单框 `(x,y,w,h,score)`，score<0.25 丢弃，NMS IoU 0.45 |
| 单 challenge 上游校验次数 | 1，失败重取 challenge | 1，失败重取 challenge |
| 算力/依赖 | native 库 + cgo；模型失败时纯 Go 标准库 | native 库 + 每次约 2.6 GFLOPs（Ultralytics 公布值，未实测）；ARM 端数十至百毫秒为估算 |
| 失败处理 | 模型缺失/推理失败 → 启发式 → 失败重取 challenge | 预测失败自动回落人工滑动 |
| 可解释/可调 | 模型黑盒；降级路径可打印分数与候选 | 黑盒，需重训 |
| 许可 | AGPLv3（自有） | 权重自述 AGPL-3.0 |
| 准确率证据 | 3 张固定图标注；线上通过率未实测 | 无公开指标，仅作者自用 |

**准确率仍无法给出数字**：S16 只验证了 3 张固定图与上游一次性接受，没有 ≥200 challenge 的通过率样本；模型/启发式的线上通过率仍是已接受的未知。后续可用 S5 埋点采集（仅存图片 digest 与上游 `result`，不落原图或账号信息）比较 top-1 通过率、单 challenge 通过率、平均校验次数。

**决策门槛（S16 之前的历史评估，已被最终决策取代）**

- 修完 S5 后单 challenge 通过率 ≥85%：维持启发式，不引入模型。
- 70%–85%：先做零许可风险的增强（尺寸/DPR 对齐、掩码采样步长、边缘剖面权重、候选收敛到 top-1）。
- <70%：进入选项 C——自行标注训练**非 AGPL** 检测器（Apache-2.0 实现）或采购授权；以 sidecar（Python + ONNX Runtime）部署，Go 主进程只传图取 x。

**最终决策（2026-10-20，用户评论 `11eadf8b`）**：选 B。整库转 AGPLv3，直接引入 `captcha.ort`，不再等待启发式实测门槛。通过率仍无实测数据，按已接受的未知处理。

**对 XBT-S5 的影响**：候选轮询改为「本地排序取 top-1 提交，失败重取 challenge」，并新增埋点字段（`captcha_challenge_id`、`candidate_count`、`top1_x`、`upstream_result`、`attempts`）。

**XBT-S16 落地（2026-10-20）**

- **许可证**：`LICENSE` 全文换为 AGPLv3；README 顶部与 `NOTICE` 声明 AGPLv3、§13 源码地址与模型来源（未修改，SHA-256 `ce9623e1…`）。
- **推理**：新增 `Server/internal/captcha`，`PredictSlideX([]byte)` 解码 → 补边至正方形 → 640×640 → NCHW `/255` → `captcha.ort`（输入 `images`，输出 `output0`）→ score 0.25 阈值 → NMS IoU 0.45 → 最高分框 `x * paddedLength / 640 - 4 + 0.5`。常量与 CSF `ChaoxingCaptchaPredictor.kt:22-60` 对齐。
- **接入**：`xxt.Client.SetCaptchaPredictor`；`detectSlideXCandidates` 模型成功时只返回模型 x（`captcha_model=model`），失败/未配置时回退启发式并记录 `captcha_model=fallback`，不阻断签到。
- **配置**：`captcha_model_path`（默认 `./assets/captcha.ort`）、`captcha_model_enabled`（默认 true）。模型资产位于 `Server/assets/captcha.ort`。
- **部署**：cgo + `libonnxruntime` 1.29.0；Dockerfile 构建阶段下载对应架构共享库，运行时设置 `CAPTCHA_ORT_LIB_PATH`。详见 DEPLOY.md §3.1。
- **测试**：`Server/internal/captcha/testdata` 3 张匿名验证码背景 + 上游已接受的 x 标注，误差 ≤2px；模型缺失返回明确错误；`go test ./internal/captcha ./internal/xxt` 通过。

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
4. 多照片池和随机化能降低「同一张照片反复使用」的特征。**产品决策（2026-10-20，含当日补充）：做多照片，优先级 P2、排在 S12 之后；允许直接抓取官方人脸照片；对抓取到的官方照片必须做裁剪 + 旋转 + 重压，原图不得保存或上传**。仍不做风格化/换脸，也不做跨账号批量枚举。

**验证方式**

```bash
grep -n "faceEncAid\|faceCode\|ifCFP" /tmp/cs/app/src/main/java/.../signer/ChaoxingSigner.kt
grep -n "MAX_FACE_IMAGES\|randomOrNull\|randomizeStylizeImage" /tmp/cs/app/src/main/java/.../api/ChaoxingFaceHelper.kt /tmp/cs/.../screen/QRCodeSignScreen.kt
grep -n "current_face_id\|faceEnc\|face_code\|UploadFaceTemp" Server/internal/xxt/client.go Server/internal/service/sign.go
```
结果：CSF `MAX_FACE_IMAGES=5`、随机选择与照片随机化均存在；xbt 无 `faceEncAid`、无 `[face]` 分支、每次 execute 重新上传。人脸相关端到端行为需要真实人脸活动验证，当前环境没有测试账号。

### 8.3 做完 S1–S12 + S7b 之后，与 CSF 仍存在的差异（2026-10-20）

结论：**协议层（参数、响应状态机、验证码协议、位置、人脸、活动发现、签退、指标、二维码过期、多照片池与官方照片变换）在计划完成后基本对齐**；剩下的差异集中在「识别模型」「设备身份」「签到来源」「出网位置」四类。产品决策（2026-10-20，用户评论 `ddedfc4d`）：**设备签名做（XBT-S13）、群聊签到来源做（XBT-S14）、客户端身份伪装不做、出口 IP 与照片存放不改**。

| # | 剩余差异 | xbt 计划后状态 | CSF | 影响 | 处置 |
|---|---|---|---|---|---|
| 1 | 滑块验证码识别内核 | **已完成（XBT-S16）**：`Server/internal/captcha` 使用 `captcha.ort` + ONNX Runtime，模型失败回退启发式；通过率无实测 | YOLO11n + ONNX（AGPLv3） | 与 CSF 同一模型、同一输入/阈值/后处理；线上等价性未实测 | 见 §8.1；关注 `captcha_model=fallback` 比例与验证码通过率 |
| 2 | 人脸 `faceResult` 设备签名 | 已实现（XBT-S13）：登录时保存加密 `clientId`，`faceResult` 在可解密时追加 `cxcid`/`signToken`；缺失或解密失败仍不带签名 | 登录保存并解密 `clientId` 后签名 | 若上游对人脸请求加签名校验，xbt 人脸签到会整类失败 | **已完成**：见 §8.3.1 |
| 3 | 签到来源：群聊实时消息 | 已实现（XBT-S14）：`/sign/activities` 刷新时按需抓 Easemob 群列表 + `messageroaming`，解析 `attachmentType=15`、`atype∈{2,74}`，按 `activeId` 与 `taskactivelist` 合并 | Easemob IM 群聊监听 + 群列表 | 页面不刷新就发现不了群消息；无「从群聊进入签到」入口 | **已完成**：见 §8.3.2（B 方案按需抓取，默认最小间隔 30s，不建长连接；staging 待验） |
| 4 | 客户端身份伪装 | `chaoxing_user_agent` 为服务端全局配置，`appType=15` 固定，无 per-account/UI | 内置 `DEFAULT` / `XUEZAIXIDIAN` 及自定义 UA + 包名 | 校园定制版学校的课程/活动列表可能不匹配 | **不做**（2026-10-20 决策）；仅在文档标注为已知限制 |
| 5 | 请求出口 IP | 签到由 `Server` 发起，`clientip` 固定空串，真实出口是服务器 IP | 由手机发起，出口是设备网络 | IP 与位置/设备画像可能不一致，风控特征不同；同一服务器多账号共用出口 | **接受现状**（2026-10-20 决策）；靠 S10 限流 + S11 串行降特征 |
| 6 | 人脸照片存放位置 | 照片存服务器（新照片表），导入的官方照片经变换后落服务端 | 存设备本地文件 | 功能等价，数据责任与泄露面不同 | **接受现状**；权限边界与日志脱敏见 OMP-39 |

**xbt 有而 CSF 没有的能力**（不是差距，别为了「对齐」砍掉）：Web 多用户 + 管理员白名单、免登录签到分享链接、服务端并发代签与状态跟踪、`Server` 侧统一指标。

**判断**：S13 落地后「设备签名」不再是差距；主线只剩 1（验证码通过率）待实测。S14 解决「找不找得到签到」的能力缺口，但不进入当前串行链。出口 IP 与照片存放属于架构选择，已决定不改。

#### 8.3.1 XBT-S13 人脸设备签名（2026-10-20 落地）

- **设备资料**：`xxt.GenerateDeviceInfo` 按 `uid + mobile` 派生合成设备 JSON（`deviceUniqueId/cdid/device_id` 同值，另含 `android_id/mediaDrmId/oaid/platform/os_name/os_ver/os_lang/brand/board/hardware/model/cpu_ar/app_name/app_ver`），不读取宿主硬件标识。首次登录生成后以 `CredentialCrypto` 加密存入 `users.device_info_cipher`，后续登录复用。
- **clientId**：登录成功后用该设备资料调用 `POST https://sso.chaoxing.com/apis/login/userLogin4Uname.do`（form `data` 为 RSA/PKCS#1 v1.5 加密 JSON），只读取 `msg.clientId`，加密存入 `users.client_id_cipher`。请求失败、响应缺字段或加密失败只记 `uid` 与错误，不阻断登录/签到。
- **解密与签名**：`xxt.DecryptClientID` 按 RSA 模长分块做 `modPow(publicExponent)`、去 PKCS#1 v1.5 type-1 填充、解析 JSON 取 `cid/sc`；失败返回空。`faceResult` 可解出时追加 `cxcid` 与 `signToken = MD5(sorted key+value + sc)`；否则保持原四项，`check-face-result` 照常发起。
- **迁移**：`AutoMigrate(&model.User{})` 新增两个可空 `text` 列，旧数据为空即走降级路径。
- **验证**：`Server/internal/xxt/device_test.go` 覆盖固定 type-1 fixture、非法样本、`signToken` 期望值/字段顺序、缺失降级、设备资料稳定性与 per-account 差异、userInfo 错误不回显响应体；`Server/internal/service/sign_test.go` 覆盖两次签到传入同一持久化 clientId；`Server/internal/handler/auth_test.go` 覆盖登录持久化、两次登录复用设备资料、clientId 拉取失败仍登录成功。命令：`cd Server && go test ./...`。
- **风险**：上游 clientId 是弱混淆产物，不是身份凭据；staging 若拒绝人脸请求，先只带 `cxcid` 不带 `signToken` 二分。

#### 8.3.2 XBT-S14 群聊签到来源（2026-10-20 落地，B 方案）

- **协议**：`internal/im` 实现 token（`POST /token`，`grant_type=password`，`username=<uid>`，`password=DES/ECB/PKCS5Padding` 解密后的 `imAccount.password`）、`joined_chatgroups`、`messageroaming`。`messageroaming` 的 `msg` 为 base64 protobuf，只解析 `Meta.field4`（时间）、`Meta.field6`（消息体）与 `MessageBody.ext` 中的 `attachment`；不引入 protobuf 依赖。
- **识别**：`attachmentType == 15`、存在 `att_chat_course`、`aid > 0`、`atype ∈ {2,74}`；提取 `title/subTitle/courseInfo.courseid/classid/coursename`。文本、图片、作业等消息直接跳过。
- **触发（B）**：`GET /sign/activities` 处理时，若该 uid 距上次抓取 ≥ `im_min_fetch_interval_seconds`（默认 30，0 禁用）则**同步**抓一次，硬超时 `im_fetch_timeout_seconds`（默认 5s）；否则直接用快照。没有 `ticker`/`Start`/`pollAll`，进程启动不发 IM 请求，前端 0 改动。
- **合并**：`/api/sign/activities` 在 `taskactivelist` 分组完成后合并 IM 结果；同一 `active_id` 只保留一条，`sources` 记录 `taskactivelist` 与 `im_group`，`source` 优先 `im_group`。IM-only 活动写入 `sign_activity_scopes`，保证现有 `/sign/execute` 授权与跳转可用；不自动提交。
- **凭据**：复用学习通会话调用 `userInfo` 读取 `msg.accountInfo.imAccount.password`（S13 只持久化 `clientId`，未保存 IM 密码；本实现按需读取，不新增数据库列），DES 解密失败返回 `im_auth_failed`。不落原始消息。
- **并发与失败**：per-uid `TryLock`；已有抓取在跑时本次直接用旧快照，不排队。`401/408` 重取 token 并重试一次；第二次失败返回 `im_unauthorized`；超时返回 `im_timeout`。失败只写 `error_code`，保留上一轮成功结果，不影响普通活动列表。
- **观测**：`GET /api/sign/im/status` 返回 `enabled/interval_seconds/fetch_timeout_seconds/last_poll_at/last_success_at/group_count/activity_count/error_code`；日志只含 `uid/active_id/source/result/duration_ms`，不含密码、token、消息体。
- **验证**：`Server/internal/im/*_test.go` 覆盖 DES fixture、附件解析/丢弃规则、token 请求、401/408 重取、响应体不泄漏；`Server/internal/service/im_fetch_test.go` 覆盖按需抓取间隔门控、单飞、超时降级、重取一次、连续失败、解密失败、日志脱敏、失败保留上一轮；`Server/internal/handler/im_merge_test.go` 覆盖双来源去重、IM-only 合并、scope/cache 落库、IM 失败不影响列表。命令：`cd Server && go test ./...`。staging 发现流程尚未执行（当前环境无课程群测试账号），上游端点/内容类型需用真实账号确认。

#### 8.3.3 XBT-S7b 照片池与官方照片导入（2026-10-20 落地）

- **数据模型**：新增 `face_photos`（`user_uid/data/object_id/use_count/is_failure_before/source/created_at`），`user_uid` 建索引。迁移保留旧 `users.face_photo` 列，并在首次启动时幂等回填为池中一条 `source=upload` 记录；此后代码只读写池。
- **上限与并发**：`FacePhotoService.Add` 在事务内对目标 `users` 行 `SELECT ... FOR UPDATE` 后计数，第 6 张返回 `ErrFacePhotoLimit`（HTTP 409）；删除后计数立即下降。
- **选图**：`facephoto.Select` 接受可注入随机源。账号存在失败历史时优先「未失败且用过」，其次「未失败」，最后全体；同一次 execute 内复用已选照片，重试不换图、不重复计数。
- **失败标记**：`check-face-result` 无 `enc` 包装为 `xxt.ErrFaceRejected`，只在该错误或提交阶段再次返回 `[face]` 时置 `is_failure_before=true`；传输/HTTP/解码错误不会误伤照片。上传+校验成功即 `use_count+1`，并持久化最新 `objectId`。
- **官方导入（OMP-52 方案 A）**：`collectionfilephotoEnc`（账号自身会话）取 `oldObjectId` → `https://p.cldisk.com/star4/<objectId>/origin.jpg` → **原样写入池**（`source=official`，≤8MB 且可解码，最大 1600 万像素）。响应返回 `transform_deferred=true`，不再返回 `transform`；未设置官方照片/无法解码/超限均返回 HTTP 422 可读提示。原始字节不写日志。
- **签发时变换**：`SignService.applyFaceCredentials` 每次执行取到基线后调用 `facephoto.Transform`，上传字节只放内存；同一次 `ExecuteSignRequest` 内缓存变换结果，`face_temp` 缓存键为变换结果的 SHA-256，因此同一执行内重试只上传一次，不同执行会重新变换并上传。
- **无人像自动导入**：`facePhotoForSign` 遇到 `facephoto.ErrNoPhotos` 时调用 `FacePhotoService.ImportOfficial`（目标账号凭据，请求 ctx + 5s 超时，单次执行最多一次）；成功后当次即用，失败返回 `face_photo_unavailable` 且不阻断非人脸签到。日志字段 `face_photo_source=pool|official_auto`。
- **权限**：本人始终可管理；管理他人时要求双方存在「同为已选」的课程（与 `AuthorizeTargets` 的代签集合一致），否则 403。不提供按手机号查询或批量枚举。
- **API**：`GET/POST /api/user/faces`、`DELETE /api/user/faces/:id`、`POST /api/user/faces/import`，均支持 `target_uid`；旧 `GET/POST/DELETE /api/user/face` 保留为兼容入口（GET 返回最新一张，DELETE 清空池）。
- **UI**：`FacePhotoManager` 复用于主页与签到详情；支持列表、拍照/相册上传、逐张删除、失败/官方标记、使用次数与「拉取官方照片」。签到错误新增 `face_photo_unavailable` 映射。
- **验证**：`cd Server && go vet ./... && go test ./...`（含照片池上限/权限/导入原样落库/签发时变换/自动导入/同执行单次上传/基线 SHA-256 不变）；`cd Web && npm run build`。

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
