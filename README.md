# noblack · 高性能敏感词检测服务

基于 **Aho-Corasick 自动机** 的 Go 敏感词检测后端，支持敏感度分级、备注返回，以及**无锁原子热加载**。

## 特性

- **多模式匹配**：Aho-Corasick 自动机，匹配复杂度 `O(n + z)`，与词库规模**无关**。
- **按 rune 构建**：中文、**英文**（bilibili）、emoji 一视同仁，位置下标精确。
- **动态多等级**：等级是**任意字符串**且**一个词可挂多个等级**（如 `挖矿 → ["bilibili","引流"]`），不再硬编码 `High/Medium/Low`。热加载后经 `GET /levels` 自动感知全部等级。
- **多备注**：`大雷 → ["大奶子","大奶"]`（中英文逗号均可）。
- **一条录入多词**：`"word":"大雷,小雷"` 用逗号分隔，共享同一套等级/备注，检测时各词独立命中、独立计数。
- **在线词库管理**：`GET/POST/PUT/DELETE /words` 增删改查，改动即时生效并写回 `words.json`。**写操作可选令牌鉴权**（`-token`），读操作与检测始终开放。
- **访问统计**：请求数、命中数、**触发最多的敏感词**排行，全程无锁采集（`atomic`+`sync.Map`），对吞吐无实质影响。可选**持久化**（`-stats-file`），后台定期落盘、重启自动恢复。
- **内嵌前端控制台**：访问 `GET /`（`http://localhost:8080`）即可打开检测测试、词库管理、统计看板三合一页面，零外部依赖。
- **JSON 词库**：单一 JSON 文件维护，见 [词库格式](#词库格式)。
- **大小写不敏感**（可选 `-ci`）：`Bilibili`/`BILIBILI` 均命中词条 `bilibili`。
- **无锁热加载**：`atomic.Value` 存自动机指针，后台构建新树 + 原子替换，**读请求永不阻塞**。
- **两种热加载触发**：`fsnotify` 文件监听（自动）+ `POST /reload`（手动）。

> 📖 完整接口说明（每个请求怎么发、响应体逐字段解释、错误码）见 **[API.md](./API.md)**。

## 目录结构

```
noblack/
├── go.mod / go.sum
├── words.json                      # 词库 (JSON)
├── API.md                          # 完整 API 文档
├── Dockerfile                      # 多阶段构建 (静态编译 + alpine)
├── docker-entrypoint.sh            # 容器入口: 初始化数据卷 + 环境变量转参数
├── docker-compose.yml
├── .dockerignore
├── cmd/
│   └── server/
│       └── main.go                 # 服务入口: 装配 + 启动 + 优雅关闭
└── internal/
    ├── matcher/
    │   ├── automaton.go            # AC 自动机: 节点定义 / 构建 / 匹配 / 等级发现
    │   ├── loader.go               # JSON 词条: 加载 / 构建 / 保存
    │   └── automaton_test.go       # 单元测试 + 基准测试
    ├── stats/
    │   ├── stats.go                # 无锁统计采集 (atomic + sync.Map)
    │   ├── persist.go              # 统计持久化: 定期落盘 + 启动恢复 (方案 A)
    │   └── stats_test.go / persist_test.go
    ├── store/
    │   ├── store.go                # atomic.Value 热替换 + 词条 CRUD
    │   ├── store_test.go
    │   └── watcher.go              # fsnotify 文件监听 (去抖)
    └── api/
        ├── handler.go              # HTTP Handler: 检测 / CRUD / 统计 / 运维
        ├── helpers.go
        └── page.go                 # 内嵌前端控制台 (单页 HTML)
```

## 词库格式

服务使用单一 JSON 词库（默认 `./words.json`）：

```json
{
  "words": [
    { "word": "大雷",      "levels": ["High"],            "remarks": ["大奶子", "大奶"] },
    { "word": "大雷,小雷", "levels": ["High"],            "remarks": ["女性胸部"] },
    { "word": "挖矿",      "levels": ["bilibili", "引流"], "remarks": ["引流站点"] },
    { "word": "六合彩",    "levels": ["赌博", "诈骗"],      "remarks": ["非法博彩", "菠菜"] },
    { "word": "pornhub",   "level": "色情",               "remarks": "黄色平台,成人网站" }
  ]
}
```

- **`word`**：敏感词；**可用逗号（中/英文）分隔多个词**（`"大雷,小雷"`），共享同一套等级/备注，检测时各词独立命中、独立计数。
- **`levels`**（推荐）：字符串数组，**一个词可属于多个等级**，如 `["bilibili","引流"]`。
- **`level`**（兼容）：单等级字符串，也可写逗号串 `"a,b"`；与 `levels` 同时存在时以 `levels` 优先。
- **`remarks`**：数组 `["a","b"]` 或逗号串 `"a,b"` 皆可；缺省为 `[]`。
- `level`/`levels` 都缺省时用 `-default-level`（默认 `Low`）。
- 顶层也支持直接是数组 `[ {...}, {...} ]`（省略 `words` 包裹）。逗号支持中文 `，` 与英文 `,`。

## 运行

```bash
# 拉取依赖 (fsnotify)
go mod tidy

# 启动 (默认 :8080, 词库 ./words.json)
go run ./cmd/server -words ./words.json -addr :8080

# 英文词大小写不敏感
go run ./cmd/server -words ./words.json -ci

# 开启统计持久化 (重启不丢, 每 30s 落盘)
go run ./cmd/server -stats-file ./stats.json

# 开启写操作鉴权 (增/改/删词条需令牌)
go run ./cmd/server -token "your-secret-token"

# 关闭文件监听 (仅用 /reload 手动热加载)
go run ./cmd/server -watch=false
```

启动参数：

| 参数 | 默认 | 说明 |
|------|------|------|
| `-words` | `./words.json` | JSON 词库路径 |
| `-addr` | `:8080` | HTTP 监听地址 |
| `-watch` | `true` | 是否启用 fsnotify 自动热加载 |
| `-ci` | `false` | 匹配大小写不敏感（主要影响英文词） |
| `-default-level` | `Low` | 词条未标注 `level`/`levels` 时的默认等级 |
| `-stats-file` | `""`（不持久化） | 统计持久化文件路径；留空则重启后统计归零 |
| `-stats-flush-interval` | `30s` | 统计后台落盘间隔（仅 `-stats-file` 非空时生效） |
| `-token` | `""`（不鉴权） | 词条写操作（增/改/删）的鉴权令牌；留空则全部开放 |

## 测试

```bash
# 单元测试
go test ./...

# 基准测试 (匹配吞吐)
go test -bench=. ./internal/matcher/
```

## Docker 部署

镜像为多阶段构建：静态编译（`CGO_ENABLED=0`）后放进 alpine，最终镜像约 20MB（其中二进制约 6MB，前端已内嵌进二进制，无需额外静态资源）。

### 用本地目录存放 words.json（推荐）

把宿主机的一个目录绑定挂载到容器 `/data`，词库就是 `./data/words.json`，你可以**直接用编辑器改这个文件**，fsnotify 会自动热加载。为让容器能写这个目录（增删改词条、落盘统计都要写），用 `--user` 让容器以你自己的身份运行：

```bash
# 构建
docker build -t noblack:latest .

# 运行: 词库/统计放在宿主机 ./data 目录, 容器以当前用户身份运行
mkdir -p ./data
docker run -d --name noblack -p 8080:8080 \
  --user "$(id -u):$(id -g)" \
  -v "$(pwd)/data:/data" \
  noblack:latest

# 打开 http://localhost:8080 即为控制台
```

- 词库文件就是 `./data/words.json`，直接编辑即时生效。
- 首次启动时若 `./data` 里没有 `words.json`，会用镜像内置的默认词库初始化。
- 统计落在 `./data/stats.json`，重启不丢。

> `--user "$(id -u):$(id -g)"` 让容器进程用你的 UID/GID，因此它写出的文件归你所有、你能直接编辑和删除，也不会有权限报错。（Windows/WSL 下 `id -u` 通常返回 1000，一般也可直接用。）

### 用 docker compose

```bash
export DOCKER_UID=$(id -u) DOCKER_GID=$(id -g)   # 让 compose 以你的身份运行容器
docker compose up -d
```

compose 已配置绑定挂载 `./data:/data` 与 `user: "${DOCKER_UID}:${DOCKER_GID}"`，词库同样在 `./data/words.json`。

### 只挂单个 words.json 文件（只读场景）

如果你只想让容器读一个本地词库文件、不需要在网页里改：

```bash
docker run -d --name noblack -p 8080:8080 \
  -v "$(pwd)/words.json:/data/words.json:ro" \
  -e NB_STATS="" \
  noblack:latest
```

此时网页上的增删改会失败（文件只读），只能改本地 `words.json` 靠热加载生效；`NB_STATS=""` 关闭统计持久化（否则会因无处写盘而报错）。

### 环境变量配置

入口脚本会把下列环境变量翻译成启动参数：

| 环境变量 | 默认 | 对应参数 | 说明 |
|----------|------|----------|------|
| `NB_ADDR` | `:8080` | `-addr` | 监听地址 |
| `NB_WORDS` | `/data/words.json` | `-words` | 词库路径 |
| `NB_STATS` | `/data/stats.json` | `-stats-file` | 统计持久化文件；置空则不持久化 |
| `NB_TOKEN` | `""` | `-token` | 写操作鉴权令牌；置空则不鉴权 |
| `NB_CI` | `false` | `-ci` | `true` 开启英文大小写不敏感 |
| `NB_WATCH` | `true` | `-watch` | 是否启用文件监听热加载 |

```bash
# 例: 开启鉴权 + 大小写不敏感
docker run -d --name noblack -p 8080:8080 \
  --user "$(id -u):$(id -g)" -v "$(pwd)/data:/data" \
  -e NB_TOKEN=your-secret -e NB_CI=true noblack:latest
```

> `docker stop` 发送 SIGTERM，容器内进程会优雅关闭并**落盘最后一次统计**（Linux 下 SIGTERM 可正常捕获）。

## API

打开浏览器访问 **`http://localhost:8080`** 即可使用内嵌控制台（检测 / 词库管理 / 统计）。

接口分四组：

- **检测**：`POST /check`
- **词库管理**：`GET /words`、`POST /words`、`PUT /words/{word}`、`DELETE /words/{word}`
- **统计**：`GET /stats`、`POST /stats/reset`
- **运维**：`POST /reload`、`GET /levels`、`GET /health`

快速上手：

```bash
# 检测
curl -X POST http://localhost:8080/check \
  -H 'Content-Type: application/json' -d '{"text":"有人在挖矿"}'

# 新增词条
curl -X POST http://localhost:8080/words \
  -H 'Content-Type: application/json' \
  -d '{"word":"六合彩","levels":["赌博","诈骗"],"remarks":["非法博彩"]}'

# 查看统计 (触发最多的词等)
curl "http://localhost:8080/stats?top=10"
```

> 📖 **每个接口的请求方式、请求体、响应体逐字段说明、错误码——完整文档见 [API.md](./API.md)。**

## 热加载并发安全说明

```
读请求 (POST /check)                    热加载 (fsnotify / POST /reload)
      │                                          │
  store.Current()  ← atomic.Load             LoadFromFile()  ← 后台构建全新树
      │  (无锁, 拿到旧树/新树之一)                  │  (纯 CPU, 不触碰旧树)
  auto.FindAll()                             atomic.Store(fresh) ← 原子发布
```

- 读路径只有一次 `atomic.Load`，**无锁、无阻塞**。
- 新树在后台完整构建完成后，才通过一次 `atomic.Store` “发布”。
- 旧树在最后一个引用它的请求结束后由 GC 回收，切换瞬间无缝。
- `reloadMu` 仅串行化“重建”动作本身，**不参与读路径**，因此绝不会阻塞 `/check`。

## 性能

单机基准下 `FindAll` 单次调用为微秒级，轻松满足 10,000 req/min（~166 QPS）的要求，实际可达数万 QPS。自动机为只读结构，可被任意数量 goroutine 并发读取。

**统计的开销**：按接口计数用 `atomic.Int64`（~1–5 ns），按词计数用 `sync.Map` + 原子值（~10–30 ns）。相比一次匹配 ~2500 ns，统计占比 **< 0.1%**，读路径依旧无锁。排序取「触发最多的词」只发生在查看 `/stats` 那一下，不在检测热路径上。
