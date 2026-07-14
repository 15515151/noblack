# noblack

一个使用 Go 编写的轻量级文本关键词匹配服务。项目基于 Aho-Corasick 自动机，提供关键词扫描、分类标签、备注信息、在线词库管理、运行统计与热更新能力，并内置了可直接使用的 Web 控制台。

适合用于内容分类、文本标注、规则路由、合规提示、客服质检等需要按词库快速识别文本片段的场景。

## 功能概览

- **高效匹配**：使用 Aho-Corasick 自动机一次扫描多个关键词。
- **Unicode 支持**：按 `rune` 处理文本，支持中文、英文、Emoji 等内容，并返回准确的字符区间。
- **灵活元数据**：每个词条可配置多个分类标签与多条备注。
- **批量词条**：一个配置项可通过中英文逗号声明多个关键词，共享同一组标签和备注。
- **在线管理**：通过 Web 控制台或 HTTP API 查询、新增、修改和删除词条。
- **动态更新**：支持监听词库文件自动重载，也可通过 API 手动触发重载。
- **并发友好**：新自动机在后台构建完成后通过 `atomic.Value` 发布，查询路径无需等待重建过程。
- **运行统计**：记录请求量、匹配量和高频命中项，可选定期持久化。
- **访问控制**：可为词库写操作配置访问令牌。
- **便于部署**：支持本地运行、Docker 和 Docker Compose，无需单独部署前端。

完整接口定义及响应字段说明请参阅 [API.md](./API.md)。

## 快速开始

### 本地运行

环境要求：Go 1.21 或更高版本。

```bash
go mod download
go run ./cmd/server
```

服务默认监听 `:8080`，词库默认读取当前目录下的 `words.json`。

启动后可访问：

- Web 控制台：`http://localhost:8080`
- 健康检查：`http://localhost:8080/health`

常用启动方式：

```bash
# 指定监听地址和词库文件
go run ./cmd/server -addr :8080 -words ./words.json

# 启用英文大小写忽略匹配
go run ./cmd/server -ci

# 持久化运行统计
go run ./cmd/server -stats-file ./stats.json

# 为词库写操作启用访问令牌
go run ./cmd/server -token "your-secret-token"

# 关闭文件监听，仅通过 API 手动重载
go run ./cmd/server -watch=false
```

### Docker Compose

```bash
docker compose up -d --build
```

默认配置会将宿主机的 `./data` 挂载到容器 `/data`：

- `./data/words.json`：词库文件
- `./data/stats.json`：统计数据

容器和服务进程均以 root 身份运行，避免宿主机绑定挂载目录的 UID/GID 与容器用户不一致，导致无法创建临时文件、更新词库或持久化统计。

首次启动且 `./data/words.json` 不存在时，容器会自动复制一份默认词库。更新配置后需要重新构建并创建容器：

```bash
docker compose down
docker compose up -d --build --force-recreate
```

### Docker

```bash
docker build -t noblack:latest .

mkdir -p ./data
docker run -d \
  --name noblack \
  -p 8080:8080 \
  -v "$(pwd)/data:/data" \
  noblack:latest
```

## 词库格式

默认词库采用 JSON 格式：

```json
{
  "words": [
    {
      "word": "售后服务",
      "levels": ["customer-service"],
      "remarks": ["转交售后流程"]
    },
    {
      "word": "退款,退货",
      "levels": ["after-sales", "priority"],
      "remarks": ["需要人工复核"]
    },
    {
      "word": "Example",
      "level": "demo",
      "remarks": "英文示例"
    }
  ]
}
```

字段说明：

| 字段 | 类型 | 说明 |
|------|------|------|
| `word` | string | 待匹配的关键词；可用中文或英文逗号分隔多个词 |
| `levels` | string[] | 推荐写法，一个词条可配置多个分类标签 |
| `level` | string | 兼容单标签写法；与 `levels` 同时存在时优先使用 `levels` |
| `remarks` | string[] / string | 备注列表，也可写成逗号分隔的字符串 |

补充规则：

- 未提供 `level` 或 `levels` 时，使用 `-default-level` 指定的默认值。
- 顶层既可以使用 `{ "words": [...] }`，也可以直接使用数组 `[...]`。
- 逗号分隔的多个关键词会分别参与匹配和统计，但共享标签与备注。
- 保存词库时会清理空白项，并以规范 JSON 格式写回文件。

## HTTP API

主要接口如下：

| 分类 | 方法与路径 | 说明 |
|------|------------|------|
| 文本匹配 | `POST /check` | 扫描文本并返回命中位置及元数据 |
| 词库管理 | `GET /words` | 获取词条列表 |
| 词库管理 | `POST /words` | 新增词条 |
| 词库管理 | `PUT /words/{word}` | 修改词条 |
| 词库管理 | `DELETE /words/{word}` | 删除词条 |
| 配置查询 | `GET /levels` | 获取当前词库中的全部标签 |
| 动态更新 | `POST /reload` | 从文件重新加载词库 |
| 运行统计 | `GET /stats` | 获取统计信息 |
| 运行统计 | `POST /stats/reset` | 重置统计信息 |
| 鉴权 | `GET /auth/status` | 查询是否启用写操作鉴权 |
| 鉴权 | `POST /auth/verify` | 校验访问令牌 |
| 运维 | `GET /health` | 健康检查 |

示例：

```bash
# 扫描文本
curl -X POST http://localhost:8080/check \
  -H 'Content-Type: application/json' \
  -d '{"text":"我需要申请退款并联系售后服务"}'

# 新增词条
curl -X POST http://localhost:8080/words \
  -H 'Content-Type: application/json' \
  -H 'X-Auth-Token: your-secret-token' \
  -d '{"word":"物流查询","levels":["customer-service"],"remarks":["转交物流流程"]}'

# 查看统计
curl "http://localhost:8080/stats?top=10"
```

写操作令牌同时支持以下两种请求头：

```text
X-Auth-Token: your-secret-token
Authorization: Bearer your-secret-token
```

更多请求、响应和错误码示例见 [API.md](./API.md)。

## 配置项

### 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-addr` | `:8080` | HTTP 监听地址 |
| `-words` | `./words.json` | 词库文件路径 |
| `-watch` | `true` | 是否监听词库文件并自动重载 |
| `-ci` | `false` | 是否忽略英文大小写 |
| `-default-level` | `Low` | 词条未配置标签时使用的默认值 |
| `-stats-file` | 空 | 统计持久化文件；为空时不持久化 |
| `-stats-flush-interval` | `30s` | 统计数据定期写入文件的间隔 |
| `-token` | 空 | 词库写操作令牌；为空时不启用鉴权 |

### Docker 环境变量

| 环境变量 | 默认值 | 对应参数 | 说明 |
|----------|--------|----------|------|
| `NB_ADDR` | `:8080` | `-addr` | HTTP 监听地址 |
| `NB_WORDS` | `/data/words.json` | `-words` | 词库文件路径 |
| `NB_STATS` | `/data/stats.json` | `-stats-file` | 统计持久化文件；置空可关闭 |
| `NB_TOKEN` | 空 | `-token` | 词库写操作令牌 |
| `NB_CI` | `false` | `-ci` | 是否忽略英文大小写 |
| `NB_WATCH` | `true` | `-watch` | 是否启用文件监听 |

## 热更新机制

词库更新不会直接修改正在提供查询的自动机：

1. 从 JSON 文件或在线管理接口读取最新词条。
2. 在后台完整构建新的 Aho-Corasick 自动机。
3. 构建成功后，通过 `atomic.Value` 一次性替换当前实例。
4. 已开始的请求继续使用原实例，后续请求使用新实例。

文件监听使用 `fsnotify`，也可以调用 `POST /reload` 主动触发更新。重建操作会串行执行，但不会占用查询路径上的锁。

## 项目结构

```text
noblack/
├── cmd/server/          # 服务入口
├── internal/api/        # HTTP 接口与内嵌控制台
├── internal/matcher/    # 自动机、词库解析与匹配逻辑
├── internal/stats/      # 运行统计与持久化
├── internal/store/      # 词库管理、热替换与文件监听
├── API.md               # 完整接口文档
├── words.json           # 默认词库
├── Dockerfile
└── docker-compose.yml
```

## 测试

```bash
# 运行全部测试
go test ./...

# 运行匹配模块基准测试
go test -bench=. ./internal/matcher/
```

实际吞吐会受文本长度、命中数量、词库内容、硬件环境和并发模型影响。建议使用与生产场景接近的数据运行基准测试，再据此设置实例数量和资源限制。

## 使用建议

- 生产环境建议配置 `-token`，并在反向代理层限制管理接口的访问范围。
- 词库与统计文件可能包含业务数据，请合理设置文件权限、备份和保留策略。
- 自动匹配结果适合作为规则判断或人工复核的辅助信息，不应在缺少上下文时作为唯一决策依据。

## 友情链接
[linuxdo](https://linux.do)

## License

本项目采用 [GNU Affero General Public License v3.0](./LICENSE) 许可。

## 请求体大小与管理接口鉴权

- 请求体不超过 3 MiB 时正常处理。
- 请求体超过 3 MiB 且不超过 10 MiB 时，服务端必须已配置令牌，并且请求需要携带该有效令牌。
- 请求体超过 10 MiB 时返回 HTTP 413，携带令牌也不会放行。
- 启用令牌鉴权后，`POST /reload` 和 `POST /stats/reset` 与词库增删改接口使用相同的令牌鉴权。

## AI 双模型（纯 CPU）

项目已内置 Lite 与 MacBERT 两个模型。部署时两个模型同时常驻内存，每次检测并行执行，并在 Web 页面同时显示结果。

```powershell
# Windows
.\scripts\start-all.ps1

# Docker
 docker compose up -d --build
```

打开 `http://127.0.0.1:8080`。详细说明见 [DEPLOY_MODELS.md](./DEPLOY_MODELS.md)。
