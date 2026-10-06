# End-to-end smoke test against a running local stack.
#   docker compose up -d postgres redis api
#   pwsh ./scripts/smoke.ps1
#
# Credentials come from .env, never from the command line.

$ErrorActionPreference = 'Stop'

$base = 'http://localhost:8080'
$env_file = Join-Path $PSScriptRoot '..\.env'
$settings = @{}
Get-Content $env_file | ForEach-Object {
    if ($_ -match '^\s*([A-Z0-9_]+)\s*=\s*(.*)$') { $settings[$Matches[1]] = $Matches[2].Trim() }
}

function Step($name) { Write-Host "`n--- $name" -ForegroundColor Cyan }
function Pass($msg)  { Write-Host "  ok   $msg" -ForegroundColor Green }
function Fail($msg)  { Write-Host "  FAIL $msg" -ForegroundColor Red; exit 1 }

Step 'health'
$h = Invoke-RestMethod "$base/healthz"
if ($h.status -ne 'ok') { Fail 'healthz' }
Pass 'healthz'

$r = Invoke-RestMethod "$base/readyz"
if ($r.status -ne 'ready') { Fail 'readyz' }
Pass 'readyz reports database reachable'

Step 'unauthenticated access is refused'
try {
    Invoke-RestMethod "$base/api/v1/me" | Out-Null
    Fail '/me returned without a token'
} catch {
    if ($_.Exception.Response.StatusCode.value__ -ne 401) { Fail "expected 401, got $($_.Exception.Response.StatusCode.value__)" }
    Pass '/me returns 401 without a token'
}

Step 'login'
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$body = @{ email = $settings['SEED_ADMIN_EMAIL']; password = $settings['SEED_ADMIN_PASSWORD'] } | ConvertTo-Json
$login = Invoke-RestMethod "$base/api/v1/auth/login" -Method Post -Body $body `
    -ContentType 'application/json' -WebSession $session
if (-not $login.access_token) { Fail 'no access token returned' }
Pass "signed in as $($login.user.email) in org $($login.user.organization)"

$cookie = $session.Cookies.GetCookies("$base/api/v1/auth") | Where-Object { $_.Name -eq 'mm_refresh' }
if (-not $cookie) { Fail 'refresh cookie not set' }
if (-not $cookie.HttpOnly) { Fail 'refresh cookie is not HttpOnly' }
Pass 'refresh cookie set, HttpOnly'

$auth = @{ Authorization = "Bearer $($login.access_token)" }

Step 'identity and permissions'
$me = Invoke-RestMethod "$base/api/v1/me" -Headers $auth
Pass "$($me.user.full_name) holds $($me.permissions.Count) permissions"

Step 'user administration'
$users = Invoke-RestMethod "$base/api/v1/users" -Headers $auth
Pass "listed $($users.users.Count) user(s)"

$studentEmail = "student+$([guid]::NewGuid().ToString('N').Substring(0,8))@demo.test"
$newUser = @{
    email     = $studentEmail
    full_name = 'Test Student'
    password  = $settings['SEED_ADMIN_PASSWORD']
    roles     = @('student')
} | ConvertTo-Json
$created = Invoke-RestMethod "$base/api/v1/users" -Method Post -Headers $auth `
    -Body $newUser -ContentType 'application/json'
Pass "created student $($created.email)"

Step 'permission enforcement'
$sbody = @{ email = $studentEmail; password = $settings['SEED_ADMIN_PASSWORD'] } | ConvertTo-Json
$slogin = Invoke-RestMethod "$base/api/v1/auth/login" -Method Post -Body $sbody -ContentType 'application/json'
$sauth = @{ Authorization = "Bearer $($slogin.access_token)" }
try {
    Invoke-RestMethod "$base/api/v1/users" -Headers $sauth | Out-Null
    Fail 'student could list users'
} catch {
    if ($_.Exception.Response.StatusCode.value__ -ne 403) { Fail "expected 403, got $($_.Exception.Response.StatusCode.value__)" }
    Pass 'student is denied user.view'
}

Step 'refresh token rotation'
$refreshed = Invoke-RestMethod "$base/api/v1/auth/refresh" -Method Post -WebSession $session
if (-not $refreshed.access_token) { Fail 'refresh returned no token' }
Pass 'refresh issued a new access token'

Step 'refresh token reuse detection'
$stale = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$stale.Cookies.Add($cookie)
try {
    Invoke-RestMethod "$base/api/v1/auth/refresh" -Method Post -WebSession $stale | Out-Null
    Fail 'replayed refresh token was accepted'
} catch {
    if ($_.Exception.Response.StatusCode.value__ -ne 401) { Fail "expected 401, got $($_.Exception.Response.StatusCode.value__)" }
    Pass 'replayed refresh token rejected and family revoked'
}

try {
    Invoke-RestMethod "$base/api/v1/auth/refresh" -Method Post -WebSession $session | Out-Null
    Fail 'rotated token still works after reuse detection'
} catch {
    Pass 'whole token family revoked after replay'
}

Step 'login rate limit'
$denied = $false
foreach ($i in 1..12) {
    try {
        $bad = @{ email = 'nobody@demo.test'; password = 'wrong-password-value' } | ConvertTo-Json
        Invoke-RestMethod "$base/api/v1/auth/login" -Method Post -Body $bad -ContentType 'application/json' | Out-Null
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -eq 429) { $denied = $true; break }
    }
}
if (-not $denied) { Fail 'rate limiter never triggered' }
Pass 'login rate limiter engaged'

Write-Host "`nAll smoke checks passed." -ForegroundColor Green
