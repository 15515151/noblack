# noblack 敏感词检测服务 · API 文档

> 版本：v1 · 基准地址（示例）：`http://localhost:8080`
> 所有响应均为 `Content-Type: application/json; charset=utf-8`。

---

## 目录

- [通用约定](#通用约定)
  - [统一响应结构](#统一响应结构)
  - [业务码 code 说明](#业务码-code-说明)
  - [错误响应](#错误响应)
- [接口一览](#接口一览)
- [1. POST /check — 敏感词检测](#1-post-check--敏感词检测)
- [2. 词库管理（CRUD）](#2-词库管理crud)
- [3. 统计](#3-统计)
- [4. POST /reload — 手动热加载词库](#4-post-reload--手动热加载词库)
- [5. GET /levels — 查询全部等级](#5-get-levels--查询全部等级)
- [6. GET /health — 健康检查](#6-get-health--健康检查)
- [词库文件格式](#词库文件格式)
- [附录：字段速查表](#附录字段速查表)

---

## 通用约定

### 统一响应结构

所有接口（无论成功或业务失败）都返回同一个外层结构：

```jsonc
{
  "code": 200,          // 业务码, 见下表 (注意: 与 HTTP 状态码可能不同)
  "message": "success", // 人类可读的描述
  "data": { ... }       // 业务数据; 出错时可能不存在
}
```

- `code`：**业务码**，`int`。
- `message`：`string`，成功或错误说明。
- `data`：`object`，各接口结构不同；发生错误时该字段通常缺省（`omitempty`）。

### 业务码 code 说明

| code | HTTP 状态码 | 含义 |
|------|------------|------|
| 200 | 200 | 成功 |
| 400 | 400 | 请求体不合法（JSON 解析失败 / body 为空） |
| 405 | 405 | HTTP 方法不允许（如对只收 POST 的接口用了 GET） |
| 500 | 500 | 服务端处理失败（如热加载时词库文件损坏） |

> ⚠️ **未匹配到路由**（访问不存在的路径）由 Go 标准库直接返回 **HTTP 404** 且**响应体为纯文本** `404 page not found`，不是上面的 JSON 结构。

### 错误响应

错误响应只有 `code` 和 `message`，没有 `data`。例如：

```json
{ "code": 405, "message": "仅支持 POST" }
```

```json
{ "code": 400, "message": "请求体解析失败: EOF" }
```

---

## 接口一览

| 方法 | 路径 | 作用 | 请求体 |
|------|------|------|--------|
| POST | `/check` | 检测文本中的敏感词 | JSON |
| GET | `/words` | 列出全部词条 | 无 |
| POST | `/words` | 新增一个词条 | JSON |
| PUT | `/words/{word}` | 更新一个词条 | JSON |
| DELETE | `/words/{word}` | 删除一个词条 | 无 |
| GET | `/auth/status` | 查询是否需要写操作令牌 | 无 |
| POST | `/auth/verify` | 校验令牌是否正确 | 无（令牌走请求头） |
| GET | `/stats` | 查询运行统计（请求数、高频词等） | 无 |
| POST | `/stats/reset` | 清零统计 | 无 |
| POST | `/reload` | 手动触发词库热加载 | 无 |
| GET | `/levels` | 查询当前词库中出现的全部等级 | 无 |
| GET | `/health` | 健康检查（词条数 + 等级集合） | 无 |
| GET | `/` | 内嵌前端控制台页面（HTML） | 无 |

> 词库的增删改（`/words` 系列）会**同时**更新内存中的检测树和磁盘上的 `words.json`，并原子生效，读请求零阻塞。

---

## 1. POST /check — 敏感词检测

检测一段文本中命中的所有敏感词，返回每个命中的词、等级、备注和位置。

### 请求

| 项 | 值 |
|----|----|
| 方法 | `POST` |
| 路径 | `/check` |
| Content-Type | `application/json`（建议；服务端不强制校验，但 body 必须是合法 JSON） |

**请求体字段**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `text` | string | 是 | 待检测文本。缺失、空字符串或仅包含空白字符时返回 HTTP 400。 |

**请求体示例**

```json
{ "text": "有人在挖矿看PornHub" }
```

### 请求示例（curl）

```bash
curl -X POST http://localhost:8080/check \
  -H 'Content-Type: application/json' \
  -d '{"text":"有人在挖矿看PornHub"}'
```

> 💡 **Windows PowerShell 用户注意**：直接用 `curl -d '{中文}'` 可能因终端编码导致中文乱码/漏匹配。建议把请求体写入 UTF-8 文件再发送：
> ```bash
> curl -X POST http://localhost:8080/check -H "Content-Type: application/json" --data-binary "@body.json"
> ```
> 或使用 Postman / Apifox 等工具，确保编码为 UTF-8。

### 响应

**成功（HTTP 200）**

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "has_sensitive_word": true,
    "matches": [
      {
        "word": "挖矿",
        "levels": ["bilibili", "引流"],
        "remarks": ["引流站点"],
        "position": { "start": 3, "end": 5 }
      },
      {
        "word": "pornhub",
        "levels": ["色情"],
        "remarks": ["黄色平台", "成人网站"],
        "position": { "start": 6, "end": 13 }
      }
    ]
  }
}
```

**响应字段说明**

| 字段 | 类型 | 说明 |
|------|------|------|
| `data.has_sensitive_word` | bool | 是否命中任意敏感词。 |
| `data.matches` | array | 命中列表；无命中时为空数组 `[]`（不是 `null`）。 |
| `data.matches[].word` | string | 命中的敏感词原文（保留词库中的原始大小写）。 |
| `data.matches[].levels` | string[] | 该词的等级列表，可能有多个，如 `["bilibili","引流"]`；恒为数组。 |
| `data.matches[].remarks` | string[] | 该词的备注列表；无备注时为空数组 `[]`。 |
| `data.matches[].position.start` | int | 命中起始下标，**按 rune（Unicode 字符）计**，含。 |
| `data.matches[].position.end` | int | 命中结束下标，按 rune 计，**不含**。即区间 `[start, end)`。 |

> 📌 **position 是 rune 下标，不是字节下标。** 例如文本 `有人在挖矿`，`挖矿` 的位置是 `[3,5)`（第 4、5 个字符），而不是按 UTF-8 字节算的 `[9,15)`。用 `text[start:end]`（Go 中 `[]rune(text)[start:end]`）即可截出原词。
>
> 📌 **同一位置可能返回多条命中**（重叠匹配）。若词库同时含 `大雷` 和 `雷`，文本 `大雷` 会返回两条：`大雷 [0,2)` 与 `雷 [1,2)`。

**未命中（HTTP 200）**

```json
{
  "code": 200,
  "message": "success",
  "data": { "has_sensitive_word": false, "matches": [] }
}
```

未命中响应仅适用于 `text` 为非空文本、但没有词库或模型命中的情况。

**空文本（HTTP 400）**

以下请求均返回 HTTP 400：

```json
{}
```

```json
{"text":""}
```

```json
{"text":"   "}
```

响应示例：

```json
{"code":400,"message":"text 不能为空或仅包含空白字符"}
```

### 错误响应

| 场景 | HTTP | 响应体 |
|------|------|--------|
| 请求体是非法 JSON | 400 | `{"code":400,"message":"请求体解析失败: invalid character 'b' ..."}` |
| 请求体为空 | 400 | `{"code":400,"message":"请求体解析失败: EOF"}` |
| `text` 缺失、为空或仅包含空白 | 400 | `{"code":400,"message":"text 不能为空或仅包含空白字符"}` |
| 用了非 POST 方法 | 405 | `{"code":405,"message":"仅支持 POST"}` |

---

## 2. 词库管理（CRUD）

用于在线增删改词条。**任何修改都会立即：① 重建检测树并原子生效；② 写回磁盘 `words.json`。** 期间 `/check` 读请求不阻塞。

> 🔑 **写操作鉴权（可选）**：服务端用 `-token <令牌>` 启动后，**新增（POST）、更新（PUT）、删除（DELETE）** 需携带令牌，否则返回 **401**。**读操作 `GET /words` 及检测、统计不需要令牌。** 未设 `-token` 时全部开放（向后兼容）。
>
> 令牌通过请求头传递，两种写法均可：
> - `X-Auth-Token: <令牌>`
> - `Authorization: Bearer <令牌>`
>
> 前端在「词库管理」页有独立的令牌输入框，验证后存入浏览器 `localStorage`，不会整页拦截。

### 2.1 GET /words — 列出全部词条

```bash
curl http://localhost:8080/words
```

响应（HTTP 200，词条按 `word` 字典序排列）：

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "count": 2,
    "words": [
      { "word": "pornhub", "levels": ["色情"],            "remarks": ["黄色平台", "成人网站"] },
      { "word": "挖矿",     "levels": ["bilibili", "引流"], "remarks": ["引流站点"] }
    ]
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `data.count` | int | 词条总数。 |
| `data.words[]` | array | 词条列表。 |
| `data.words[].word` | string | 敏感词。 |
| `data.words[].levels` | string[] | 等级列表。 |
| `data.words[].remarks` | string[] | 备注列表。 |

### 2.2 POST /words — 新增或合并词条

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `word` | string | 是 | 敏感词。可用中英文逗号分隔多个词，它们共享同一套 `levels`/`remarks`。 |
| `levels` | string[] | 否 | 等级列表；缺省用默认等级。 |
| `remarks` | string[] | 否 | 备注列表。 |

**重叠词的合并规则**

- 请求中的词与已有批量词条部分重叠，并且 `levels`、`remarks` 完全相同时，服务端自动去重并合并新词。
- 所有请求词都已存在且元数据相同时，按幂等合并处理，不会重复写入。
- 重叠词的 `levels` 或 `remarks` 不同时返回 HTTP 400，避免隐式覆盖已有元数据；此时使用 `PUT /words/{word}` 明确更新原词条。

例如已有：

```json
{"word":"你妈,他妈,你老冯","levels":["Medium"],"remarks":["辱骂"]}
```

再次提交：

```json
{"word":"你妈,他妈,你老冯,妈了个逼","levels":["Medium"],"remarks":["辱骂"]}
```

会合并为 `你妈,他妈,你老冯,妈了个逼`。
Web 控制台编辑批量词条时也遵循该规则：如果 POST 返回 `merged`，旧批量词条已经被服务端替换，前端不会再对旧词条路径发送 DELETE 请求。

**新增成功（HTTP 200）**

```json
{
  "code": 200,
  "message": "created",
  "data": {
    "word": "六合彩",
    "levels": ["赌博", "诈骗"],
    "remarks": ["非法博彩", "菠菜"],
    "created": true,
    "merged": false,
    "added_words": ["六合彩"],
    "reused_words": []
  }
}
```

**合并成功（HTTP 200）**

```json
{
  "code": 200,
  "message": "merged",
  "data": {
    "word": "你妈,他妈,你老冯,妈了个逼",
    "levels": ["Medium"],
    "remarks": ["辱骂"],
    "created": false,
    "merged": true,
    "added_words": ["妈了个逼"],
    "reused_words": ["你妈", "他妈", "你老冯"]
  }
}
```

**错误**

| 场景 | HTTP | 响应体 |
|------|------|--------|
| `word` 为空 | 400 | `{"code":400,"message":"word 不能为空"}` |
| 重叠词元数据冲突 | 400 | 重叠词的 `levels` 或 `remarks` 与已有词条不同；使用 PUT 明确更新。 |
| 请求体非法 JSON | 400 | `{"code":400,"message":"请求体解析失败: ..."}` |
| 落盘失败 | 400 | `{"code":400,"message":"写入临时词库文件失败: ..."}` |
### 2.3 PUT /words/{word} — 更新词条

`{word}` 是路径参数（URL 编码；中文需 `encodeURIComponent`）。请求体同 POST；`word` 字段可省略，缺省时用路径里的词。整条覆盖（`levels`/`remarks` 以请求体为准）。

```bash
# 更新 "挖矿" 的等级与备注 (挖矿 的 URL 编码为 %E6%8C%96%E7%9F%BF)
curl -X PUT "http://localhost:8080/words/%E6%8C%96%E7%9F%BF" \
  -H 'Content-Type: application/json' \
  -d '{"levels":["bilibili"],"remarks":["已降级"]}'
```

**成功（HTTP 200）**

```json
{
  "code": 200,
  "message": "updated",
  "data": { "word": "挖矿", "levels": ["bilibili"], "remarks": ["已降级"] }
}
```

**错误**：词条不存在 → **HTTP 404** `{"code":404,"message":"词条 \"挖矿\" 不存在"}`。

### 2.4 DELETE /words/{word} — 删除词条

```bash
curl -X DELETE "http://localhost:8080/words/%E6%8C%96%E7%9F%BF"
```

**成功（HTTP 200）**

```json
{ "code": 200, "message": "deleted", "data": { "word": "挖矿" } }
```

**错误**：词条不存在 → **HTTP 404** `{"code":404,"message":"词条 \"挖矿\" 不存在"}`。

### 2.5 鉴权端点

**GET /auth/status** — 前端据此决定是否显示令牌输入框。

```bash
curl http://localhost:8080/auth/status
# {"code":200,"message":"success","data":{"required":true}}   # 已用 -token 启动
# {"code":200,"message":"success","data":{"required":false}}  # 未启用鉴权
```

**POST /auth/verify** — 校验令牌是否正确（令牌走请求头）。

```bash
curl -X POST http://localhost:8080/auth/verify -H "X-Auth-Token: s3cret"
# 正确 → {"code":200,"message":"ok"}
# 错误 → HTTP 401 {"code":401,"message":"令牌无效或缺失"}
```

> 写操作（POST/PUT/DELETE `/words`）令牌错误或缺失时统一返回 **HTTP 401** `{"code":401,"message":"令牌无效或缺失"}`。

---

## 3. 统计

统计采集全程无锁（`atomic` + `sync.Map`），单次记录约纳秒级，对检测吞吐无实质影响。

**持久化（可选）**：默认统计只在内存，进程重启归零。启动时加 `-stats-file ./stats.json` 即开启持久化——后台按 `-stats-flush-interval`（默认 30s）定期落盘，启动时自动读回，`Ctrl+C` 优雅退出时再补刷一次。崩溃时最多丢失最后一个间隔内的增量。`/stats/reset` 会把内存清零，下次落盘即写入零值。

### 3.1 GET /stats — 查询统计

**查询参数**

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `top` | int | 20 | 返回命中最多的前 N 个词。非正数忽略、用默认值。 |

```bash
curl "http://localhost:8080/stats?top=5"
```

**响应（HTTP 200）**

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "check_requests": 5,
    "hit_requests": 4,
    "total_matches": 4,
    "reload_count": 0,
    "distinct_words": 2,
    "top_words": [
      { "word": "测试词", "count": 3 },
      { "word": "挖矿",   "count": 1 }
    ]
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `data.check_requests` | int | `/check` 被调用的总次数。 |
| `data.hit_requests` | int | 其中「至少命中一个词」的请求数。 |
| `data.total_matches` | int | 累计命中的敏感词次数（含同一请求内的多个/重叠命中）。 |
| `data.reload_count` | int | 词库热加载成功次数（含 `/reload` 与文件自动监听）。 |
| `data.distinct_words` | int | 曾被命中过的不同词数量。 |
| `data.top_words[]` | array | 命中最多的词，按 `count` 降序（同数按词字典序）。 |
| `data.top_words[].word` | string | 敏感词。 |
| `data.top_words[].count` | int | 该词累计命中次数。 |

### 3.2 POST /stats/reset — 清零统计

```bash
curl -X POST http://localhost:8080/stats/reset
```

**成功（HTTP 200）**：`{"code":200,"message":"reset"}`
**方法错误（HTTP 405）**：`{"code":405,"message":"仅支持 POST"}`

---

## 4. POST /reload — 手动热加载词库

立即从磁盘重新读取词库文件并原子替换生效。与 `fsnotify` 文件自动监听是两条并行触发路径，任选其一即可，也可同时存在。

> 🔒 **并发安全**：重建在后台完成，期间 `/check` 读请求走旧词库、零阻塞；构建成功后一次性原子切换。若词库文件在重建时损坏/不合法，**保留旧词库**并返回 500，不影响线上检测。

### 请求

| 项 | 值 |
|----|----|
| 方法 | `POST` |
| 路径 | `/reload` |
| 请求体 | 无（可为空） |

### 请求示例（curl）

```bash
curl -X POST http://localhost:8080/reload
```

### 响应

**成功（HTTP 200）**

```json
{
  "code": 200,
  "message": "reloaded",
  "data": { "word_count": 7 }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `data.word_count` | int | 重新加载后当前生效的词条总数。 |

**失败（HTTP 500）** —— 词库文件不存在或 JSON 不合法：

```json
{
  "code": 500,
  "message": "热加载失败: 解析 JSON 词库失败: invalid character ..."
}
```

> 此时**旧词库继续服务**，`/check` 不受影响。

**方法错误（HTTP 405）**

```json
{ "code": 405, "message": "仅支持 POST" }
```

---

## 5. GET /levels — 查询全部等级

返回当前词库中**实际出现过的所有等级**（去重、排序）。等级是动态发现的——词库里新增了 `赌博`、`引流` 等自定义等级并热加载后，此接口立即反映，无需改代码或重启。

### 请求

| 项 | 值 |
|----|----|
| 方法 | `GET` |
| 路径 | `/levels` |
| 请求体 | 无 |

### 请求示例（curl）

```bash
curl http://localhost:8080/levels
```

### 响应（HTTP 200）

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "count": 8,
    "levels": ["High", "Low", "Medium", "bilibili", "引流", "色情", "诈骗", "赌博"]
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `data.levels` | string[] | 全部等级，已去重并按字典序排序。 |
| `data.count` | int | 等级数量，等于 `levels` 长度。 |

---

## 6. GET /health — 健康检查

用于探活 / 监控，返回当前词条数和等级集合。

### 请求

| 项 | 值 |
|----|----|
| 方法 | `GET` |
| 路径 | `/health` |
| 请求体 | 无 |

### 请求示例（curl）

```bash
curl http://localhost:8080/health
```

### 响应（HTTP 200）

```json
{
  "code": 200,
  "message": "ok",
  "data": {
    "levels": ["High", "Low", "Medium", "bilibili", "引流", "色情", "诈骗", "赌博"],
    "word_count": 7
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `data.word_count` | int | 当前生效的词条总数。 |
| `data.levels` | string[] | 当前全部等级（同 `/levels`）。 |

> 用于 K8s liveness/readiness 探针时，判断 HTTP 200 即可视为健康。

---

## 词库文件格式

服务只使用 **JSON** 词库（默认 `./words.json`，可用启动参数 `-words` 指定）。

```json
{
  "words": [
    { "word": "大雷",    "levels": ["High"],            "remarks": ["大奶子", "大奶"] },
    { "word": "挖矿",    "levels": ["bilibili", "引流"], "remarks": ["引流站点"] },
    { "word": "六合彩",  "levels": ["赌博", "诈骗"],      "remarks": ["非法博彩", "菠菜"] },
    { "word": "pornhub", "level": "色情",               "remarks": "黄色平台,成人网站" }
  ]
}
```

**词条字段**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `word` | string | 是 | 敏感词，支持中文 / 英文 / emoji。**可用逗号分隔多个词**（`"大雷,小雷"`），共享同一套等级/备注。为空则跳过该条。 |
| `levels` | string[] | 否 | 等级列表（推荐）。一个词可属于多个等级。 |
| `level` | string | 否 | 单等级（兼容写法），也可写逗号串 `"a,b"`。与 `levels` 并存时 **`levels` 优先**。 |
| `remarks` | string[] 或 string | 否 | 备注。数组 `["a","b"]` 或逗号串 `"a,b"` 皆可；缺省为空。 |

- `level` / `levels` 都缺省时，使用启动参数 `-default-level`（默认 `Low`）。
- 顶层也可直接是数组：`[ {…}, {…} ]`（省略 `words` 包裹）。
- 逗号分隔支持中文 `，` 与英文 `,`。

**修改词库后如何生效**：直接编辑 `words.json` 保存，若开启了文件监听会自动热加载；或调用 `POST /reload` 手动触发。

---

## 附录：字段速查表

**请求**

| 接口 | 方法 | 请求体 |
|------|------|--------|
| `/check` | POST | `{"text": "字符串"}` |
| `/words` | GET | 无 |
| `/words` | POST | `{"word","levels":[],"remarks":[]}` |
| `/words/{word}` | PUT | `{"levels":[],"remarks":[]}` |
| `/words/{word}` | DELETE | 无 |
| `/stats` | GET | 无（可选 `?top=N`） |
| `/stats/reset` | POST | 无 |
| `/reload` | POST | 无 |
| `/levels` | GET | 无 |
| `/health` | GET | 无 |

**响应 data 结构**

| 接口 | data 结构 |
|------|-----------|
| `/check` | `{ has_sensitive_word: bool, matches: [{ word, levels[], remarks[], position:{start,end} }] }` |
| `GET /words` | `{ count: int, words: [{ word, levels[], remarks[] }] }` |
| `POST/PUT /words` | `{ word, levels[], remarks[] }` |
| `DELETE /words` | `{ word }` |
| `/stats` | `{ check_requests, hit_requests, total_matches, reload_count, distinct_words, top_words:[{word,count}] }` |
| `/reload` | `{ word_count: int }` |
| `/levels` | `{ levels: string[], count: int }` |
| `/health` | `{ word_count: int, levels: string[] }` |

## 请求体大小策略

所有接口均按实际接收到的请求体字节数执行以下限制，分块传输请求也不例外：

- 不超过 3 MiB：正常处理。
- 超过 3 MiB 且不超过 10 MiB：服务端必须已配置令牌，请求还必须携带有效的 `X-Auth-Token` 或 Bearer 令牌；否则返回 HTTP 401。
- 超过 10 MiB：始终返回 HTTP 413，携带令牌也不会放行。

启用令牌鉴权后，`POST /reload` 和 `POST /stats/reset` 属于受保护的管理操作。


## AI 双模型响应字段

启用本地模型服务后，`POST /check` 会在原有词库匹配字段之外，返回 Lite 与 MacBERT 两个纯 CPU 模型的独立检测结果。两个模型常驻内存，并在每次请求中并行推理。

### 新增字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `data.model_results` | array | 两个模型的独立结果。正常情况下依次包含 `lite` 和 `macbert`。 |
| `data.combined_action` | string | 综合建议，取决于 `model_combine_policy`。生产微调模型默认 `max`，即取两个模型中更严格的动作。 |
| `data.model_combine_policy` | string | 当前合并策略，默认 `max`；可配置为低误报优先的 `consensus`。 |
| `data.model_device` | string | 模型运行设备；当前纯 CPU 部署固定为 `cpu`。 |
| `data.models_parallel` | bool | 两个模型是否并行推理；正常部署时为 `true`。 |
| `data.model_latency_ms` | number | 两个模型并行推理的总耗时，单位为毫秒。 |
| `data.model_error` | string | 模型服务不可用时的降级提示。出现该字段时，词库匹配结果仍然有效。 |

每个 `data.model_results[]` 元素包含：

| 字段 | 类型 | 说明 |
|------|------|------|
| `model` | string | 模型名称：`lite` 或 `macbert`。 |
| `id` | string | 输入文本的脱敏哈希标识，不回显原文。 |
| `sexual_harm_probability` | number | 色情或性暗示风险分数，范围为 `0`～`1`。 |
| `action` | string | 模型独立建议：`pass`、`review` 或 `block`。 |
| `semantic_gate` | number | 模型对字符语义分支的平均门控权重。 |
| `rule_hits` | string[] | 命中的高精度补充规则；无命中时为空数组。 |
| `pass_threshold` | number | 低于该阈值时动作是 `pass`。 |
| `block_threshold` | number | 大于等于该阈值时动作是 `block`。两个阈值之间为 `review`。 |
| `latency_ms` | number | 该模型的独立推理耗时，单位为毫秒。 |

### 双模型响应示例

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "has_sensitive_word": false,
    "matches": [],
    "model_device": "cpu",
    "models_parallel": true,
    "combined_action": "block",
    "model_combine_policy": "max",
    "model_latency_ms": 18.42,
    "model_results": [
      {
        "model": "lite",
        "id": "45ae67c3509964bc",
        "sexual_harm_probability": 0.1612,
        "action": "review",
        "semantic_gate": 0.5418,
        "rule_hits": [],
        "pass_threshold": 0.15,
        "block_threshold": 0.5,
        "latency_ms": 4.12
      },
      {
        "model": "macbert",
        "id": "45ae67c3509964bc",
        "sexual_harm_probability": 0.7638,
        "action": "block",
        "semantic_gate": 0.5583,
        "rule_hits": [],
        "pass_threshold": 0.15,
        "block_threshold": 0.5,
        "latency_ms": 17.95
      }
    ]
  }
}
```

### 降级行为

如果模型服务暂时不可用，`POST /check` 仍返回 HTTP 200 和正常的词库匹配结果，同时增加：

```json
{
  "model_error": "model service unavailable"
}
```

一体化启动脚本和 Docker 入口会等待 Lite 与 MacBERT 都加载完成后再启动 Web 服务，因此正常部署中不应频繁出现降级字段。
