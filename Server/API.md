# 学不通 2.0 后端接口文档

- Base URL: `http://<host>:3030`
- REST 前缀: `/api`
- 返回格式统一:

```json
{
  "code": 0,
  "message": "ok",
  "data": {}
}
```

`code = 0` 表示成功，`code = 1` 表示失败。

## 1. 鉴权说明

### 1.1 Bearer Token
除登录与健康检查外，所有 REST 接口都需要在请求头携带：

```http
Authorization: Bearer <JWT>
```

### 1.2 权限等级
- `permission = 1`: 普通用户
- `permission = 2`: 管理员

白名单管理接口仅管理员可访问。

---

## 2. 通用接口

### 2.1 健康检查
- Method: `GET`
- Path: `/api/health`
- Auth: 否

响应示例：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "service": "xbt2-server"
  }
}
```

---

## 3. 认证接口

### 3.1 登录
- Method: `POST`
- Path: `/api/auth/login`
- Auth: 否

请求体：

```json
{
  "mobile": "13800000000",
  "password": "your_password"
}
```

响应体：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "token": "<jwt>",
    "user": {
      "uid": 123456,
      "name": "张三",
      "mobile": "138****0000",
      "avatar": "https://...",
      "permission": 2
    }
  }
}
```

说明：
- 若白名单为空，首次登录用户会自动成为管理员（`permission=2`）。
- 若账号不在白名单，会返回未授权。

---

## 4. 课程接口

### 4.1 获取课程列表
- Method: `GET`
- Path: `/api/courses`
- Auth: 是

响应体 `data`:

```json
[
  {
    "class_id": 111,
    "course_id": 222,
    "name": "高等数学",
    "teacher": "李老师",
    "icon": "https://...",
    "is_selected": true
  }
]
```

### 4.2 同步课程
- Method: `POST`
- Path: `/api/courses/sync`
- Auth: 是

请求体：无

响应体 `data`:

```json
{
  "count": 12
}
```

### 4.3 更新监控课程选择
- Method: `PUT`
- Path: `/api/courses/selection`
- Auth: 是

请求体：

```json
{
  "course_ids": [222, 333]
}
```

响应体 `data`:

```json
{
  "selected_count": 2
}
```

说明：
- 当前实现按 `course_id` 更新选中状态（与前端当前实现一致）。

---

## 5. 签到接口

### 5.1 获取已选课程的签到活动
- Method: `GET`
- Path: `/api/sign/activities`
- Auth: 是

响应体 `data`:

```json
[
  {
    "course_id": 222,
    "class_id": 111,
    "course_name": "高等数学",
    "course_teacher": "李老师",
    "icon": "https://...",
    "has_more": false,
    "activities": [
      {
        "active_id": 987654,
        "activity_name": "课堂签到",
        "type": 2,
        "status": 1,
        "user_status": 0,
        "is_sign_out": false,
        "activity_state": "ongoing",
        "start_time": 1760000000000,
        "end_time": 1760003600000,
        "sign_type": 2,
        "if_refresh_ewm": false,
        "if_photo": false,
        "if_face": false,
        "if_need_vcode": false,
        "if_open_address": false,
        "location_latitude": 0,
        "location_longitude": 0,
        "location_range": 0,
        "record_source": 0,
        "record_source_name": "",
        "record_sign_time": 1760000500000,
        "course_name": "高等数学",
        "course_id": 222,
        "class_id": 111,
        "course_teacher": "李老师",
        "source": "im_group",
        "sources": ["taskactivelist", "im_group"],
        "im_atype": 2,
        "im_sub_title": "10-20 10:00",
        "im_message_time": 1760000200000
      }
    ]
  }
]
```

`record_source` 含义：
- `0`: 尚未签到。
- `-1`: 该同学已在学习通自行签到。
- `= 当前用户 uid`: 该同学由当前用户代签（本人签到时也是该值）。
- `>0 且 != 当前用户 uid`: 该同学已被其他用户代签。

`record_source_name` 含义：
- `""` (空字符串): 尚未签到。
- `"学习通"`: 该同学已在学习通自行签到。
- 其他字符串（例如 `"张三"`）: 表示该同学被该用户代签。

`sign_type` 含义：
- `0` 普通签到
- `2` 二维码签到
- `3` 手势签到
- `4` 位置签到
- `5` 签到码签到

说明：当 `sign_type=0` 且 `if_photo=true` 时，该活动为拍照签到，应调用 `/api/sign/photo`。当 `if_face=true` 时，签到时需要携带 `if_face=true`（`/api/sign/execute` 的 `special_params` 或 `/api/sign/photo` 的表单字段），后端会自动使用该用户已上传的人脸照片完成验证。

说明：
- 每门课程默认最多返回最新 `5` 条签到活动（可通过后端 `Server/config.yaml` 中的 `activity_list_limit` 配置）。
- 当该课程活动总数超过返回条数时，`has_more=true`。
- `type` 为上游活动种类：`2` 签到、`74` 签退；`0` 表示上游未返回该字段。
- `is_sign_out=true` 表示签退活动（`type=74` 或名称含“签退”），随普通签到活动一起返回。
- `status`、`user_status` 为上游原始值，仅做保留，状态判断以 `activity_state` 为准。
- `activity_state` 取值 `not_started`（未开始）、`ongoing`（进行中）、`expired`（已结束/已失效）。过期和未开始的活动同样返回并标记，不再静默丢弃。
- `sort` 规则不变：组内按 `start_time` 倒序，再按 `activity_list_limit` 截断。
- 活动 `ext` 会写入后端 `sign_activities.ext_json` 缓存，供 `preSign` 在元数据接口失败时回退使用；不在列表响应中返回。
- `source` 为 `taskactivelist`（活动列表轮询）或 `im_group`（Easemob 群聊消息）；两者都发现同一 `active_id` 时只返回一条，`sources` 同时包含两个值，`source` 取 `im_group`。
- `im_atype`、`im_sub_title`、`im_message_time` 仅在群聊来源存在时返回；`im_atype=2` 为签到，`74` 为签退。群聊来源只做发现与跳转，不自动提交签到。
- 群聊来源失败不会影响 `taskactivelist` 结果；错误码通过 `GET /api/sign/im/status` 查询。

### 5.2 获取同班同学（可代签目标）
- Method: `GET`
- Path: `/api/sign/classmates`
- Auth: 是
- Query:
  - `course_id` (required)
  - `class_id` (required)

示例：

```http
GET /api/sign/classmates?course_id=222&class_id=111
```

响应体 `data`:

```json
[
  {
    "uid": 10001,
    "name": "王五",
    "mobile_masked": "139****0000",
    "avatar": "https://..."
  }
]
```

### 5.3 查询待签状态
- Method: `POST`
- Path: `/api/sign/check`
- Auth: 是

请求体：

```json
{
  "activity_id": 987654,
  "user_ids": [10001, 10002]
}
```

说明：
- 后端会自动把当前登录用户加入查询列表。
- 前端可据此过滤出未签用户，再自行并发调用执行签到接口。
- 本地无签到记录的用户会在本次查询中向学习通发起一次 `preSign`（POST `ext`）确认状态：
  - 上游已签：返回 `signed=true`、`record_source=-1`、`record_source_name="学习通"`，并写入 `source_uid=-1` 的签到记录；之后的查询不再请求上游。
  - 活动已截止：`signed=false`、`checked_upstream=true`，`message="签到已截止"`，不写记录。
  - 凭据/网络/上游失败：保留本地状态，`checked_upstream=false`，整个查询不因此失败。
- `checked_upstream` 表示该用户的状态是否由本次上游查询确认；已有本地记录的用户为 `false`。

响应体 `data`:

```json
{
  "items": [
    {
      "user_id": 343479151,
      "signed": true,
      "record_source": 343479151,
      "record_source_name": "张三",
      "message": "该同学已本人签到",
      "checked_upstream": false
    },
    {
      "user_id": 10001,
      "signed": false,
      "record_source": 0,
      "record_source_name": "",
      "message": "未签到",
      "checked_upstream": true
    },
    {
      "user_id": 10002,
      "signed": true,
      "record_source": -1,
      "record_source_name": "学习通",
      "message": "该同学已在学习通签到",
      "checked_upstream": true
    }
  ]
}
```

### 5.4 执行单用户签到
- Method: `POST`
- Path: `/api/sign/execute`
- Auth: 是

请求体：

```json
{
  "activity_id": 987654,
  "target_uid": 10001,
  "sign_type": 2,
  "course_id": 222,
  "class_id": 111,
  "if_refresh_ewm": false,
  "if_open_address": false,
  "special_params": {
    "enc": "xxxx",
    "latitude": "30.7501",
    "longitude": "103.9272",
    "description": "成都市郫都区xxx"
  }
}
```

兼容说明：
- 若未传 `target_uid`，但传了 `user_ids`，后端会使用 `user_ids` 的第一个 uid。
- 若都未传，默认签当前登录用户。
- `if_open_address` 对应活动元数据里的 `if_open_address`：服务端执行签到时会读取 `getPPTActiveInfo`（30s 缓存）作为权威来源，请求体字段只在读取失败时兜底。为 `true` 且元数据带有圆心/半径时：
  - 若 `special_params` 提供了经纬度，服务端先按 haversine 距离校验预设点；超出 `location_range` 直接返回 `location_out_of_range` 与 `distance_m`，不会请求 `preSign`/`stuSignajax`。
  - 服务端随后在元数据圆内面积均匀随机取点（6 位小数）提交；未提供经纬度时同样按元数据圆心生成，因此签到类型 `0/2/3/4/5` 的提交参数都会带 `location`、`locationResult`（仅后者含 `mockData`）；类型 `4` 另带 `address` 与 `ifTiJiao=1`。
  - 上游返回 `errorLocation_<米>` 时关闭随机化，用未随机化的基准点（用户预设点或元数据圆心）重试一次；第二次仍失败返回上游距离。
- 无可用圆心/半径时不做校验与随机化：提供了经纬度就按原值提交，未提供则不下发伪造坐标。

响应体 `data`:

```json
{
  "user_id": 10001,
  "success": true,
  "already_signed": false,
  "record_source": 343479151,
  "record_source_name": "张三",
  "message": "签到成功",
  "error_code": "",
  "retryable": false
}
```

`error_code` 是上游响应的类型化结果，前端不再解析 `message`/原始响应：

| error_code | 含义 | `retryable` | 备注 |
|---|---|---|---|
| `""` | 签到成功（含 `success2` 迟到/已结束、本地已有记录） | `false` | `success=true` |
| `captcha_required` | 验证码校验未通过 | `true` | 服务端已带 `validate`+`enc2` 自动重试一次 |
| `face_required` | 人脸验证未通过或未设置人脸照片 | `false` | 服务端已自动做人脸恢复重试一次 |
| `location_out_of_range` | 位置超出范围 | `false` | 预设点校验或上游返回距离时带 `distance_m`（米）；服务端已关闭随机化重试一次 |
| `qr_expired` | 二维码已过期 | `false` | 需要重新扫码 |
| `activity_expired` | 签到已截止 | `false` | 上游 `preSign` 判定已截止，不写记录 |
| `session_expired` | 学习通会话失效 | `false` | 需要重新登录 |
| `upstream_error` | 其他上游错误 | `true` | `message` 为可读提示 |

所有签到类型（普通/二维码/手势/位置/签到码）执行前都会先请求 `/newsign/preSign`：POST `ext`（来自活动元数据 `getPPTActiveInfo`），二维码额外带 `rcode`。`preSign` 返回 403 时服务端重新登录一次并重试一次；再次 403 或重登失败返回 `session_expired`，不会循环。

上游返回“您已签到过了”时会写入 `source_uid=-1` 的学习通来源记录，之后的 `/api/sign/check` 与重复执行都只读本地记录，不再请求上游签到。

`special_params` 按签到类型：
- 普通签到(`0`): 可空
- 二维码签到(`2`):
  - `enc` (required)
  - `c` (optional, 预签到场景可用)
  - `latitude` + `longitude` + `description` (optional; `if_open_address=true` 时用于 `location`/`locationResult`)
- 手势签到(`3`):
  - `sign_code` (required)
  - `latitude` + `longitude` + `description` (optional; `if_open_address=true` 时用于 `location`/`locationResult`)
- 位置签到(`4`):
  - `latitude` (required)
  - `longitude` (required)
  - `description` (required)
  - 位置预设约定使用百度 `BD-09` 坐标系；字段格式仍为十进制度字符串。
  - 浏览器定位来源为 `WGS-84`，前端会离线转换为百度 `BD-09` 后提交。
- 签到码签到(`5`):
  - `sign_code` (required)
  - `latitude` + `longitude` + `description` (optional; `if_open_address=true` 时用于 `location`/`locationResult`)

`deviceCode` 由服务端按账号稳定派生（`HMAC-SHA256(chaoxing_aes_key, "xbt-device-code:v1:<uid>")`，64 位小写十六进制），同账号多次签到一致、不同账号不同，不使用真实硬件标识；轮换 `chaoxing_aes_key` 会改变全部账号的 `deviceCode`。`validate`/`enc2` 只在验证码/人脸状态机内部写入，首发请求不携带。

### 5.5 签到分享接口

签到分享用于生成免登录链接。分享页不会返回或展示账号列表；执行目标由后端按分享者在当前课程班级下可代签范围动态计算。

#### 5.5.1 创建分享链接
- Method: `POST`
- Path: `/api/sign/shares`
- Auth: 是

请求体：

```json
{
  "activity_id": 123456,
  "course_id": 222,
  "class_id": 111,
  "sign_type": 5,
  "if_refresh_ewm": false,
  "activity_name": "签到",
  "course_name": "高等数学",
  "course_teacher": "李老师",
  "end_time": 1760000000000
}
```

响应体 `data`:

```json
{
  "token": "raw-share-token",
  "expires_at": 1760000000000
}
```

#### 5.5.2 获取分享活动信息
- Method: `GET`
- Path: `/api/sign/shares/:token`
- Auth: 否

响应体 `data` 只包含活动和签到类型信息，不包含账号信息。

#### 5.5.3 执行分享签到
- Method: `POST`
- Path: `/api/sign/shares/:token/execute`
- Auth: 否

请求体：

```json
{
  "special_params": {
    "sign_code": "1234"
  }
}
```

说明：
- 普通签到可传空 `special_params`。
- 二维码签到传 `enc/c`。
- 位置签到传 `latitude/longitude/description`，坐标使用百度 `BD-09`。
- 当目标范围全部成功或已签到后，分享链接会标记为已使用并失效；部分失败时可在活动结束前继续重试。
### 5.6 拍照签到
- Method: `POST`
- Path: `/api/sign/photo`
- Auth: 是
- Content-Type: `multipart/form-data`

请求字段：
- `activity_id` (required): 签到活动 ID
- `course_id` (required): 课程 ID
- `class_id` (required): 班级 ID
- `target_uid` (optional): 代签目标用户；不传则默认当前登录用户
- `if_refresh_ewm` (optional): 与活动详情中的 `if_refresh_ewm` 一致
- `if_face` (optional): 活动要求人脸验证时传 `true`
- `file` (optional): 照片文件，字段名固定为 `file`，最大 20MB
- `object_id` (optional): 已上传到超星云盘的图片 `objectId`；传了 `object_id` 时可以不传 `file`

说明：
- 后端会使用目标用户的学习通登录凭据上传照片到超星云盘，获取 `objectId` 后再提交拍照签到。
- 如果你已经有图片 `objectId`，可以直接传 `object_id`，后端会跳过上传步骤。
- 响应体与 `/api/sign/execute` 一致。

示例：

```bash
curl -X POST http://localhost:3030/api/sign/photo \
  -H "Authorization: Bearer <JWT>" \
  -F "activity_id=987654" \
  -F "course_id=222" \
  -F "class_id=111" \
  -F "target_uid=10001" \
  -F "file=@photo.jpg"
```

### 5.7 人脸照片接口
人脸照片用于需要人脸验证的签到活动。照片保存在 `users.face_photo`，上传时会自动压缩到 720×1280 以内并尽量控制在 100KB 以下。

#### 5.7.1 查询人脸照片
- Method: `GET`
- Path: `/api/user/face`
- Auth: 是

可选参数 `status_only=1` 只返回是否存在照片：

```json
{ "has_face": true }
```

不传 `status_only` 时额外返回 `face_data`（JPEG data URL）。

#### 5.7.2 上传/替换人脸照片
- Method: `POST`
- Path: `/api/user/face`
- Auth: 是
- Content-Type: `multipart/form-data`

字段：`file`（必填，图片，最大 20MB）。

#### 5.7.3 删除人脸照片
- Method: `DELETE`
- Path: `/api/user/face`
- Auth: 是

说明：签到时若活动 `if_face=true`，后端会为目标账号上传该照片并换取 `currentFaceId/faceEnc/faceCode`，再提交签到参数。

### 5.8 功能开关
- Method: `GET`
- Path: `/api/features`
- Auth: 是

响应体 `data`:

```json
{
  "contributions_enabled": true,
  "class_admin_group_id": 1,
  "class_admin_group_name": "计科 2301"
}
```

说明：`contributions_enabled` 由管理员通过 `PUT /api/admin/settings` 修改。关闭后主页隐藏贡献榜入口，`GET /api/sign/contributions` 返回 `403`。`class_admin_group_id/name` 仅当当前账号是某个班级的班级管理员时返回。

### 5.9 群聊签到按需抓取状态
- Method: `GET`
- Path: `/api/sign/im/status`
- Auth: 是

响应体 `data`:

```json
{
  "enabled": true,
  "interval_seconds": 30,
  "fetch_timeout_seconds": 5,
  "error_code": "",
  "last_poll_at": 1760000200000,
  "last_success_at": 1760000200000,
  "group_count": 3,
  "activity_count": 2
}
```

`error_code` 可能值：
- `""`: 最近一次抓取成功。
- `im_auth_failed`: 学习通凭据或 IM 密码解密失败。
- `im_not_configured`: 该账号没有 Easemob IM 账号。
- `im_unauthorized`: 重取 token 后仍返回 401/408。
- `im_token_failed`: token 端点失败。
- `im_groups_failed` / `im_messages_failed`: 群列表/消息端点失败。
- `im_parse_failed`: 上游响应无法解析。
- `im_timeout`: 抓取超过 `im_fetch_timeout_seconds`。
- `im_network`: 网络或未知失败。
- `im_disabled`: 服务端关闭了群聊抓取（`im_disabled: true` 或 `im_min_fetch_interval_seconds: 0`）。

说明：
- 触发方式为 **B 方案**：`GET /api/sign/activities` 处理时，若距上次抓取 ≥ `im_min_fetch_interval_seconds`（默认 `30`）则同步抓一次，硬超时 `im_fetch_timeout_seconds`（默认 `5s`）；否则直接用快照，不发上游请求。没有后台 ticker，进程启动时不抓取。
- 同一账号并发抓取不超过 1：已有抓取在跑时，本次请求直接用旧快照返回，不排队、不阻塞列表。
- 抓取只缓存解析后的签到字段（`active_id/title/course/atype`），不持久化原始消息、IM 密码或 `access_token`。
- 只有 `401/408` 会触发一次 token 重取并重试一次；第二次仍失败则记录 `im_unauthorized` 并结束本次抓取。
- 抓取失败只写 `error_code`，`taskactivelist` 结果照常返回。

---

## 6. 白名单管理接口（管理员）

> 该组接口已重构为 RESTful 资源风格，仅管理普通用户白名单（permission 固定为 1）。

### 6.1 获取普通用户白名单
- Method: `GET`
- Path: `/api/admin/whitelist/users`
- Auth: 是（管理员）

响应体 `data`:

```json
[
  {
    "id": 12,
    "uid": 343479453,
    "mobile_masked": "139****0000",
    "permission": 1
  }
]
```

### 6.2 添加普通用户白名单
- Method: `POST`
- Path: `/api/admin/whitelist/users`
- Auth: 是（管理员）

请求体：

```json
{
  "mobile": "13900000000"
}
```

响应体 `data`:

```json
{
  "id": 12,
  "uid": 343479453,
  "mobile_masked": "139****0000",
  "permission": 1
}
```

说明：
- 该接口不再接受 `permission` 参数。
- 管理员账号不会被该接口修改。

### 6.3 批量导入普通用户白名单
- Method: `POST`
- Path: `/api/admin/whitelist/users/import`
- Auth: 是（管理员）

请求体：

```json
{
  "mobiles": "13900000001\n13900000002,13900000003"
}
```

响应体 `data`:

```json
{
  "count": 3,
  "skipped_admin": 0
}
```

说明：
- 支持换行、逗号、空格混合文本。
- 自动提取手机号并去重。
- 若手机号是管理员白名单，会被跳过并计入 `skipped_admin`。

### 6.4 删除普通用户白名单
- Method: `DELETE`
- Path: `/api/admin/whitelist/users/:id`
- Auth: 是（管理员）

示例：

```http
DELETE /api/admin/whitelist/users/12
```

响应体 `data`:

```json
{
  "id": 12,
  "uid": 0,
  "mobile_masked": "139****0000"
}
```

说明：
- 管理员账号不允许删除。

### 6.5 管理面板账号与课程接口（管理员）

管理面板用于集中添加账号、同步账号课程、维护某个账号参与代签的课程，并把同一组选中课程一键套用给其他账号。

#### 6.5.1 获取已保存账号
- Method: `GET`
- Path: `/api/admin/accounts`
- Auth: 是（管理员）

响应体 `data`:

```json
[
  {
    "uid": 343479151,
    "name": "张三",
    "mobile_masked": "138****0000",
    "avatar": "https://...",
    "permission": 1,
    "last_login_at": 1760000000000,
    "course_count": 12,
    "selected_count": 3
  }
]
```

#### 6.5.2 添加账号并同步课程
- Method: `POST`
- Path: `/api/admin/accounts`
- Auth: 是（管理员）

请求体：

```json
{
  "mobile": "13800000000",
  "password": "your_password"
}
```

说明：
- 后端会登录学习通校验账号密码，并保存加密后的凭据。
- 新账号会自动加入普通用户白名单；若该手机号已是管理员，不会降级。
- 添加成功后会尝试同步课程，失败时仍返回账号信息与 `sync_message`。

#### 6.5.3 获取指定账号课程
- Method: `GET`
- Path: `/api/admin/accounts/:uid/courses`
- Auth: 是（管理员）

#### 6.5.4 同步指定账号课程
- Method: `POST`
- Path: `/api/admin/accounts/:uid/courses/sync`
- Auth: 是（管理员）

#### 6.5.5 手动给指定账号添加课程
- Method: `POST`
- Path: `/api/admin/accounts/:uid/courses`
- Auth: 是（管理员）

请求体：

```json
{
  "course_id": 222,
  "class_id": 111,
  "name": "高等数学",
  "teacher": "李老师",
  "icon": "https://...",
  "is_selected": true
}
```

#### 6.5.6 更新指定账号的代签课程选择
- Method: `PUT`
- Path: `/api/admin/accounts/:uid/courses/selection`
- Auth: 是（管理员）

请求体：

```json
{
  "courses": [
    { "course_id": 222, "class_id": 111 }
  ]
}
```

#### 6.5.7 一键套用课程
- Method: `POST`
- Path: `/api/admin/courses/copy-selection`
- Auth: 是（管理员）

请求体：

```json
{
  "source_uid": 343479151,
  "target_uids": [10001, 10002]
}
```

说明：
- 后端会读取 `source_uid` 当前已选课程。
- 对每个目标账号创建或更新相同的 `user_courses` 关系，并设为 `is_selected=true`。

### 6.6 班级分组与班级课程同步（管理员）

班级分组用于把账号按班级归档，并以班级为单位同步代签课程设置。这里的“班级”是后台管理分组，不是学习通课程里的 `class_id`。

#### 6.6.1 获取班级分组
- Method: `GET`
- Path: `/api/admin/class-groups`
- Auth: 是（管理员）

响应体 `data`:

```json
[
  {
    "id": 1,
    "name": "计科 2301",
    "description": "一班",
    "member_count": 2,
    "member_uids": [343479151, 343479152]
  }
]
```

#### 6.6.2 创建班级分组
- Method: `POST`
- Path: `/api/admin/class-groups`
- Auth: 是（管理员）

请求体：

```json
{
  "name": "计科 2301",
  "description": "一班"
}
```

#### 6.6.3 更新班级分组
- Method: `PUT`
- Path: `/api/admin/class-groups/:id`
- Auth: 是（管理员）

请求体同创建接口。

#### 6.6.4 删除班级分组
- Method: `DELETE`
- Path: `/api/admin/class-groups/:id`
- Auth: 是（管理员）

说明：只删除班级分组和成员关系，不删除账号、课程或签到记录。

#### 6.6.5 替换班级成员
- Method: `PUT`
- Path: `/api/admin/class-groups/:id/members`
- Auth: 是（管理员）

请求体：

```json
{
  "user_uids": [343479151, 343479152]
}
```

说明：一个账号只能属于一个班级；如果账号已在其他班级，会移动到当前班级。

#### 6.6.6 按班级同步课程
- Method: `POST`
- Path: `/api/admin/class-groups/:id/courses/copy-selection`
- Auth: 是（管理员）

请求体：

```json
{
  "source_uid": 343479151,
  "mode": "replace"
}
```

`mode` 可选值：
- `replace`: 先清空目标成员已选代签课程，再套用源账号已选课程。
- `append`: 只追加源账号已选课程，不取消目标成员原有选择。

响应体 `data`:

```json
{
  "target_count": 3,
  "course_count": 10,
  "copied_relations": 30,
  "mode": "replace"
}
```

### 6.7 签到记录查询（管理员）

#### 6.7.1 获取签到记录
- Method: `GET`
- Path: `/api/admin/sign-records`
- Auth: 是（管理员）

查询参数：
- `page`: 页码，默认 `1`
- `page_size`: 每页数量，默认 `20`，最大 `100`
- `keyword`: 课程名、教师、活动名、账号名或 ID 关键词
- `user_uid`: 目标账号 UID
- `source_uid`: XBT 签到来源 UID
- `activity_id`: 签到活动 ID
- `course_id`: 课程 ID
- `class_id`: 学习通班级 ID
- `sign_type`: 签到类型，`0` 普通、`2` 二维码、`3` 手势、`4` 位置、`5` 签到码
- `start_time`: 签到开始时间，毫秒时间戳
- `end_time`: 签到结束时间，毫秒时间戳

响应体 `data`:

```json
{
  "items": [
    {
      "id": 1,
      "activity_id": 987654321,
      "activity_name": "位置签到",
      "course_id": 222,
      "class_id": 111,
      "course_name": "高等数学",
      "course_teacher": "李老师",
      "sign_type": 4,
      "sign_time_ms": 1760000500000,
      "first_sign_time_ms": 1760000498000,
      "last_sign_time_ms": 1760000500000,
      "target_count": 3,
      "target_names": "张三、王五、赵六",
      "source_count": 1,
      "source_names": "李四"
    }
  ],
  "page": 1,
  "page_size": 20,
  "total": 1,
  "total_pages": 1
}
```

说明：
- 该接口只查询 XBT 本地执行成功的签到记录，不实时查询学习通，也不展示学习通已签到记录。
- 同一 `activity_id/course_id/class_id/sign_type` 会合并为一条记录，`target_count` 表示本次合并后的目标人数。
- 历史旧记录可能缺少课程或活动快照，接口会返回 `未知课程` 或 `未知活动`。

### 6.8 宿舍在寝签到（管理员）

宿舍在寝签到用于每天北京时间 `22:00` 自动执行在寝定位签到。它独立于学习通课程签到记录，不写入 `sign_records`。

#### 6.8.1 获取自动签到总览
- Method: `GET`
- Path: `/api/admin/qmx-auto-sign`
- Auth: 是（管理员）

响应包含全局开关、下次执行时间和每个账号的单独配置、最近执行结果。

#### 6.8.2 更新全局开关
- Method: `PUT`
- Path: `/api/admin/qmx-auto-sign/settings`
- Auth: 是（管理员）

请求体：

```json
{
  "enabled": true
}
```

#### 6.8.3 读取账号宿舍在寝签到定位点
- Method: `POST`
- Path: `/api/admin/qmx-auto-sign/accounts/:uid/locations/preview`
- Auth: 是（管理员）

说明：后端使用该账号保存的学习通凭据换取宿舍在寝签到凭据，并返回当前在寝签到批次和允许定位点。

#### 6.8.4 更新账号自动签到配置
- Method: `PUT`
- Path: `/api/admin/qmx-auto-sign/accounts/:uid`
- Auth: 是（管理员）

请求体：

```json
{
  "enabled": true,
  "location": {
    "location_name": "宿舍楼",
    "location_index": 0,
    "longitude": 119.123456,
    "latitude": 35.123456,
    "range": 100
  }
}
```

说明：开启单账号自动签到前必须已选择定位点。定时执行时优先按 `location_name` 匹配当前宿舍在寝签到定位点，找不到再按 `location_index` 兜底。

#### 6.8.5 单账号立即执行
- Method: `POST`
- Path: `/api/admin/qmx-auto-sign/accounts/:uid/run`
- Auth: 是（管理员）

说明：只对该账号立即执行一次，用于测试或补签；要求该账号已开启并已配置定位点。

#### 6.8.6 获取自动签到记录
- Method: `GET`
- Path: `/api/admin/qmx-auto-sign/records`
- Auth: 是（管理员）
- Query:
  - `page`: 页码，默认 `1`
  - `page_size`: 每页数量，默认 `20`，最大 `100`
  - `user_uid`: 可选，按账号过滤
  - `trigger`: 可选，`scheduled` 或 `manual`

记录包含账号、触发方式、成功状态、宿舍在寝签到返回码/消息、批次、定位点和执行时间。

### 6.9 班级管理员

班级管理员是低于全局管理员的角色，由管理员在班级成员设置中通过 `admin_uid` 指定，且必须属于该班级。

#### 6.9.1 获取本班信息
- Method: `GET`
- Path: `/api/class-admin/overview`
- Auth: 是（班级管理员）

返回当前班级和成员列表。

#### 6.9.2 添加本班账号
- Method: `POST`
- Path: `/api/class-admin/accounts`
- Auth: 是（班级管理员）

请求体：

```json
{
  "mobile": "13800000000",
  "password": "学习通密码"
}
```

行为：
- 校验学习通账号密码
- 新账号以普通用户权限加入白名单
- 自动加入班级管理员的班级（已属于其他班级的账号会被拒绝）
- 同步新账号的学习通课程
- 默认把班级管理员已选课程追加给新账号

响应体 `data` 包含 `account`、`sync_count`、`sync_message`、`copied_relations`、`course_count`、`copy_message`。

---

## 7. 错误码与常见错误

统一结构：

```json
{
  "code": 1,
  "message": "error message",
  "data": null
}
```

常见 HTTP 状态码：
- `400` 参数错误
- `401` 未登录或 token 无效
- `403` 权限不足
- `404` 资源不存在
- `500` 服务端错误

---

## 8. 联调建议

1. 先调用 `/api/auth/login` 获取 JWT。
2. 带 `Authorization: Bearer <JWT>` 调用 `/api/courses/sync`。
3. 调用 `/api/courses` + `/api/courses/selection` 选定课程。
4. 调用 `/api/sign/activities` 拿活动。
5. 调用 `/api/sign/check` 查待签状态。
6. 前端过滤出未签用户后，并发调用 `/api/sign/execute`。
