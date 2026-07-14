param()
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$runtime = Join-Path $root ".runtime"
New-Item -ItemType Directory -Force -Path $runtime | Out-Null

python -m pip install --target $runtime `
  --index-url https://download.pytorch.org/whl/cpu `
  torch==2.13.0+cpu
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

python -m pip install --target $runtime `
  --upgrade --no-cache-dir `
  -r (Join-Path $root "model_service\requirements.txt")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "CPU 模型运行依赖已安装到 $runtime" -ForegroundColor Green
