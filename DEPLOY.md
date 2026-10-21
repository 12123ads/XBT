# 部署文档（Docker + Caddy）

> 适用于 Debian/Ubuntu 服务器，一条链路部署 `db + server + web`，并通过 Caddy 提供 HTTPS 域名访问。

## 1. 环境准备

```bash
sudo apt update
sudo apt install -y git docker.io docker-compose-plugin caddy ufw
sudo systemctl enable --now docker caddy
```

---

## 2. 拉取项目

```bash
cd /opt
sudo git clone https://github.com/EnderWolf006/XBT.git xbt
cd /opt/xbt
```

---

## 3. 后端配置

复制并编辑配置（示例）：

```bash
cp Server/config_example.yaml Server/config.yaml
nano Server/config.yaml
```

建议关键项：

- `allow_insecure_tls: false`
- `jwt_secret`、`credential_secret` 换成随机高强度值
- `postgres_dsn` 保持与 compose 一致（默认 `host=db`）
- `captcha_model_enabled: true`（默认；文件缺失时自动回退启发式）
- `captcha_model_path: "./assets/captcha.ort"`

### 3.1 验证码模型与 ONNX Runtime

滑块识别使用 `Server/assets/captcha.ort`（YOLO11n，来自 ChaoxingSignFaker，AGPLv3，见 `NOTICE`）。Go 绑定为 `github.com/yalue/onnxruntime_go`，因此：

- **必须使用 cgo 构建**：`CGO_ENABLED=1`，构建机需要 C 编译器。
- **运行时需要共享库**：`libonnxruntime.so` 版本必须与绑定头文件一致（当前 `1.29.0`）。程序通过 `CAPTCHA_ORT_LIB_PATH` 指定路径；未设置时按 `libonnxruntime.so` 由 `LD_LIBRARY_PATH` / `ldconfig` 解析。
- 官方镜像已在构建阶段下载对应架构的 ONNX Runtime 到 `/usr/local/lib`，并设置：
  - `LD_LIBRARY_PATH=/usr/local/lib`
  - `CAPTCHA_ORT_LIB_PATH=/usr/local/lib/libonnxruntime.so`

非 Docker 部署（Linux）：

```bash
# 以 arm64 为例；amd64 使用 onnxruntime-linux-x64-1.29.0.tgz
curl -fsSL -o /tmp/ort.tgz \
  https://github.com/microsoft/onnxruntime/releases/download/v1.29.0/onnxruntime-linux-aarch64-1.29.0.tgz
sudo tar -xzf /tmp/ort.tgz -C /opt
sudo cp -a /opt/onnxruntime-linux-aarch64-1.29.0/lib/. /usr/local/lib/
sudo ldconfig

cd Server
CGO_ENABLED=1 go build -o xbt-server ./cmd/server
CAPTCHA_ORT_LIB_PATH=/usr/local/lib/libonnxruntime.so ./xbt-server
```

降级行为：模型文件缺失、运行时加载失败或推理失败时，日志出现
`captcha_model=fallback`，本次验证码改用内置启发式，签到流程不中断。
模型可用时日志为 `captcha_model=model`，`captcha attempt` 行也会带该字段。

验证模型链路（需共享库）：

```bash
cd Server
CAPTCHA_ORT_LIB_PATH=/usr/local/lib/libonnxruntime.so \
CAPTCHA_TEST_REQUIRE_MODEL=1 go test ./internal/captcha -v
```

> 共享库缺失时普通 `go test ./...` 会跳过模型用例；CI/发布验证应设置
> `CAPTCHA_TEST_REQUIRE_MODEL=1`，让缺失直接失败。

---

快速生成随机密钥：

```bash
python3 - <<'PY'
import secrets
print(secrets.token_hex(32))
print(secrets.token_hex(32))
PY
```

---

## 4. 启动服务

```bash
cd /opt/xbt
docker compose up -d --build
```

地图选点的高德 key 通过仓库根目录 `.env` 注入构建，首次部署先执行 `cp .env.example .env` 并填写，详见 §13.2。

查看状态：

```bash
docker compose ps
```

健康检查：

```bash
curl http://127.0.0.1:3030/api/health
curl -I http://127.0.0.1:5173
```

---

## 5. 配置 Caddy 反向代理（HTTPS）

编辑 `/etc/caddy/Caddyfile`，新增：

```caddy
your-domain.example {
    reverse_proxy 127.0.0.1:5173
}
```

重载配置：

```bash
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

验证：

```bash
curl -I https://your-domain.example
curl https://your-domain.example/api/health
```

---

## 6. 防火墙建议

推荐只开放 80/443：

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
```

如已开放 5173/3030，建议关闭公网暴露：

```bash
sudo ufw delete allow 5173/tcp
sudo ufw delete allow 3030/tcp
```

---

## 7. 首次登录与白名单

- 白名单表为空时，首次登录手机号会被自动写入并赋予管理员权限。
- 之后不在白名单的手机号会返回 `403 账号未授权`。

查看白名单：

```bash
docker compose exec -T db psql -U xbt -d xbt -c "select id,mobile,permission,created_at from whitelists order by id;"
```

---

## 8. 常用运维命令

```bash
# 状态
docker compose ps

# 日志
docker compose logs -f server
docker compose logs -f web
docker compose logs -f db

# 重启
docker compose restart

# 停止
docker compose down
```

---

## 9. 常见问题排查

### 9.1 登录失败且日志有 x509 证书错误

症状（示例）：

- `tls: failed to verify certificate: x509: certificate signed by unknown authority`

原因：后端运行镜像缺少 CA 证书。

修复：已在 `Server/Dockerfile` 中加入 `ca-certificates` 安装；重建后端镜像即可。

```bash
docker compose build server
docker compose up -d server
```

### 9.2 Web 构建 npm 异常（`Exit handler never called!`）

已采用更稳的构建方式：

- `node:20-alpine`
- `npm ci --include=dev`
- 构建阶段 `NODE_ENV=development`

如果仍失败：

```bash
docker compose build --no-cache web
```

### 9.3 `tsc: not found`

原因：未安装 devDependencies。

修复：确保 Dockerfile 使用 `npm ci --include=dev`，不要省略 dev 依赖。

### 9.5 登录接口 502 / `connect() failed (111: Connection refused)`

典型日志：

- `POST /api/auth/login` 返回 `502`
- `connect() failed (111: Connection refused) while connecting to upstream`

高概率原因：`server` 容器启动失败，常见是后端数据库连接配置错误。

重点检查 `Server/config.yaml`：

```yaml
postgres_dsn: "host=db port=5432 user=xbt password=xbt dbname=xbt sslmode=disable TimeZone=Asia/Shanghai"
```

不要把 `host` 写成 `example.com` 或其他外部地址（除非你确实在用外部 PostgreSQL）。

> 注意：`Server/Dockerfile` 中有 `COPY config.yaml /app/config.yaml`，配置会被打进镜像。
> 修改 `Server/config.yaml` 后必须重建后端镜像，否则容器仍使用旧配置。

修复步骤：

```bash
docker compose build --no-cache server
docker compose up -d server web
```

验证：

```bash
docker compose exec web wget -qO- http://server:3030/api/health
```

---

## 10. 当前项目内已落地的 Docker 修复

- `Server/Dockerfile`
  - 安装 `ca-certificates`
  - 默认按构建环境生成可执行文件（不强制写死 `GOARCH=arm64`，避免在 x86 主机出现 `exec format error`）
- `Web/Dockerfile`
  - 切到 `node:20-alpine`
  - `npm ci --include=dev`
  - 规避 npm 在特定环境下的异常退出问题

---

## 11. Android APK 中 Web 地址修改位置

Web 前端已经打包进 APK 的 `assets/web/`，启动时由 WebView 直接加载本地资源，不再依赖远端页面。
`target_url` 同时作为 WebView 的页面 origin 和 API 同源地址（前端默认请求 `/api`）。

修改字段：

- `Android/app/src/main/res/values/strings.xml`（本地文件，已 gitignore）
- 模板：`Android/strings_example.xml`

```xml
<string name="target_url">https://your-domain.example/</string>
```

首次构建：

```bash
cp Android/strings_example.xml Android/app/src/main/res/values/strings.xml
```

重新构建 APK：

```bash
./Android/build-apk.sh
# 或者
cd Android
./gradlew assembleDebug
```

`build-apk.sh` 会先构建 Web 并同步到 `Android/app/src/main/assets/web/`，再编译 APK，产物在 `dist/xbt-debug.apk`。
如果机器上没有 `npm`，脚本会直接使用仓库中已提交的 Web 构建产物。

### 11.1 生产签名 APK

```bash
./Android/build-release.sh
```

脚本会执行 R8 压缩、资源收缩、zipalign，并使用发布证书签名，产物在 `dist/xbt-release.apk`。

发布证书默认读取：

- `$HOME/.xbt-keystore/xbt-release.jks`
- `$HOME/.xbt-keystore/keystore.properties`（包含 `STORE_PASS` / `KEY_PASS`）

这两个文件**不要提交到仓库**。证书一旦更换，已安装的旧版本将无法覆盖升级。

> 前端 API 基地址是 `/api`，所以 `target_url` 必须指向同时提供页面和 API 的同一站点。
> 如果 API 部署在另一个域名，需要额外给后端配置 CORS。

### 11.2 应用内更新与 APK 发布

更新接口是公开的，客户端登出状态也能检查更新：

- `GET /api/app/version?package=com.github.enderwolf006.xbt&versionCode=3&abi=arm64-v8a`
- `GET /api/app/download/<file>.apk`

接口契约见 `Server/API.md` §2.2。只支持 HTTPS，`apkUrl` 永远输出 `https://`。

发布步骤：

1. 修改 `Android/app/build.gradle.kts` 的 `versionCode`（必须大于已发布的版本）和 `versionName`。
2. 执行 `./Android/build-release.sh`。脚本会读取 `Server/releases/releases.json` 作为基线，若 `versionCode` 未增加会直接失败；成功后打印 `sha256` 与 `sizeBytes`。
3. 把 APK 复制为版本化文件名并放入发布目录：

   ```bash
   cp dist/xbt-release.apk Server/releases/xbt-2.0.2.apk
   sha256sum Server/releases/xbt-2.0.2.apk
   stat -c %s Server/releases/xbt-2.0.2.apk
   ```

4. 按 `Server/releases/releases.json.example` 创建 `Server/releases/releases.json`，填入新的 `versionCode`、`versionName`、`apkFile`、`publishedAt`、`changelog`。`sha256` / `sizeBytes` 可省略（服务启动时计算），也可以填入第 3 步的结果用于校验。
5. 重启后端，使清单生效：`docker compose restart server`（或重启 `xbt-server` 进程）。

后端配置（`Server/config.yaml`）：

```yaml
app_release_manifest: "./releases/releases.json"
# 对外 HTTPS 根地址；留空则使用请求 Host 并强制 https。
public_base_url: "https://xbt.example.com"
```

Docker 部署时 `docker-compose.yml` 已把 `./Server/releases` 只读挂载到容器 `/app/releases`，所以改清单/换 APK 不需要重建镜像，但**必须重启 server 容器**。`Server/.gitignore` 已忽略 `releases/*.apk`，不要把安装包提交到仓库。

验证：

```bash
curl -sS "https://xbt.example.com/api/app/version?package=com.github.enderwolf006.xbt&versionCode=3&abi=arm64-v8a"
# 返回 latest.versionCode / mandatory；versionCode 等于最新时返回 204

curl -sS -r 0-99 -o /dev/null -w '%{http_code}\n' \
  "https://xbt.example.com/api/app/download/xbt-2.0.2.apk"
# 期望 206

sha256sum Server/releases/xbt-2.0.2.apk
# 与 /api/app/version 返回的 sha256 一致
```

清单缺失或校验失败时，只有 `/api/app/*` 返回 `503`，其它接口正常；启动日志会写明 `app_release status=unavailable manifest=... error=...`。不要把测试包、内部包放进发布目录：下载接口只暴露当前 `latest.apkFile`，但目录本身仍应只存放正式安装包。

---

## 12. 修改“位置签到”地址（地点预设）

位置签到可选地址在前端配置文件中维护：

- `Web/config.yaml`
- 字段：`sign.location_presets`

每条地点包含：

- `name`：地点名
- `lng`：经度，使用百度 `BD-09` 坐标系
- `lat`：纬度，使用百度 `BD-09` 坐标系
- `description`：地址描述（提交给签到接口）

注意：位置预设统一使用百度 `BD-09` 坐标系，配置顺序是 `lng` 经度在前、`lat` 纬度在后。若坐标来自高德/腾讯地图（`GCJ-02`）或浏览器 GPS（`WGS-84`），需要先转换为 `BD-09` 后再写入配置；页面上的浏览器定位会在前端把 `WGS-84` 离线转换为 `BD-09` 后提交。

示例：

```yaml
sign:
  location_presets:
    - name: "教学楼A"
      lng: "104.123456"
      lat: "30.123456"
      description: "某市某区某路XX号 教学楼A"
```

修改后需要重建并重启 `web` 容器（前端构建产物才会更新）：

```bash
cd /opt/xbt
docker compose build --no-cache web
docker compose up -d web
```


---

## 13. 位置签到地图选点（高德 JS API）

位置签到的“地图选点”使用高德 JS API 2.0，只在用户首次打开地图弹层时懒加载；未配置 key、SDK 加载失败或瓦片超时时，弹层内显示错误和“重试”，浏览器定位、预设地点和签到主流程仍然可用。

### 13.1 申请 key 与配置域名白名单

1. 在高德开放平台创建“Web端（JS API）”类型的 key，并生成安全密钥（securityJsCode）。
2. 在高德控制台给 key 配置域名白名单。白名单填页面实际 origin 的域名（不带路径和协议由控制台决定）：
   - Web 同源部署：`119.29.139.161:8081`（以实际部署域名为准）。
   - Android：WebView 的页面 URL 来自 `Android/app/src/main/res/values/strings.xml` 的 `target_url`（当前为 `https://119.29.139.161:8081/`）。页面资源虽然由 APK 本地拦截，但页面 origin 仍是 `target_url`，因此白名单填该域名/端口；不需要填 `file://`、`localhost` 或 `127.0.0.1`。
3. key 与安全密钥会进入前端 bundle，属于公开标识，不是服务端 secret；仍需依赖高德控制台的域名白名单和配额限制。

### 13.2 注入构建变量

前端只读取以下构建变量，不要在源码中写死：

- `VITE_AMAP_KEY`：高德 JS API key。
- `VITE_AMAP_SECURITY_CODE`：高德安全密钥（securityJsCode）。

本地开发：

```bash
cp Web/.env.example Web/.env.local
# 编辑 Web/.env.local，填入 VITE_AMAP_KEY / VITE_AMAP_SECURITY_CODE
cd Web && npm run dev
```

Docker（两种方式等价，均通过 compose 的 `web.build.args` 注入；shell 环境变量优先级高于根 `.env`）：

方式一：仓库根目录 `.env`（推荐，`docker compose up -d --build` 自动读取）：

```bash
cd /opt/xbt
cp .env.example .env    # 首次
# 编辑 .env，填入 VITE_AMAP_KEY / VITE_AMAP_SECURITY_CODE
docker compose up -d --build web
```

方式二：`--build-arg`（适合临时或 CI 的一次性构建）：

```bash
cd /opt/xbt
docker compose build web \
  --build-arg VITE_AMAP_KEY='your-key' \
  --build-arg VITE_AMAP_SECURITY_CODE='your-security-code'
docker compose up -d web
```

两种方式不要同时用不同值：两次构建会产出不同 bundle。发布时固定一种来源。

Android APK（`build-apk.sh` 会先执行 `npm run build`，环境变量会传入构建）：

```bash
export VITE_AMAP_KEY='your-key'
export VITE_AMAP_SECURITY_CODE='your-security-code'
./Android/build-apk.sh
```

真实 key 不要提交到仓库：根 `.env` 和 `Web/.env.local` 均已被 `.gitignore` 忽略。`Web/.env` 已不再被跟踪，仅保留 `Web/.env.example` 作为模板；Docker 构建上下文也排除了 `Web/.env*`，镜像内的值只来自 compose 的 build args。

### 13.3 验证要点

- 首次进入签到页时 Network 中没有 `webapi.amap.com`；打开“地图选点”后才出现地图 SDK 请求。
- 地图选点提交的 `special_params.latitude` / `longitude` 为 BD-09、6 位小数；`description` 为逆地理编码地址，失败时为 `地图选点 {lng},{lat}`。
- 移空 `VITE_AMAP_KEY` 重新构建后，地图区域显示“未配置地图 key（VITE_AMAP_KEY）”，预设地点和浏览器定位仍可完成签到。
