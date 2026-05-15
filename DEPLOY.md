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
xbt.rxzh.cc {
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
curl -I https://xbt.rxzh.cc
curl https://xbt.rxzh.cc/api/health
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

Android 壳内加载的前端地址在以下文件：

- `Android/app/src/main/res/values/strings.xml`

修改字段：

```xml
<string name="target_url">https://xbt.rxzh.cc/</string>
```

如果项目里还没有 `strings.xml`（只有 `strings_example.xml`），先复制：

```bash
cp Android/app/src/main/res/values/strings_example.xml Android/app/src/main/res/values/strings.xml
```

> 复制后请将 `strings_example.xml` 移出 `res/values`（或删除），否则会出现 `Duplicate resources`。

```bash
mv Android/app/src/main/res/values/strings_example.xml Android/strings_example.xml.bak
# 或者：rm -f Android/app/src/main/res/values/strings_example.xml
```

修改后重新构建 APK：

```bash
cd Android
./gradlew assembleDebug
```

---

## 12. 修改“位置签到”地址（地点预设）

位置签到可选地址在前端配置文件中维护：

- `Web/config.yaml`
- 字段：`sign.location_presets`

每条地点包含：

- `name`：地点名
- `lng`：经度
- `lat`：纬度
- `description`：地址描述（提交给签到接口）

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

