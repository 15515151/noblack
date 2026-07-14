param(
    [int]$Port = 8091,
    [int]$Threads = 2
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$workspace = Split-Path -Parent $root
$python = "python"
$vendorModel = Join-Path $workspace ".vendor-model"
$vendorData = Join-Path $workspace ".vendor"
$src = Join-Path $root "model_service\src"

$env:PYTHONPATH = "$vendorModel;$vendorData;$src"
$env:CUDA_VISIBLE_DEVICES = ""
$env:HF_HUB_OFFLINE = "1"
$env:TRANSFORMERS_OFFLINE = "1"
$env:TOKENIZERS_PARALLELISM = "false"
$env:NB_MODEL_HOST = "127.0.0.1"
$env:NB_MODEL_PORT = "$Port"
$env:NB_MODEL_THREADS = "$Threads"
$env:NB_LITE_MODEL = Join-Path $root "models\lite-baseline"
$env:NB_MACBERT_MODEL = Join-Path $root "models\macbert-pilot"

& $python (Join-Path $root "model_service\app.py")
