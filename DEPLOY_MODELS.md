# 双模型 CPU 部署

本项目现在把两个模型直接放在仓库中：

- `models/lite-baseline`：轻量字符 + 拼音双分支模型
- `models/macbert-pilot`：MacBERT + 拼音双分支模型

部署时两个模型会：

1. 强制使用纯 CPU；
2. 服务启动时加载一次并常驻内存；
3. 每次 `/check` 请求同时并行推理；
4. Web 页面同时显示两个模型的概率、动作、耗时和门控权重；
5. Go 关键词匹配也和模型请求并行执行。

## Windows 本地运行

从当前项目目录执行：

```powershell
.\scripts\start-all.ps1
```

然后访问：

```text
http://127.0.0.1:8080
```

当前上层工作区已经安装了 Python 模型依赖，因此可以直接运行。如果把本项目单独复制到另一台 Windows 机器，先安装 CPU 运行环境：

```powershell
.\scripts\install-cpu-runtime.ps1
.\scripts\start-all.ps1
```

`install-cpu-runtime.ps1` 会安装纯 CPU PyTorch，不会安装或使用 CUDA。

### 自检

```powershell
python .\scripts\start_all.py --self-test --port 18080 --model-port 18091
```

成功输出应包含：

```json
{
  "ok": true,
  "device": "cpu",
  "parallel": true,
  "models": ["lite", "macbert"]
}
```

## Docker Compose

```bash
docker compose up -d --build
```

镜像使用 `python:3.13-slim` 加 PyTorch CPU wheel。没有配置 NVIDIA Runtime，也不会访问 GPU。

首次构建需要下载约 2 GB 的 PyTorch CPU wheel 与相关依赖，构建会比原先的纯 Go 镜像慢。运行时建议至少：

- 2 个 CPU 核心；
- 2 GB 可用内存，推荐 4 GB；
- 两个模型当前合计约 398 MB 权重。

## 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `NB_MODEL_THREADS` | `2` | PyTorch CPU 线程数 |
| `NB_MODEL_PASS_THRESHOLD` | `0.15` | 低于此概率自动通过 |
| `NB_MODEL_BLOCK_THRESHOLD` | `0.5` | 高于等于此概率自动拦截 |
| `NB_MODEL_SERVICE_URL` | `http://127.0.0.1:8091` | Go 调用的本地模型服务 |
| `NB_MODEL_MAX_TEXT_CHARS` | `20000` | AI 模型单次文本字符上限 |

CPU 核数较多时可以调整：

```powershell
.\scripts\start-all.ps1 -ModelThreads 4
```

不建议盲目设成全部 CPU 线程，因为 Lite 与 MacBERT 本身会并行运行，过多线程可能引起抢占并降低延迟。

## `/check` 响应

原有的词库字段仍然保留，并新增：

```json
{
  "has_sensitive_word": false,
  "matches": [],
  "model_device": "cpu",
  "models_parallel": true,
  "combined_action": "review",
  "model_latency_ms": 28.5,
  "model_results": [
    {
      "model": "lite",
      "sexual_harm_probability": 0.2,
      "action": "review"
    },
    {
      "model": "macbert",
      "sexual_harm_probability": 0.7,
      "action": "block"
    }
  ]
}
```

`combined_action` 默认取两个模型中更严格的动作：`block > review > pass`。前端会保留两个独立结果，不会只展示合并结论。

## 模型服务不可用时

Go 服务会继续返回关键词匹配结果，并在响应中添加：

```json
{"model_error":"model service unavailable"}
```

正常的一体化启动脚本和 Docker 入口会等待两个模型都成功加载后，才启动 Go Web 服务。

## Git LFS

MacBERT 权重约 393 MB，`.gitattributes` 已将 `model.safetensors` 配置为 Git LFS。提交模型文件前，需要安装并启用 Git LFS：

```bash
git lfs install
git add .gitattributes models
```
