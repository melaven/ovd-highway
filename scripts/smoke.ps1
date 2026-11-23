<#
.SYNOPSIS
    Smoke-checks a local OVD // Highway stack.

.DESCRIPTION
    Probes the unauthenticated endpoints (GET /healthz, /readyz, /metrics)
    and one protected Control Plane endpoint (GET /api/v1/sources) using
    an Authorization: Bearer header. Exits non-zero on any failure.

.EXAMPLE
    ./scripts/smoke.ps1
    ./scripts/smoke.ps1 -BaseUrl http://localhost:8080 -Token $env:ADMIN_API_TOKEN
#>
[CmdletBinding()]
param(
    [string]$BaseUrl = $(if ($env:SMOKE_BASE_URL) { $env:SMOKE_BASE_URL } else { 'http://localhost:8080' }),
    [string]$Token   = $(if ($env:ADMIN_API_TOKEN) { $env:ADMIN_API_TOKEN } else { '' })
)

$ErrorActionPreference = 'Stop'
$BaseUrl = $BaseUrl.TrimEnd('/')
$script:failures = 0

function Probe([string]$Name, [string]$Path, [string]$BearerToken, [scriptblock]$Validate) {
    $url = "$BaseUrl$Path"
    try {
        $headers = @{}
        if ($BearerToken) { $headers['Authorization'] = "Bearer $BearerToken" }
        $resp = Invoke-WebRequest -Uri $url -Headers $headers -UseBasicParsing -TimeoutSec 10
        $ok = & $Validate $resp
        if ($ok) {
            Write-Host "PASS  $Name  ($($resp.StatusCode))" -ForegroundColor Green
        } else {
            Write-Host "FAIL  $Name  ($($resp.StatusCode)) unexpected response body" -ForegroundColor Red
            $script:failures++
        }
    } catch {
        Write-Host "FAIL  $Name  request error: $($_.Exception.Message)" -ForegroundColor Red
        $script:failures++
    }
}

Write-Host "Smoke-testing $BaseUrl" -ForegroundColor Cyan

Probe 'healthz' '/healthz' '' {
    param($r)
    $body = $r.Content | ConvertFrom-Json
    return ($body.status -eq 'ok')
}

Probe 'readyz' '/readyz' '' {
    param($r)
    $body = $r.Content | ConvertFrom-Json
    return ($body.status -eq 'ready')
}

Probe 'metrics' '/metrics' '' {
    param($r)
    return ($r.Content -match 'ovd_uptime_seconds')
}

if ($Token) {
    Probe 'api/v1/sources (bearer)' '/api/v1/sources' $Token {
        param($r)
        return ($null -ne $r.Content)
    }
} else {
    Write-Host "SKIP  api/v1/sources - no ADMIN_API_TOKEN provided" -ForegroundColor Yellow
}

if ($script:failures -gt 0) {
    Write-Host "" 
    Write-Host "$($script:failures) probe(s) failed." -ForegroundColor Red
    exit 1
}
Write-Host ""
Write-Host "All probes passed." -ForegroundColor Green
exit 0
