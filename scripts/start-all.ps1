param(
    [int]$Port = 8080,
    [int]$ModelPort = 8091,
    [int]$ModelThreads = 2
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    python .\scripts\start_all.py --port $Port --model-port $ModelPort --threads $ModelThreads
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally { Pop-Location }
