# 把 xbt 登录账号导入 ChaoxingSignFaker（CSF）

- 状态：**已实现（服务端 OMP-62 / Web OMP-63，2026-10-21）**
- 最终决策：**批量导出（管理员，全部账号或 `?uids=` 指定）+ 二维码常显**；不做遮挡、不做定时隐藏
- 对比基线：xbt `d41bd05`；ChaoxingSignFaker（CSF）`9ae5398`（2026-10-20）
- 适用版本：CSF 链接格式未承诺版本兼容，升级 CSF 后需重新核对

## 1. 功能是什么

faker 内置“代签用户”导入通道。xbt 在服务端解密学习通密码，按 CSF 的链接格式生成导入链接，并在 Web 展示二维码或提供复制链接。用户在 CSF 扫码/粘贴后，CSF 自己登录学习通并保存为“代签用户”。

能力边界：

- **不做** faker 修改、不写 faker 私有数据、不生成明文密码文件。
- 导入的账号全部是 CSF 的“代签用户”，**CSF 仍需一个主账号登录**；导入不能替代主登录。
- CSF 每次只解析一条链接，N 个账号仍需 N 次扫码或粘贴；xbt 只能省掉手输手机号和密码。
- CSF 不回报导入结果，xbt 无法知道哪条成功、哪条失败。

## 2. 入口与使用流程

| 角色 | 入口 | 范围 |
|---|---|---|
| 普通用户 | 账号管理 → “导出到 CSF” | 仅当前 JWT 对应账号 |
| 管理员 | 管理后台 → 账号课程 → “批量导出” | 默认**全部已保存账号**；接口支持 `?uids=1,2` |

### 2.1 本人导出

1. 打开“账号管理”，点击当前账号卡片上的“导出到 CSF”。
2. 页面直接弹出导出面板，显示账号昵称、UID、二维码、完整链接和“复制链接”按钮。
3. 二维码**常显**，关闭面板即消失；不写入本地持久存储，刷新后需重新生成。

### 2.2 管理员批量导出

1. 管理后台 → 账号课程 → “批量导出”，进入 `/admin/accounts/faker-export`。
2. 页面加载即调用 `GET /api/admin/accounts/faker-export`，默认导出全部账号。
3. 所有账号以**同屏卡片**展示：每张卡片一张二维码 + 完整链接 + “复制链接”；页面顶部提供“下载 ZIP”。
4. 单条账号失败（凭据无法解密等）只在该卡片显示失败原因，不影响其他卡片。
5. 页面不缓存数据；刷新会重新请求并重新生成二维码。

## 3. 链接协议

```
http://cdn.aquamarine5.fun/?phone=<URL 编码手机号>&pwd=<URL 编码 Base64>&name=<URL 编码昵称>&face=&dc=<deviceCode>
pwd = Base64(AES-128-CBC/PKCS5(明文密码, key=IV=u2oh6Vu^HWe4_AES))
dc  = HMAC-SHA256(chaoxing_aes_key, "xbt-device-code:v1:<uid>") 的 64 位小写 hex
```

- 参数顺序固定为 `phone/pwd/name/face/dc`，所有值做 URL 编码。
- `face` 恒为空：xbt 只有人脸图片字节，没有持久化的云盘 `objectId`。
- `dc` 与签到参数中的设备号同源，可让 CSF 导入后使用 xbt 的稳定伪设备号。
- `pwd` 不是哈希，固定密钥可逆；**链接等同于账号密码**。

## 4. 接口

### 4.1 本人导出

`GET /api/account/faker-export`，需要登录，只导出 JWT 对应账号。

```json
{
  "url": "http://cdn.aquamarine5.fun/?phone=13800000000&pwd=...&name=...&face=&dc=...",
  "qr_png": "data:image/png;base64,...",
  "warnings": ["链接等同于账号密码，拿到即可还原密码并控制账号，请勿转发或截图外传"]
}
```

- `qr_png` 为 512×512、纠错级别 M 的 PNG data URL，内容与 `url` 完全一致。
- 凭据无法解密时返回 `400 credential expired, please add account again`。
- 响应头 `Cache-Control: no-store`。

### 4.2 管理员批量导出

`GET /api/admin/accounts/faker-export`，需要管理员。

- 查询参数 `uids=1,2`：可选，逗号分隔；缺省导出全部已保存账号；重复 uid 去重；空项、非法值或非正数返回 `400`。
- 响应逐条返回 `uid/name/phone/url/qr_png`；失败项只有 `uid` 与 `error`。
- 非管理员调用返回 `403`，响应体不含任何账号字段。
- 响应头 `Cache-Control: no-store`。

```json
{
  "items": [
    {
      "uid": 343479151,
      "name": "张三",
      "phone": "13800000000",
      "url": "http://cdn.aquamarine5.fun/?phone=...&pwd=...&name=...&face=&dc=...",
      "qr_png": "data:image/png;base64,..."
    }
  ],
  "warnings": ["链接等同于账号密码，拿到即可还原密码并控制账号，请勿转发或截图外传"]
}
```

### 4.3 批量 ZIP

`GET /api/admin/accounts/faker-export.zip`，需要管理员，查询参数同上。

- 响应为 `application/zip` 附件，服务端文件名为 `faker-export.zip`；Web 下载时另存为 `csf-export-<YYYY-MM-DD>.zip`。
- 内含每个成功账号的 `qr-<uid>.png`（512×512）和 `links.txt`。
- `links.txt` 每行格式：`uid<TAB>phone<TAB>name<TAB>url`，行数与压缩包内成功导出的账号数一致。
- 凭据无法解密的账号被跳过，只在服务端日志记录 `uid` 与错误，不进入压缩包。
- 响应头 `Cache-Control: no-store`。

## 5. 安全与风险

### 5.1 链接就是密码（最高风险）

`pwd` 是固定密钥 AES 可逆密文，任何拿到链接或二维码截图的人都能还原密码并控制账号。CSF 自己也提示“他人可以通过此二维码来控制你的账号”。

已做缓解：链接/密文不落库、不写缓存；服务端日志不记录 `url`/`pwd`/密码，只记操作者 `uid`、目标数和 IP；三个接口均返回 `Cache-Control: no-store`。**未做**：链接有效期、撤销、一次性语义。

### 5.2 截图与同屏暴露

批量页把 N 条凭据同时渲染在同一屏，一次截屏的爆炸半径是 N，而不是 1。页面已标注“该二维码/链接等同账号密码，请勿截图外传”，但这只是提示，不构成防护。

### 5.3 日志与前端泄露

二维码/链接可能进入浏览器历史、截图、代理日志、错误上报。

已做缓解：响应 `no-store`；页面不使用 `localStorage`/`sessionStorage`/IndexedDB 保存导出数据；错误信息不回显 `pwd`。复制链接依赖 `navigator.clipboard`，非安全上下文回退到临时 textarea，复制后立即移除。

### 5.4 设备指纹冲突

xbt 用合成设备资料和稳定 `deviceCode`，并持久化 `clientId` 用于人脸签名。CSF 用本机设备信息重新登录，可能让上游轮换 `clientId`，导致 xbt 后续人脸签到签名失效。当前链接会携带 xbt 的 `dc`，但 CSF 是否在所有登录路径原样使用、上游是否接受，仍需真机确认。

### 5.5 上游风控与验证码

短时间在新设备、新 IP 登录多个账号，可能触发验证码、设备验证或临时封禁。CSF 导入失败时只能提示上游错误。

缓解现状：失败不自动重试；界面提示“请勿截图外传”，但没有频率限制。**限速未实现**，见第 6 节。

### 5.6 版本与兼容

CSF 未承诺链接格式版本。xbt 的格式固定于 `9ae5398`，CSF 升级后可能在无提示的情况下失效。当前没有生成时的版本自检，也没有在 UI 标注适用版本。

### 5.7 合规与授权

- CSF 是第三方 AGPLv3 应用；xbt 只做 URL 格式互操作，不复制其代码、不打包、不修改。
- 学习通账号密码交给第三方应用，涉及平台条款和账号安全；必须由账号本人明确同意。
- 管理员批量导出的是他人密码；当前没有二次确认、没有账号本人授权流程，部署方需自行约定内部授权，见第 6 节。

## 6. 未实现 / 后续

| 项目 | 状态 | 说明 |
|---|---|---|
| 限速（单批上限、30 秒 429） | **未实现** | 批量接口缺省导出全部账号，没有单批上限、请求间隔或 429；账号量大时响应体和二维码生成耗时线性增长。 |
| 二次确认 | **未实现** | 本人和管理员导出都直接请求并展示，没有“确认账号列表 + 风险提示”的中间步骤。 |
| 脱敏手机号 | **未实现** | 批量页面直接展示完整手机号（`phone` 字段），未做中间四位打码；本人导出面板不展示手机号。 |
| 逐张展示 / 遮挡模式 | **不做** | 用户已否决；现状为同屏卡片常显。 |
| 二维码定时隐藏 | **不做** | 用户已否决；关闭面板/离开页面即消失。 |
| 链接有效期、撤销、一次性 | **未实现** | CSF 只在导入时解析，xbt 无法事后让已发出的链接失效。 |
| CSF 版本自检 | **未实现** | 未固定“已验证版本”范围，也没有格式变化时的显式报错。 |
| 真机端到端验证 | **未完成** | 无测试学习通账号与 CSF 安装实例；“xbt 链接 → CSF 扫码/粘贴 → 上游登录成功 → 可代签”仍需手工验证。 |

## 7. 验收清单（按当前实现）

自动化测试位于 `Server/internal/faker/faker_test.go` 与 `Server/internal/handler/faker_export_test.go`。

1. 普通用户调用 `GET /api/account/faker-export` 只得到 JWT 对应账号；换用他人 token 不会导出被换账号（`TestFakerExportSelfOnlyExportsJWTAccount`）。
2. 非管理员调用 `GET /api/admin/accounts/faker-export` 返回 `403`，响应体不含账号字段（`TestFakerExportAdminBatchRejectsNonAdmin`）。
3. 三个接口的响应头均为 `Cache-Control: no-store`（本人、批量、ZIP 测试均断言）。
4. 二维码内容与接口返回的 `url` 完全一致：`qr_png` 由同一 `url` 生成；人工用扫码工具解码核对（未做自动化解码断言）。
5. ZIP 内含每个成功账号的 `qr-<uid>.png` 与 `links.txt`，`links.txt` 行数与成功账号数一致（`TestFakerExportAdminZipContainsOnePNGPerAccount`）。
6. `?uids=` 只返回指定账号并去重；空项、非法值、非正数返回 `400`（`TestFakerExportAdminBatchFiltersUIDs`、`TestFakerExportAdminBatchRejectsInvalidUIDs`）。
7. 单个账号凭据无法解密时按条返回 `error`，其余账号继续生成（批量 JSON）；ZIP 跳过该账号。
8. 响应体与服务端日志不出现明文密码或 `pwd=` 原值（`TestFakerExportLogsNeverContainPassword`）。
9. 展示页不使用 `localStorage`/`sessionStorage`/IndexedDB 保存 `url` 或 `pwd`；关闭面板/刷新后需重新请求。
10. 二维码常显：没有“显示/遮挡”按钮，没有 30 秒定时器。

## 8. 本地验证

前置条件：PostgreSQL 可连接；Go 1.25；测试会自行建库迁移。

```bash
cd Server
export XBT_TEST_POSTGRES_DSN='postgres://xbt:xbt@127.0.0.1:5432/xbt?sslmode=disable'

# 只跑 faker 相关
go test ./internal/faker ./internal/handler -run 'TestFaker|TestBuildURL|TestEncryptPassword|TestQRPNG' -count=1

# 全量
go test ./... -count=1
go vet ./...
```

常见失败：

- `XBT_TEST_POSTGRES_DSN` 未设置或数据库不可达：测试在建立连接时失败，先确认 DSN 与端口。
- 本地未安装 Go 或无网络拉依赖：先执行 `go mod download`。

手工端到端验证（仍待完成）：

1. 在 xbt 生成一个测试账号的二维码/链接。
2. 在 CSF `9ae5398` 扫码或粘贴链接。
3. 确认账号出现在“代签用户”列表，并能完成一次不敏感签到。
4. 核对 CSF 侧保存的 `deviceCode`、cookies 与上游登录结果。

## 9. 实现与变更记录

- OMP-62（2026-10-21，服务端）：新增 `Server/internal/faker` 与 `Server/internal/handler/faker_export.go`，提供本人导出、管理员批量导出、批量 ZIP 三个接口。
- OMP-63（2026-10-21，Web）：账号管理加入本人导出面板；管理后台加入 `/admin/accounts/faker-export`，同屏卡片 + 复制链接 + ZIP 下载，二维码常显。
- OMP-68（2026-10-21，文档）：本文从讨论稿改为已实现说明，删除被否决的遮挡/定时隐藏方案与未实现的限速验收项，新增“未实现 / 后续”清单。
