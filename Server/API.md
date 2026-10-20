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

> 例外：`/api/app/version` 的成功响应是更新客户端直接消费的裸 JSON（无 `code/message/data` 包装），详见 §2.2；它的错误响应仍使用统一结构。

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

### 2.2 应用内更新

更新接口**不需要登录**：登出状态或旧版本客户端也必须能检查并安装更新。仅支持 HTTPS，不提供 http 回退。

#### 2.2.1 检查版本

- Method: `GET`
- Path: `/api/app/version`
- Auth: 否

查询参数：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `package` | 是 | Android 包名，必须与清单一致 |
| `versionCode` | 是 | 客户端当前版本号，非负整数 |
| `abi` | 是 | 客户端 ABI，当前仅 `arm64-v8a` |

请求示例：

```http
GET /api/app/version?package=com.github.enderwolf006.xbt&versionCode=3&abi=arm64-v8a
```

有更新时返回 `200` 裸 JSON：

```json
{
  "latest": {
    "versionCode": 4,
    "versionName": "2.0.2",
    "apkUrl": "https://xbt.example.com/api/app/download/xbt-2.0.2.apk",
    "sha256": "<hex>",
    "sizeBytes": 12345678,
    "changelog": "...",
    "publishedAt": "2026-10-21T00:00:00Z"
  },
  "minSupportedVersionCode": 3,
  "mandatory": false
}
```

- `mandatory` 为 `true` 当且仅当请求的 `versionCode < minSupportedVersionCode`。
- 请求的 `versionCode` **大于等于**最新 `versionCode` 时返回 `204 No Content`，响应体为空（客户端无需再解析 JSON）。
- `apkUrl` 始终是 `https://`。配置了 `public_base_url` 时使用该 origin；留空时使用请求的 `Host` 并强制 `https`。
- 包名不匹配或 ABI 未知：`404`，响应体不包含其它包的信息。
- 参数缺失或 `versionCode` 非法：`400`。
- 清单缺失、损坏、APK 缺失或 `sha256`/`sizeBytes` 与文件不一致：`503`。此时服务继续启动，其它接口不受影响；启动日志包含 `app_release status=unavailable` 和具体原因。

#### 2.2.2 下载 APK

- Method: `GET`
- Path: `/api/app/download/<file>.apk`
- Auth: 否

响应头：

- `Content-Type: application/vnd.android.package-archive`
- `Accept-Ranges: bytes`
- `ETag: "<sha256>"`
- `Last-Modified: <apk mtime>`
- `Cache-Control: public, max-age=31536000, immutable`

支持 `Range`（返回 `206 Partial Content`）、`If-None-Match` / `If-Modified-Since`（返回 `304`）以及 `If-Range`。只提供当前清单中 `latest.apkFile` 指向的文件，目录中的其它 APK 或测试包不会被下载。

#### 2.2.3 版本清单

清单路径由 `config.yaml` 的 `app_release_manifest` 指定，默认 `./releases/releases.json`；APK 放在同一目录。服务**启动时加载**，发布新版本后必须重启。示例见 `Server/releases/releases.json.example`：

```json
{
  "package": "com.github.enderwolf006.xbt",
  "abi": "arm64-v8a",
  "minSupportedVersionCode": 3,
  "latest": {
    "versionCode": 4,
    "versionName": "2.0.2",
    "apkFile": "xbt-2.0.2.apk",
    "sha256": "<hex>",
    "sizeBytes": 12345678,
    "changelog": "...",
    "publishedAt": "2026-10-21T00:00:00Z"
  }
}
```

- `sha256`、`sizeBytes` 可省略；由服务启动时对 APK 实际计算。若填写则必须与文件一致，否则启动时该接口返回 `503`。
- `versionCode` 必须单调递增；`Android/build-release.sh` 会以已发布的 `releases.json` 为基线拒绝版本号未增加的构建。
- `minSupportedVersionCode` 不得大于 `latest.versionCode`。
- `publishedAt` 必须是 RFC3339 时间。

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
- 元数据可能返回 `locationLatitude=0`/`locationLongitude=0`（教师未设置圆心）。服务端把 `(0,0)` 视为缺失，并依次尝试 `location`（对象或 JSON 字符串）、`address`/`locationInfo` 对象及顶层 `latitude`/`longitude`；只接受合法且非 `(0,0)` 的坐标。
- 位置签到（`sign_type=4`）在未提供 `special_params.latitude/longitude` 且没有可用圆心/半径（包括元数据读取失败）时，直接返回 `location_point_required`，且不会请求 `preSign`/`stuSignajax`；前端应提示「请先在地图选点」。已提供合法经纬度时保持原行为：按原值提交（元数据有圆心则先做范围校验）。非位置类型在元数据缺失时保持原降级行为。

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
| `face_required` | 人脸验证未通过 | `false` | 服务端已自动做人脸恢复重试一次 |
| `face_photo_unavailable` | 无人像且无法取得可用基线照片 | `false` | 照片池为空且自动拉取官方照片失败（或基线无法解码）；其它签到类型不受影响 |
| `location_out_of_range` | 位置超出范围 | `false` | 预设点校验或上游返回距离时带 `distance_m`（米）；服务端已关闭随机化重试一次 |
| `location_point_required` | 位置签到缺少可用圆心且未选点 | `false` | 活动元数据圆心为 `(0,0)`/缺失或读取失败；不提交上游，前端提示「请先在地图选点」 |
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

### 5.5 签退
- Method: `POST`
- Path: `/api/sign/sign-out`
- Auth: 是

签退依赖主签到：`activity_id` 传**主签到活动**，服务端从 `getPPTActiveInfo` 读取 `signOutId` 与 `signOutPublishTimeStamp`，再用 `signOutId` 对应的签退活动执行 `preSign` 与 `stuSignajax`。

请求体：

```json
{
  "activity_id": 987654,
  "target_uid": 10001,
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

`target_uid` 缺省为当前登录用户；`special_params` 与 `/api/sign/execute` 一致（二维码 `enc`、手势/签到码 `sign_code`、位置经纬度）。

响应体 `data`:

```json
{
  "user_id": 10001,
  "success": true,
  "already_signed_out": false,
  "sign_out_id": 987655,
  "sign_out_time_ms": 1760000500000,
  "message": "签退成功",
  "error_code": "",
  "retryable": false
}
```

签退专属 `error_code`：

| error_code | 含义 | `retryable` | 备注 |
|---|---|---|---|
| `sign_in_required` | 主签到未完成 | `false` | 本地无签到记录且上游 `preSign` 未返回已签；不会请求签退接口 |
| `sign_out_unavailable` | 该签到未发布签退活动 | `false` | 元数据无 `signOutId` |
| `sign_out_not_published` | 签退尚未到发布时间 | `false` | `signOutPublishTimeStamp` 晚于当前时间 |

其余 `error_code`（`captcha_required`、`face_required`、`location_out_of_range`、`location_point_required`、`qr_expired`、`activity_expired`、`session_expired`、`upstream_error`）与 `/api/sign/execute` 相同。签退成功写入独立的 `sign_out_records`（`user_uid + 主签到 activity_id` 唯一），不改写 `sign_records`；重复签退返回 `already_signed_out=true`，且不再请求上游。分享链接不支持签退：为签退活动创建分享或执行这类分享都返回 `400`「分享链接不支持签退活动」。

### 5.6 签到分享接口

签到分享用于生成免登录链接。分享页不会返回或展示账号列表；执行目标由后端按分享者在当前课程班级下可代签范围动态计算。

#### 5.6.1 创建分享链接
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

#### 5.6.2 获取分享活动信息
- Method: `GET`
- Path: `/api/sign/shares/:token`
- Auth: 否

响应体 `data` 只包含活动和签到类型信息，不包含账号信息。

#### 5.6.3 执行分享签到
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
- 签退活动不支持分享：为签退活动创建分享或执行这类分享链接都会返回 `400`「分享链接不支持签退活动」，不会静默按签到处理。
### 5.7 拍照签到
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

### 5.8 人脸照片接口

人脸照片用于需要人脸验证的签到活动。每个账号维护最多 5 张的受管照片池（`face_photos`）。池内存放**基线图**：手动上传的图会先压缩到 720×1280 以内；官方导入的图按下载原样保存。每次签到执行时（`if_face=true`）才对当次选中的基线做一次裁剪/旋转/重压，变换结果只用于本次上传，不写库。账号没有任何照片时，签发流程会自动拉取一次官方照片作为基线。

所有接口接受可选查询参数 `target_uid`：省略时操作自己的照片池；指定他人时要求双方存在同为「已选」的课程（与代签权限一致），否则返回 `403`。

#### 5.8.1 查询照片池
- Method: `GET`
- Path: `/api/user/faces`
- Auth: 是

```json
{
  "user_uid": 10001,
  "max": 5,
  "photos": [
    {
      "id": 3,
      "user_uid": 10001,
      "object_id": "face_temp-object-id",
      "use_count": 4,
      "is_failure_before": false,
      "source": "official",
      "created_at": "2026-10-20T10:00:00Z",
      "image": "data:image/jpeg;base64,..."
    }
  ]
}
```

`source` 为 `upload`（手动上传）或 `official`（从学习通导入）。`is_failure_before=true` 表示该照片曾在人脸校验中被拒绝；存在失败历史时优先选择「未失败且用过」的照片。

#### 5.8.2 上传照片
- Method: `POST`
- Path: `/api/user/faces`
- Auth: 是
- Content-Type: `multipart/form-data`

字段：`file`（必填，图片，最大 20MB）。第 6 张返回 `409`。

#### 5.8.3 删除照片
- Method: `DELETE`
- Path: `/api/user/faces/{id}`
- Auth: 是

返回 `404` 表示照片不存在或无权访问。

#### 5.8.4 导入学习通官方照片
- Method: `POST`
- Path: `/api/user/faces/import`
- Auth: 是

使用目标账号自己的会话调用 `collectionfilephotoEnc` 获取官方照片，并把下载到的原图**原样保存为基线**（`source=official`，上限 8MB 且需可解码）。签到执行时才在内存中做裁剪 90%–99%、旋转 ±5°、去黑边、JPEG 重压（默认质量 85，超 100KB 依次降到 80/70 并可按比例缩小）并丢弃 EXIF；变换结果不落库。成功响应额外包含 `transform_deferred: true`，不再返回 `transform` 字段（前端可忽略该字段）。

| 状态码 | 含义 |
|---|---|
| `422` | 账号未设置官方人脸照片、原图无法解码或超过 8MB |
| `409` | 照片池已满 |
| `403` | 无权管理该账号 |
| `502` | 上游请求失败 |

#### 5.8.5 兼容接口（旧客户端）
- `GET /api/user/face`：`status_only=1` 返回 `has_face/count`；否则额外返回最新一张的 `face_data`。
- `POST /api/user/face`：等价于向自己的池新增一张。
- `DELETE /api/user/face`：清空自己的照片池。

说明：签到时若活动 `if_face=true`，后端会为目标账号选择一张基线照片，在本次执行内变换一次后上传换取 `objectId`，并调用 `check-face-result` 获取 `currentFaceId/faceEnc/faceCode`，再提交签到参数。同一次执行内的重试复用同一份变换结果与 `objectId`；不同执行会重新变换并上传。账号没有任何照片时，后端会用该账号凭据自动拉取一次官方照片作为基线；失败返回 `face_photo_unavailable`，不影响其它签到类型。

### 5.9 功能开关
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

### 5.10 群聊签到按需抓取状态
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
- `429` 签到提交超过限流阈值
- `500` 服务端错误

---

## 8. 联调建议

1. 先调用 `/api/auth/login` 获取 JWT。
2. 带 `Authorization: Bearer <JWT>` 调用 `/api/courses/sync`。
3. 调用 `/api/courses` + `/api/courses/selection` 选定课程。
4. 调用 `/api/sign/activities` 拿活动。
5. 调用 `/api/sign/check` 查待签状态。
6. 前端过滤出未签用户后，并发调用 `/api/sign/execute`。

---

## 9. 签到观测（管理员）

### 9.1 结构化结果日志

每次签到提交（`/api/sign/execute`、分享链接执行、拍照签到最终提交）恰好写一行标准日志：

```text
sign_execute {"activity_id":987654,"user_uid":10001,"sign_type":2,"result_code":"success","attempt":1,"upstream_status":200,"duration_ms":842,"success":true,"already_signed":false,"validate_triggered":false,"captcha_passed":false,"location_error":false,"upstream_403":false,"has_cxcid":false}
```

- `attempt`：本次执行向 `stuSignajax` 发起的提交次数（验证码/位置/人脸恢复会累加）。
- `upstream_status`：最后一次提交的 HTTP 状态码；未到达上游时为 `0`。
- `has_cxcid`：人脸请求是否携带 S13 设备签名；只记录布尔值，永不记录 `clientId`、`sc`、token、照片或密码。
- 参数校验失败、限流等未进入上游的提交也会写一行，`result_code` 为 `invalid_request`、`unsupported_sign_type`、`rate_limited`。

### 9.2 指标
- Method: `GET`
- Path: `/api/admin/sign-metrics`
- Auth: 管理员

响应 `data`：

```json
{
  "total": 120,
  "success": 96,
  "already_signed": 4,
  "success_rate": 0.8,
  "result_codes": {"success": 96, "captcha_required": 9, "location_out_of_range": 3, "upstream_error": 12},
  "validate_triggered": 15,
  "validate_trigger_rate": 0.125,
  "captcha_passed": 9,
  "captcha_pass_rate": 0.6,
  "location_errors": 3,
  "error_location_rate": 0.025,
  "upstream_403": 2,
  "upstream_403_rate": 0.0167,
  "p95_duration_ms": 2140,
  "latency_samples": 120,
  "im_fetch_requests": 18,
  "im_fetch_skipped_stale": 102,
  "im_fetch_timeout": 1,
  "im_fetch_error_codes": {"im_timeout": 1, "im_not_configured": 2},
  "im_fetch_latency_p95_ms": 3120,
  "im_fetch_latency_samples": 18,
  "im_accounts": {
    "10001": {
      "im_fetch_requests": 12,
      "im_fetch_skipped_stale": 80,
      "im_fetch_timeout": 1,
      "im_fetch_error_codes": {"im_timeout": 1},
      "im_fetch_error_code": "im_timeout",
      "im_fetch_latency_ms_p95": 3120,
      "im_fetch_latency_samples": 12
    }
  },
  "im_accounts_truncated": false,
  "activities_p95_ms": 3450,
  "activities_samples": 120
}
```

指标为进程内累计值，重启清零；p95 取最近 2048 个样本。`im_accounts` 按账号保留同上计数（最多 512 个账号，超出时置 `im_accounts_truncated=true`），用于判断单账号的 5s 超时是否合理。`activities_p95_ms` 覆盖整个 `/api/sign/activities` 处理时间，**包含**同步 IM 抓取。

### 9.3 限流
- 仅作用于签到提交（`/api/sign/execute`、`/api/sign/photo`、分享链接执行），使用进程内令牌桶 + 并发上限；单账号提交按目标 uid 计数，分享批量执行按分享令牌计数。
- 超限返回 HTTP `429`，`Retry-After` 为建议等待秒数，响应 `message` 为「签到请求过于频繁，请稍后再试」，并记一条 `result_code=rate_limited` 的结果日志。
- 配置项：`sign_rate_limit_per_minute`（默认 30）、`sign_rate_limit_burst`（默认 10）、`sign_max_concurrent`（默认 2）。非正值回退默认值。
- IM 抓取不参与该限流；IM 失败只降级为无群聊活动，不返回 5xx。

### 9.4 滑块验证码模型
- 配置项：`captcha_model_path`（默认 `./assets/captcha.ort`）、`captcha_model_enabled`（默认 `true`）。模型文件缺失、ONNX Runtime 加载失败或推理失败时自动回退内置启发式，不阻断签到。
- 日志字段：`captcha_model=model|fallback`（模型加载成功/降级）、`captcha attempt ... captcha_model=... challenge_id=... candidate_count=... top1_x=... upstream_result=...`。
- 模型可用时只提交模型给出的 x；不可用时仍按启发式排序后提交 top-1。上游对同一 token 只接受一次提交。
- 部署要求（cgo + `libonnxruntime` 1.29.0 + `CAPTCHA_ORT_LIB_PATH`）见 `DEPLOY.md` §3.1。
