# 把 xbt 登录账号导入 ChaoxingSignFaker（faker）：可行性与方案讨论

- 状态：**讨论稿，未决策，未写实现代码**
- 日期：2026-10-20
- 角色：产品经理
- 对比基线：xbt `d41bd05`；ChaoxingSignFaker（CSF）`9ae5398`（2026-10-20）

## 结论先行

**可行，而且不需要改 faker。** faker 已经内置“代签用户”导入通道：扫码、粘贴链接、手动账密。xbt 只要按 faker 的链接格式生成一条导入链接/二维码即可，faker 收到后会自己登录学习通并把账号保存为“代签用户”。

推荐方向（待你确认，不是决策）：

1. **优先做 faker 兼容的导入链接/二维码**（方案 A），首版只允许“本人导出自己的账号”，单账号、短时展示、不落库、不写日志。
2. **不推荐**默认导出明文 CSV；**不做**直接改写 faker 的 `datastore.pb`；**不先做**批量导出全部账号。
3. 有一个硬限制要先说清：**faker 必须有一个主账号登录**，导入的账号都会成为“代签用户”，不能替代 faker 的主登录。因此“完全不用在 faker 登录一次”做不到。

需要你拍板的三件事见第 6 节；在你选择前不进入实现。

## 1. 问题、目标与非目标

### 问题
用户在 xbt 已经登录过一批学习通账号。换到 faker 代签时，还要逐个小号手动输入手机号和密码，成本高、易错。

### 目标
让用户从 xbt 拿到一条可被 faker 直接识别的账号导入凭证，减少重复登录。

### 成功指标（若后续实现）
- 单账号从 xbt 发起到 faker 出现账号 ≤ 30 秒（不含上游登录耗时）。
- 导入后能在 faker 直接对目标账号执行代签。
- 导出过程不把密码写入数据库、日志、浏览器持久存储。
- 非授权用户无法导出他人账号（403）。

### 非目标
- 账号双向同步、课程/签到记录迁移、自动代签策略迁移。
- 在 faker 内自动登录 xbt 主账号。
- 把 xbt 变成密码托管服务。

## 2. 现状事实（已核对代码）

### 2.1 xbt 侧

- 学习通密码以 `CredentialCipher` 加密存 PostgreSQL，服务端可用 `CredentialCrypto.Decrypt` 还原明文（`Server/internal/model/models.go`、`Server/internal/service/crypto.go`）。因此导出只能在服务端做。
- Web 本地多账号只保存 `uid / name / 脱敏手机号 / JWT`，不保存密码（`Web/src/store/auth.ts`、`Web/src/types/index.ts`）。**纯前端导出密码不可行。**
- 管理端已有账号列表与“添加账号”接口，但列表手机号是脱敏的，且**当前没有任何导出接口**（`Server/internal/handler/admin_account.go:437-457`、`Server/API.md:690-730`）。
- 每个账号有稳定的伪设备号 `deviceCode = HMAC-SHA256(chaoxing_aes_key, "xbt-device-code:v1:<uid>")`，64 位小写 hex（`Server/internal/xxt/sign_params.go:174-194`）。
- 人脸照片以图片字节存 `users.face_photo`；签到上传得到的云盘 `objectId` 只做 6 小时内存缓存，**没有持久化**（`Server/internal/service/sign.go:520-560`）。

### 2.2 faker / CSF 侧

- 导入入口有三个：
  - “其他用户”页扫码（`OtherUserScreen.kt:3274-3330`）；
  - 粘贴文本链接（`OtherUserScreen.kt:2090-2180`）；
  - 手动手机号 + 密码添加（`OtherUserScreen.kt:560-700`）。
- 链接格式（`ChaoxingOtherUserHelper.kt:103-107`）：
  `http://cdn.aquamarine5.fun/?phone=<手机号>&pwd=<URL 编码的密文>&name=<昵称>&face=<objectId 逗号列表>&dc=<deviceCode>`
- `pwd` 不是明文：AES/CBC/PKCS5Padding，key 与 IV 都是固定字符串 `u2oh6Vu^HWe4_AES`，结果 Base64（`ChaoxingHttpClient.kt:666-677`）。这是**可逆的固定密钥编码**，拿到链接等同拿到密码。
- 导入时 faker 用 `phone + encryptedPassword` 真正登录学习通，成功后把 cookies 保存为该账号的会话（`ChaoxingOtherUserHelper.kt:270-350`）。已存在账号会更新密码或人脸。
- `face` 参数是学习通云盘的人脸照片 `objectId` 列表，不是图片本身；faker 会按 objectId 下载（`ChaoxingFaceHelper.kt:147-175`）。
- `dc` 是 faker 本机设备号，原样写入该账号会话，后续签到参数使用它（`ChaoxingDeviceInfoHelper.kt:89-105`）；格式没有强校验。
- faker 自己的分享页明确警告：“他人可以通过此二维码来控制你的账号”（`OtherUserScreen.kt:2540`）。这与“链接=凭据”的判断一致。

### 2.3 格式可行性验证

- 已用 `openssl enc -aes-128-cbc -K <key hex> -iv <key hex> -base64` 复现 CSF 的 `pwd` 算法（key=IV=`u2oh6Vu^HWe4_AES`，PKCS7 填充）。
- **未做端到端验证**：当前没有可用的测试学习通账号、安卓设备或 faker 安装实例，无法实测“生成链接 → faker 导入 → 上游登录成功”。这一步应作为实现前的 30 分钟手工验证。

## 3. 方案对比

| 方案 | 做法 | 直接性 | 改动范围 | 主要风险 | 判断 |
|---|---|---|---|---|---|
| **A. faker 兼容链接/二维码** | xbt 服务端解密密码 → 按 faker 格式 AES 加密 → 返回链接，Web 展示二维码 | 高：faker 扫码/粘贴即可 | xbt 新增 1 个接口 + 1 个 UI；faker 零改动 | 链接等同密码；截屏/转发即泄露 | **推荐** |
| B. 明文 CSV/JSON | 导出手机号、密码、昵称 | 低：faker 没有文件导入，只能逐条手输 | 最小 | 明文落盘，风险最高；体验没有变好 | 不推荐（除非明确要离线备份） |
| C. 直接写 faker `datastore.pb` | 生成 Protobuf 写入 faker 私有目录 | 表面最高 | 需 root/备份恢复；强绑定 faker 版本与字段 | 升级即坏；越权写入他人应用数据；不可维护 | 不做 |
| D. 给 faker 提 PR 增加文件导入 | 上游新增 CSV/JSON 导入 | 高（批量） | 跨仓库、AGPL、上游接受度不可控 | 排期不可控 | 仅作为长期备选 |

### 为什么推荐 A
- 复用 faker 已有且经过验证的导入链路，xbt 只做“凭证格式转换”，不碰 faker 内部数据。
- 体验最接近“直接导出”：一条链接/一张二维码，扫码后 faker 自动登录并保存。
- 失败路径清晰：手机号或密码错误时由 faker 报登录失败，xbt 不需要猜测导入结果。

### 方案 A 的硬限制
1. faker 仍需一个主账号登录；导入的账号全是“代签用户”。
2. 链接没有过期、撤销、一次性语义。faker 只在导入时解析，xbt 无法在事后让链接失效。
3. 人脸照片不能直接复用：xbt 只有图片字节，没有稳定 `objectId`。首版建议不带 `face`，让 faker 用自己的方式获取官方人脸照片；如必须带，需要额外上传换取 objectId，工作量和风控都上升。
4. faker 的链接格式没有版本承诺；需要固定“已验证版本”，格式变化时明确报错，而不是静默生成坏链接。

## 4. 若后续批准，建议的最小切片

### Phase 1（建议先做）
- 服务端新增“生成 faker 导入链接”能力：仅本人（JWT 对应 uid）可导出自己的账号；管理员是否可导出他人，单独决策。
- 返回字段：`url`、`expires_display_seconds`、`warnings`；不返回明文密码。
- Web 在“账号管理”当前账号上增加“导入到 faker”入口：二次确认 → 展示二维码 + 可复制链接 → 30 秒后自动隐藏。
- 不持久化链接；访问日志、审计日志、错误信息均不记录 `pwd`。
- 固定 faker 已验证版本范围，生成前做格式自检。

### Phase 2（视反馈）
- 管理员批量导出：每个账号一张二维码/一条链接，支持逐条复制，不做“全选一键生成”。
- 可选携带 `dc`（见风险 5.3）。
- 可选携带人脸 `objectId`（需先解决 objectId 获取与用户授权）。

### 明确不做
- 不生成明文密码文件。
- 不写入 faker 的私有数据目录。
- 不记录导入结果（xbt 无法可靠观测 faker 侧结果）。

## 5. 风险

### 5.1 链接就是密码（最高风险）
固定密钥 AES 可逆，任何拿到链接/二维码截图的人都能还原密码并控制账号。faker 自己也这样提示。
缓解：仅本人导出、二次确认、屏幕短时展示、禁止复制到日志、禁止服务端持久化、导出行为审计（只记 uid/时间/IP，不记密文）。

### 5.2 日志与前端泄露
二维码/链接可能进入浏览器历史、截图、代理日志、错误上报。
缓解：URL 不放 referrer；页面禁用第三方统计；服务端响应加 `Cache-Control: no-store`；错误信息脱敏。

### 5.3 设备指纹冲突
xbt 用合成设备资料和稳定 `deviceCode`，并持久化 `clientId` 用于人脸签名（`models.go` 的 `ClientIDCipher`）。faker 用本机 OAID 生成设备号并重新登录，可能让上游轮换 `clientId`，导致 xbt 后续人脸签到签名失效。
缓解：导入前告知“可能需要在 xbt 重新登录/重新获取设备信息”；可选把 xbt 的 `dc` 传给 faker，让签到设备号保持一致，但需实测 faker 是否原样使用、上游是否接受。

### 5.4 上游风控与验证码
短时间在新设备、新 IP 登录多个账号，可能触发验证码、设备验证或临时封禁。faker 导入失败时只能提示上游错误。
缓解：一次只导入一个账号；失败不自动重试；在文案中提示“请在常用网络下操作”。

### 5.5 版本与兼容
faker 可能修改链接参数或加密方式。xbt 生成的链接会在无提示的情况下失效。
缓解：记录“已验证 faker 版本”，生成时校验参数完整性；在 UI 标注适用版本；格式变更时给出明确升级提示。

### 5.6 合规与授权
- faker 是第三方 AGPLv3 应用；xbt 只做 URL 格式互操作，不复制其代码、不打包、不修改。
- 学习通账号密码交给第三方应用，涉及平台条款和账号安全；必须由账号本人明确同意，并提示“链接可被他人用于控制账号”。
- 管理员批量导出他人账号，必须有账号本人授权，否则不应提供。

## 6. 需要你确认的问题（附推荐）

1. **导出范围**：只允许本人导出自己，还是管理员可批量导出白名单账号？
   推荐：首版仅本人；批量另开需求并加授权门槛。
2. **首版形态**：faker 链接/二维码（A）、明文 CSV（B）、还是先不做？
   推荐：A。B 只作为离线备份的独立需求。
3. **是否带人脸照片**：不带、只带官方照片、还是带 xbt 已上传照片？
   推荐：首版不带。xbt 没有持久 `objectId`，带图需要额外上传与授权，收益/成本不划算。
4. **是否传 `dc`（设备号）**：传 xbt 的稳定设备号，还是让 faker 用本机设备号？
   推荐：先不传，Phase 2 实测后再决定；避免设备指纹冲突扩大。
5. **入口位置**：账号管理页（本人）还是管理后台（批量）？
   推荐：本人入口放“账号管理”，批量入口后置到管理后台。

## 7. 讨论阶段的验证方式

- 代码核验（已完成）：
  - `grep -n "getSharedUrl\|parseFromQRCode" /tmp/cs/app/src/main/java/org/aquamarine5/brainspark/chaoxingsignfaker/api/ChaoxingOtherUserHelper.kt`
  - `grep -n "encryptByAES\|TRANSFER_KEY" /tmp/cs/app/src/main/java/org/aquamarine5/brainspark/chaoxingsignfaker/api/ChaoxingHttpClient.kt`
  - `grep -rn "CredentialCipher\|faceObjectID" /root/xbt/Server/internal`
- 待做的手工验证（实现前）：
  1. 用测试账号生成一条链接；
  2. 在 faker `9ae5398` 的“链接导入”中粘贴；
  3. 确认账号出现在“代签用户”列表，并能完成一次不敏感签到；
  4. 检查 faker 侧保存的 `deviceCode` 与 cookies 是否符合预期。

## 8. 本文档的边界

本文只做可行性与方案讨论，**不含实现代码，不修改 faker，不做产品决策**。你确认方案和范围后，再拆实现 issue、补验收标准与测试计划。
