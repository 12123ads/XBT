# S12 签到协议 E2E 回归与验收

> 日期：2026-10-20
> 工作区：`/root/xbt`，分支 `feature/captcha-protocol-fix`
> 代码基线：`2e574f7`（S10 已完成）
> 环境：Linux arm64；Go 1.25.5；Node v22.23.3 / npm 10.9.9；本地 PostgreSQL（`docker-compose.yml` 的 `xbt` 库）
> 说明：本次验收没有真实学习通账号与 staging 活动，所有需要真实上游的条目均标注「未执行」，未编造结果。

## 0. 结论

- 自动化：`go test ./...` 全绿（含 PostgreSQL 集成测试），`go vet ./...` 通过，`Web && npm run build` 通过。
- 协议回放：新增 `Server/internal/xxt/sign_parity_replay_test.go`，用 `httptest` 本地服务器 + 真实 `xxt.Client` 回放二维码、普通、手势、位置、签到码、验证码、人脸（带/不带 `clientId`）、已截止、官方已签、403 重登、二维码过期，共 18 个断言通过。
- 组合层重试：引用既有 service 测试验证人脸恢复、位置重试、终态错误不重试、各类签到 preSign，共 22 个断言通过。
- 未覆盖：S14（OMP-41）仍为 `blocked`；真实 staging/账号、真实验证码与人脸通过率未执行；S7b 不在本 issue 范围。详见 §4。

## 1. 自动化结果

| # | 命令 | 结果 |
|---|---|---|
| 1 | `cd Server && go vet ./...` | exit 0，无输出 |
| 2 | `cd Server && XBT_TEST_POSTGRES_DSN=<local-dev-dsn> go test ./... -count=1` | 全部 `ok`：`config`、`db`、`facephoto`、`handler`、`im`、`middleware`、`observe`、`ratelimit`、`service`、`xxt`；无 `FAIL` |
| 3 | `cd Web && npm run build` | `tsc -b && vite build` 成功，2263 modules，产物写入 `Web/dist`；仅有 chunk >500 kB 警告 |
| 4 | `cd Server && go test ./internal/xxt -run 'TestSignParityReplay' -count=1 -v` | `ok`，18 个 `--- PASS` |
| 5 | `cd Server && XBT_TEST_POSTGRES_DSN=<local-dev-dsn> go test ./internal/service -run '<人脸/位置/预签到/终态>' -count=1 -v` | `ok`，22 个 `--- PASS` |

`<local-dev-dsn>` 取仓库 `docker-compose.yml` 的本地开发凭据，未在报告中记录密码。未设置 `XBT_TEST_POSTGRES_DSN` 时，依赖数据库的测试会 `t.Skip`；本报告使用的是设置后的完整结果。

### 1.1 干净树复验（2026-10-20T09:49Z）

首轮运行时工作区含其他 run 的未提交 IM 改动，为排除污染，按提交 `9746921` 用 `git archive 9746921 | tar -x -C <scratch>` 导出干净树重跑（Go 1.25.5，PostgreSQL 16 本地库）：

| 命令（在导出树执行） | 结果 |
|---|---|
| `go vet ./...` | exit 0，无输出 |
| `XBT_TEST_POSTGRES_DSN='host=127.0.0.1 port=5432 user=xbt dbname=xbt sslmode=disable' go test ./... -count=1` | 全部 `ok`（config/db/facephoto/handler/im/middleware/observe/ratelimit/service/xxt），无 `FAIL` |
| `go test ./internal/xxt -run TestSignParityReplay -count=1 -v` | `ok`，18 个 `--- PASS`（含 3 个 face 子用例、2 个 preSign 状态、2 个 403 子用例） |
| `cd Web && npm run build`（`Web/` 与 HEAD 无差异） | 成功，2263 modules，`dist/assets/index-DGAkS9Ai.js` 1,043.76 kB |

`npm run build` 的已知警告：

```text
dist/assets/index-DGAkS9Ai.js  1,043.76 kB │ gzip: 307.09 kB
(!) Some chunks are larger than 500 kB after minification.
```

## 2. 协议回放矩阵

回放方式：`httptest.NewServer` 承载所有上游端点，测试用 transport 把 `passport2.chaoxing.com`、`i.chaoxing.com`、`mobilelearn.chaoxing.com`、`captcha.chaoxing.com`、`pan-yz.chaoxing.com` 重写到本地服务器，保留原始 `Host`；因此 cookie jar、登录、preSign、stuSignajax、验证码取图都走真实 `net/http` 路径。

| 验收项 | 测试 | 实际断言 | 结果 |
|---|---|---|---|
| 二维码 | `TestSignParityReplaySignTypes/qr` | 发现 `type=2`、`otherId=2`、`ext`；`preSign` 为 POST + `ext`，`rcode=SIGNIN:aid=100&source=15&Code=qr-code&enc=qr-enc`；`stuSignajax` 带 `enc`、`location`/`locationResult`，标量 `latitude=-1`/`longitude=-1` | PASS |
| 普通 | `.../normal` | `activeId/uid/courseId/appType/fid/name/deviceCode` 齐全；无 `enc`/`signCode`；坐标 `-1`；无 location 字段 | PASS |
| 手势 | `.../gesture` | 先请求 `checkSignCode`（1 次）再提交；带 `signCode=2468` 与精确到 6 位的小数坐标 | PASS |
| 位置 | `.../location` | 带 `address=教学楼`、`latitude=31.230400`、`longitude=121.473700`、`ifTiJiao=1`、`location`/`locationResult` | PASS |
| 签到码 | `.../sign_code` | 带 `signCode=1357` 与位置字段，无 `enc` | PASS |
| 验证码 | `TestSignParityReplayCaptcha` | 第一次提交无 `validate`/`enc2`；收到 `validate_cap-enc` 后解一次验证码；第二次带 `validate=validate-ok&enc2=cap-enc`；总提交 2 次，`CaptchaAttempted/Passed=true` | PASS |
| 人脸：可解密 clientId | `TestSignParityReplayFaceSignature/decryptable_clientId` | `check-face-result` 的 `faceResult` 带 `cxcid=signed-cid`，`signToken` 与协议拼接/MD5 结果一致 | PASS |
| 人脸：无 clientId | `.../missing_clientId` | `faceResult` 只有基础字段，不含 `cxcid`/`signToken` | PASS |
| 人脸：不可用 clientId | `.../unusable_clientId` | 解密失败时降级为无签名，不报错、不带签名 | PASS |
| 已截止 | `TestSignParityReplayPreSignStates/deadline` | preSign 页面含「下次早点哦」→ `PreSignExpired`，不发起 `stuSignajax` | PASS |
| 官方已签 | `.../official_already_signed` | preSign `signstatus=9` → `PreSignAlreadySigned`，不发起 `stuSignajax` | PASS |
| 403 重登 | `TestSignParityReplayForbiddenRelogin/recovers_once` | 第一次 preSign 403 → 重新登录 1 次 → 重试 1 次成功；`login=2`、`preSign=2`，重试仍带 `ext` | PASS |
| 403 终态 | `.../terminal_second_403` | 第二次仍 403 → `ErrSessionExpired`；`login=2`、`preSign=2`，无第三次请求 | PASS |
| 二维码过期 | `TestSignParityReplayQRExpired` | `签到失败，请重新扫描。` → `qr_expired`，只提交 1 次，不重试 | PASS |

组合层重试（既有测试，本次复跑）：

| 场景 | 测试 | 结果 |
|---|---|---|
| 人脸 `checkFace_` 只恢复一次 | `TestSignExecuteFaceChallengeRetriesExactlyOnce` | PASS（2 子用例） |
| 同一活动人脸照片只上传一次，重复签到复用 objectId | `TestFaceUploadReusedWithinActivity` | PASS |
| 持久化 clientId 传给 `check-face-result` | `TestFaceCheckUsesPersistedClientID` | PASS |
| 无 clientId 时降级 | `TestFaceCheckOmitsClientIDWhenStorageIsEmpty` | PASS |
| `errorLocation_` 只重试一次且不重新随机化 | `TestSignExecuteLocationErrorRetriesExactlyOnce` | PASS（2 子用例） |
| 官方已签落库为 upstream 来源 | `TestSignExecuteAlreadySignedIsRecordedAsUpstreamSource` | PASS |
| `captcha_required`/`qr_expired`/`session_expired`/`upstream_error`/`location_out_of_range` 不重试 | `TestSignExecuteTerminalErrorsDoNotRetry` | PASS（5 子用例） |
| 五种类型都先 preSign 且携带活动 `ext` | `TestSignExecutePreSignsEverySignTypeWithActivityExt` | PASS（5 子用例） |

## 3. 手工/staging 清单

无真实账号，以下全部 **未执行**。表中的请求参数与重试次数来自协议回放/既有测试，不是 staging 实测结果。

| 签到类型 | 请求参数（回放已断言） | 失败重试次数（自动化） | staging 结果 |
|---|---|---|---|
| 二维码 | `activeId/uid/courseId/appType/fid/name/deviceCode/enc`，`latitude=-1`、`longitude=-1`，`location`/`locationResult`；preSign `rcode` | 上游 1 次；二维码过期不重试；验证码最多 +1 次 | 未执行 |
| 普通 | 通用参数 + `latitude=-1`/`longitude=-1` | 上游 1 次；验证码最多 +1 次 | 未执行 |
| 手势 | 通用参数 + `signCode` + `checkSignCode` + 位置 | `checkSignCode` 1 次；提交 1 次 | 未执行 |
| 位置 | 通用参数 + `address/latitude/longitude/ifTiJiao=1` + `location`/`locationResult` | `errorLocation_` 最多 +1 次，重试使用未随机化基点 | 未执行 |
| 签到码 | 通用参数 + `signCode` + 位置 | 提交 1 次 | 未执行 |
| 人脸 | `objectId/currentFaceId/ifCFP=0/vpProbability=-1/vpStrategy/faceCode=""/faceEncAid=""/faceEnc`；恢复时带 `enc2`；`check-face-result` 的 `faceResult` 带设备签名（可用 clientId 时） | 人脸挑战最多 +1 次；同一次执行内只上传 1 次（不同执行重新变换上传） | 未执行 |
| 验证码 | 首次不带 `validate/enc2`；重试带 `validate` + `enc2`；`get/conf` + 取图 + 校验各 1 次 | 最多 +1 次提交 | 未执行 |
| 已截止 | preSign 返回截止标记；不提交 `stuSignajax` | 0 次提交 | 未执行 |
| 官方已签 | preSign 返回已签状态；不提交 `stuSignajax` | 0 次提交 | 未执行 |
| 403 重登 | preSign 403 → 强制重登 1 次 → 重试 1 次 | 最多 2 次 preSign；第二次 403 终止 | 未执行 |

通过率记录（按 PM 增补要求）：

| 指标 | 来源 | 结果 |
|---|---|---|
| 验证码通过率 | staging 手工记录 | 未执行 |
| 人脸签到通过率 | staging 手工记录 | 未执行 |

## 4. 未覆盖项

1. **S14 / OMP-41（群聊签到来源）**：OMP-41 当前为 `blocked`，因此未将「打开页面触发抓取并合并、`attachmentType==15` 按 `activeId` 去重、间隔内不重复抓上游、并发只抓一次、IM 超时降级、无请求期间不发 IM 请求」纳入本次验收。工作区存在相关未提交改动与测试，但未随本 issue 提交，也不能据此刻画为已覆盖。
2. **真实 staging / 真实账号**：二维码、普通、手势、位置、签到码、人脸、验证码的上游实际返回与通过率均未执行；报告中的「PASS」仅代表本地 fixture 回放。
3. **真实 `clientId` 兼容性**：S13 签名路径回放使用固定测试密钥（`device_test.go` 的 `clientIDFixture`）替换 `faceResultKey`，验证的是「可解密则签名、不可解密则降级」的分支；上游真实私钥签发的 `clientId` 无法在当前环境验证。
4. **真实验证码识别率**：回放使用确定性合成图片（固定答案 x=100），不代表真实滑块识别通过率。
5. **Web/浏览器端到端**：只执行了 `npm run build`；未用真实浏览器验证签到页交互与错误码展示。
6. **S7b（人脸照片池/官方照片导入）**：本报告的范围仍只到 S12；S7b 随后由 OMP-39 实现，OMP-52 又将其改为「官方原图作为基线落库、每次执行在内存变换后上传」：`ImportOfficial` 原样保存并返回 `transform_deferred=true`，空池自动导入一次，同一执行内重试只上传一次，基线字节在执行前后 SHA-256 不变——以上均有本地单测；但「变换后的照片能被真实 `check-face-result` 接受」仍需 staging 账号实测。实现细节见 `docs/sign-parity-plan.md` §8.3.3。
7. **403 真实重登**：回放验证了客户端预算（1 次重登 + 1 次重试）与终态错误，未对真实学习通会话验证。

## 5. 剩余风险

1. **共享工作区并发修改（已消除影响）**：首轮测试运行时工作区存在其他 run 对 `Server/internal/service/im_fetch.go`、`Server/internal/xxt/im_account.go` 等的未提交 IM 改动；§1.1 已在 `9746921` 的干净导出树上复跑全量套件与回放，结果一致，故不再污染本报告结论。本 issue 的提交只包含回放测试、报告和一处测试注入点，不包含上述 IM 改动。
2. **二维码坐标形状**：协议构造函数对二维码固定发送标量 `latitude=-1`/`longitude=-1`，坐标放在 `location`/`locationResult` 中；需 staging 确认上游接受该组合。
3. **设备签名密钥注入点**：`device.go` 新增包级 `faceResultKey` 变量，仅用于测试替换；生产默认仍为 `deviceInfoRSAKey`，行为不变。
4. **构建体积**：Web 主 chunk 1,043.76 kB（gzip 307.09 kB），构建有 >500 kB 警告，非本次协议改动引入，但会影响首屏。
5. **S14 未验收**：群聊签到来源仍可能漏发现活动；在 OMP-41 完成前不能认为「签到来源」已对齐。

## 6. 本次改动文件

- `Server/internal/xxt/sign_parity_replay_test.go`：新增 S12 协议回放测试（httptest + 真实 client）。
- `Server/internal/xxt/device.go`：新增 `faceResultKey` 测试注入点，默认行为不变。
- `docs/sign-parity-acceptance.md`：本报告。

## 7. 下一步

1. 解除 OMP-41 阻塞后补 S14 回放：按需抓取/合并、`activeId` 去重、30s 间隔、并发单飞、IM 超时降级、空闲不发请求。
2. 准备 staging 账号与至少一个真实活动，按 §3 逐类执行并填写通过率。
3. 用真实 `clientId` 验证人脸签名；若上游对 `faceResult` 校验加严，优先复测 `cxcid`/`signToken`。
4. 在真实上游确认二维码 `location` 与 `latitude=-1` 的组合。
