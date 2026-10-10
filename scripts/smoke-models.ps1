# Model configuration smoke test against a running local stack.
#   docker compose up -d postgres redis api
#   pwsh ./scripts/smoke-models.ps1
#
# Proves the platform routes, the permission boundary, the credential
# write-only rule, and that a connection test reports a provider failure as a
# result rather than as a server error.
#
# Credentials come from .env, never from the command line.

$ErrorActionPreference = 'Stop'

$base = if ($env:STAGING_URL) { $env:STAGING_URL.TrimEnd('/') } else { 'http://localhost:8080' }
$env_file = Join-Path $PSScriptRoot '..\.env'
$settings = @{}
Get-Content $env_file | ForEach-Object {
    if ($_ -match '^\s*([A-Z0-9_]+)\s*=\s*(.*)$') { $settings[$Matches[1]] = $Matches[2].Trim() }
}

function Step($name) { Write-Host "`n--- $name" -ForegroundColor Cyan }
function Pass($msg)  { Write-Host "  ok   $msg" -ForegroundColor Green }
function Fail($msg)  { throw $msg }

function Login($email, $password) {
    $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    $body = @{ email = $email; password = $password } | ConvertTo-Json
    $login = Invoke-RestMethod "$base/api/v1/auth/login" -Method Post -Body $body `
        -ContentType 'application/json' -WebSession $session
    return @{ Authorization = "Bearer $($login.access_token)" }
}

function StatusOf($err) { $err.Exception.Response.StatusCode.value__ }

Step 'sign in'
$super = Login $settings['SEED_SUPERADMIN_EMAIL'] $settings['SEED_SUPERADMIN_PASSWORD']
Pass 'platform operator signed in'

$teacher = $null
if ($settings['SEED_DEMO_PASSWORD']) {
    $teacher = Login 'teacher@demo.test' $settings['SEED_DEMO_PASSWORD']
    Pass 'teacher signed in'
}

Step 'the platform routes are closed to an organisation role'
if ($teacher) {
    try {
        Invoke-RestMethod "$base/api/v1/platform/providers" -Headers $teacher | Out-Null
        Fail 'a teacher read the provider list'
    } catch {
        if ((StatusOf $_) -ne 403) { Fail "expected 403, got $(StatusOf $_)" }
        Pass 'a teacher is refused with 403'
    }
}

$savedBindings = @((Invoke-RestMethod "$base/api/v1/platform/bindings" -Headers $super).bindings | Select-Object tier,provider_id,model,temperature,top_p,max_tokens,timeout_ms)
try {
Step 'create a provider'
$suffix = [guid]::NewGuid().ToString('N').Substring(0, 8)
$body = @{
    key      = "smoke_$suffix"
    name     = "Smoke provider $suffix"
    kind     = 'openai_compatible'
    base_url = 'http://127.0.0.1:9/v1'   # discard port: always refuses a connection
    api_key  = 'sk-smoke-token-abcd1234'
} | ConvertTo-Json

$provider = Invoke-RestMethod "$base/api/v1/platform/providers" -Method Post -Headers $super `
    -Body $body -ContentType 'application/json'
if (-not $provider.id) { Fail 'no provider id returned' }
Pass "created $($provider.key)"

Step 'the credential is write-only'
$raw = Invoke-RestMethod "$base/api/v1/platform/providers" -Headers $super | ConvertTo-Json -Depth 6
if ($raw -match 'sk-smoke-token-abcd1234') { Fail 'the API key came back in a response' }
if (-not $provider.has_api_key) { Fail 'the key was not stored' }
if ($provider.api_key_hint -ne '****1234') { Fail "hint is $($provider.api_key_hint)" }
Pass 'only the last four characters are readable'

Step 'bind the judge tier'
$body = @{ bindings = @(@{
    tier        = 'judge'
    provider_id = $provider.id
    model       = 'qwen3:8b'
    temperature = 0.4
    top_p       = 0.95
    max_tokens  = 512
    timeout_ms  = 3000
}) } | ConvertTo-Json -Depth 4

$bindings = Invoke-RestMethod "$base/api/v1/platform/bindings" -Method Put -Headers $super `
    -Body $body -ContentType 'application/json'
$judge = $bindings.bindings | Where-Object { $_.tier -eq 'judge' }
if (-not $judge) { Fail 'the judge binding did not come back' }
Pass "judge routed to $($judge.provider_key) / $($judge.model)"

Step 'invalid parameters are refused before the database sees them'
$body = @{ bindings = @(@{
    tier = 'judge'; provider_id = $provider.id; model = 'qwen3:8b'
    temperature = 9; top_p = 0.95; max_tokens = 512; timeout_ms = 3000
}) } | ConvertTo-Json -Depth 4
try {
    Invoke-RestMethod "$base/api/v1/platform/bindings" -Method Put -Headers $super `
        -Body $body -ContentType 'application/json' | Out-Null
    Fail 'temperature 9 was accepted'
} catch {
    if ((StatusOf $_) -ne 422) { Fail "expected 422, got $(StatusOf $_)" }
    Pass 'temperature out of range returns 422'
}

Step 'a bound provider cannot be deleted'
try {
    Invoke-RestMethod "$base/api/v1/platform/providers/$($provider.id)" -Method Delete -Headers $super | Out-Null
    Fail 'a bound provider was deleted'
} catch {
    if ((StatusOf $_) -ne 409) { Fail "expected 409, got $(StatusOf $_)" }
    Pass 'deleting a bound provider returns 409'
}

Step 'a connection test reports the provider failure as a result'
$test = Invoke-RestMethod "$base/api/v1/platform/test" -Method Post -Headers $super `
    -Body (@{ tier = 'judge' } | ConvertTo-Json) -ContentType 'application/json'
if ($test.ok) { Fail 'an unreachable endpoint reported ok' }
if ($test.reachable) { Fail 'an unreachable endpoint reported reachable' }
if (-not $test.error) { Fail 'no error text returned' }
Pass "unreachable endpoint reported: $($test.error)"

Step 'AI actors are versioned, not overwritten'
$key = "smoke_judge_$suffix"
$body = @{
    key = $key; name = 'Smoke Judge'; role = 'judge'; model_tier = 'judge'
    system_prompt = 'You are an appellate judge.'; temperature = 0.4
    capabilities = @('ask_question', 'evaluate'); focus = @('precedent')
} | ConvertTo-Json -Depth 4

$profile = Invoke-RestMethod "$base/api/v1/ai-profiles" -Method Post -Headers $super `
    -Body $body -ContentType 'application/json'
if ($profile.version -ne 1) { Fail "first version is $($profile.version)" }
Pass "created $key at version 1"

$body = @{
    key = $key; name = 'Smoke Judge'; role = 'judge'; model_tier = 'judge'
    system_prompt = 'You are a strict appellate judge.'; temperature = 0.6
    capabilities = @('ask_question', 'interrupt', 'evaluate'); focus = @('precedent')
} | ConvertTo-Json -Depth 4

$revised = Invoke-RestMethod "$base/api/v1/ai-profiles/$($profile.id)/versions" -Method Post `
    -Headers $super -Body $body -ContentType 'application/json'
if ($revised.version -ne 2) { Fail "revision is version $($revised.version)" }
Pass 'revising produced version 2'

$all = Invoke-RestMethod "$base/api/v1/ai-profiles" -Headers $super
# @() because Windows PowerShell does not give a lone object a Count.
$versions = @($all.profiles | Where-Object { $_.key -eq $key })
if ($versions.Count -ne 2) { Fail "expected 2 versions on record, found $($versions.Count)" }
$active = @($versions | Where-Object { $_.is_active })
if ($active.Count -ne 1) { Fail "expected one active version, found $($active.Count)" }
if ($active[0].version -ne 2) { Fail "the active version is $($active[0].version), expected 2" }
Pass 'version 1 is retired and still on record'

Step 'an unknown capability is refused'
$body = @{
    key = "smoke_bad_$suffix"; name = 'Bad'; role = 'judge'; model_tier = 'judge'
    system_prompt = ''; temperature = 0.4; capabilities = @('delete_everything')
} | ConvertTo-Json -Depth 4
try {
    Invoke-RestMethod "$base/api/v1/ai-profiles" -Method Post -Headers $super `
        -Body $body -ContentType 'application/json' | Out-Null
    Fail 'an unenforced capability was accepted'
} catch {
    if ((StatusOf $_) -ne 422) { Fail "expected 422, got $(StatusOf $_)" }
    Pass 'a capability the coordinator does not enforce returns 422'
}

} finally {
    # Restore the exact routing that existed before this check, even on failure.
    $body = @{ bindings = $savedBindings } | ConvertTo-Json -Depth 4
    Invoke-RestMethod "$base/api/v1/platform/bindings" -Method Put -Headers $super -Body $body -ContentType 'application/json' | Out-Null
    if ($provider.id) { Invoke-RestMethod "$base/api/v1/platform/providers/$($provider.id)" -Method Delete -Headers $super | Out-Null }
}
Write-Host "All model configuration checks passed." -ForegroundColor Green
