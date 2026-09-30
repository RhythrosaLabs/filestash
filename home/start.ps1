# Starts Sensorium on Windows. Safe to run again at any time.
# If scripts are blocked: powershell -ExecutionPolicy Bypass -File .\start.ps1
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host "Docker is needed: install Docker Desktop from https://www.docker.com/products/docker-desktop/ and run this again."; exit 1
}
docker info *> $null
if ($LASTEXITCODE -ne 0) { Write-Host "Docker isn't running: open Docker Desktop, wait until it says 'running', then run this again."; exit 1 }

if (-not (Test-Path .env)) {
    @"
FILES_DIR=$($env:USERPROFILE -replace '\\','/')
AI_MODEL=qwen3:8b
AI_BASE_URL=
AI_API_KEY=
JEV_API_KEY=
"@ | Set-Content -Encoding ascii .env
    Write-Host "==> created home\.env (edit it to change the folder or the model)"
}
$vars = @{}
Get-Content .env | Where-Object { $_ -match '^\s*([A-Z_]+)=(.*)$' } | ForEach-Object { $vars[$Matches[1]] = $Matches[2] }
New-Item -ItemType Directory -Force rclone | Out-Null

$profileArgs = @()
$lmModel = $null
if (-not $vars["AI_BASE_URL"]) {
    try {
        $models = Invoke-RestMethod -TimeoutSec 2 http://localhost:1234/v1/models
        $lmModel = ($models.data | Where-Object { $_.id -notmatch "embed" } | Select-Object -First 1).id
    } catch {}
}
if (-not $vars["AI_BASE_URL"] -and $lmModel) {
    Write-Host "==> using LM Studio's server on this computer, model: $lmModel"
    $env:AI_BASE_URL = "http://host.docker.internal:1234/v1"
    if (-not $vars["AI_MODEL"] -or $vars["AI_MODEL"] -eq "qwen3:8b") { $env:AI_MODEL = $lmModel }
    if (-not $vars["AI_API_KEY"]) { $env:AI_API_KEY = "lm-studio" }
} elseif (-not $vars["AI_BASE_URL"]) {
    try {
        Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 http://localhost:11434/api/version | Out-Null
        Write-Host "==> using the Ollama app already running on this computer"
        $env:AI_BASE_URL = "http://host.docker.internal:11434/v1"
        if (Get-Command ollama -ErrorAction SilentlyContinue) { ollama pull $vars["AI_MODEL"] }
    } catch {
        Write-Host "==> running Ollama in Docker (first run downloads the model, a few GB)"
        $env:AI_BASE_URL = "http://ollama:11434/v1"
        $profileArgs = @("--profile", "ollama")
    }
}

Write-Host "==> building and starting (the first build takes 5 to 15 minutes)"
docker compose @profileArgs up -d --build
if ($LASTEXITCODE -ne 0) { exit 1 }

Write-Host -NoNewline "==> waiting for Sensorium"
for ($i = 0; $i -lt 60; $i++) {
    try { Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 http://localhost:8334/ | Out-Null; break } catch { Write-Host -NoNewline "."; Start-Sleep 2 }
}
Write-Host ""
Write-Host "==> ready: http://localhost:8334"
Write-Host "    first visit: choose an admin password, it's also the password of 'This computer' and 'Clouds (rclone)'"
Write-Host "    add iCloud / Dropbox / Google Drive / OneDrive accounts: docker compose exec -it sensorium rclone config"
Start-Process http://localhost:8334
