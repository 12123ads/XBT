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
        "start_time": 1760000000000,
        "end_time": 1760003600000,
        "sign_type": 2,
        "if_refresh_ewm": false,
        "record_source": 0,
        "record_source_name": "",
        "record_sign_time": 1760000500000,
        "course_name": "高等数学",
        "course_id": 222,
        "class_id": 111,
        "course_teacher": "李老师"
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

说明：
- 每门课程默认最多返回最新 `5` 条签到活动（可通过后端 `Server/config.yaml` 中的 `activity_list_limit` 配置）。
- 当该课程活动总数超过返回条数时，`has_more=true`。

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

响应体 `data`:

```json
{
  "items": [
    {
      "user_id": 343479151,
      "signed": true,
      "record_source": 343479151,
      "record_source_name": "张三",
      "message": "该同学已本人签到"
    },
    {
      "user_id": 10001,
      "signed": false,
      "record_source": 0,
      "record_source_name": "",
      "message": "未签到"
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
  "special_params": {
    "enc": "xxxx",
    "location": {
      "result": 1,
      "address": "成都市郫都区xxx",
      "latitude": 30.7501,
      "longitude": 103.9272
    }
  }
}
```

兼容说明：
- 若未传 `target_uid`，但传了 `user_ids`，后端会使用 `user_ids` 的第一个 uid。
- 若都未传，默认签当前登录用户。

响应体 `data`:

```json
{
  "user_id": 10001,
  "success": true,
  "already_signed": false,
  "record_source": 343479151,
  "record_source_name": "张三",
  "message": "签到成功"
}
```

`special_params` 按签到类型：
- 普通签到(`0`): 可空
- 二维码签到(`2`):
  - `enc` (required)
  - `c` (optional, 预签到场景可用)
  - `location` (optional, 二维码附加位置变种；可传对象/数组或 JSON 字符串，后端会透传给学习通)
  - `latitude` + `longitude` + `description` (optional, 兼容写法；后端会自动组装为 `location` 后透传)
- 手势签到(`3`):
  - `sign_code` (required)
- 位置签到(`4`):
  - `latitude` (required)
  - `longitude` (required)
  - `description` (required)
  - 位置预设约定使用百度 `BD-09` 坐标系；字段格式仍为十进制度字符串。
  - 浏览器定位来源为 `WGS-84`，前端会离线转换为百度 `BD-09` 后提交。
- 签到码签到(`5`):
  - `sign_code` (required)

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

### 6.8 QMX 自动签到（管理员）

QMX 自动签到用于每天北京时间 `22:00` 自动执行查寝定位签到。它独立于学习通课程签到记录，不写入 `sign_records`。

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

#### 6.8.3 读取账号 QMX 定位点
- Method: `POST`
- Path: `/api/admin/qmx-auto-sign/accounts/:uid/locations/preview`
- Auth: 是（管理员）

说明：后端使用该账号保存的学习通凭据换取 QMX 凭据，并返回当前查寝批次和允许定位点。

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

说明：开启单账号自动签到前必须已选择定位点。定时执行时优先按 `location_name` 匹配当前 QMX 定位点，找不到再按 `location_index` 兜底。

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

记录包含账号、触发方式、成功状态、QMX 返回码/消息、批次、定位点和执行时间。

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
