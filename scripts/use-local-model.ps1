# Points selected tiers at an Ollama server on this host.
#
#   pwsh scripts/use-local-model.ps1
param([string]$Model = 'qwen3-nt', [string]$BaseUrl = 'http://host.docker.internal:11434/v1', [ValidateSet('monitor','judge','grader')][string[]]$Tiers = @('grader'))
$ErrorActionPreference = 'Stop'

$settings = @{}
Get-Content (Join-Path $PSScriptRoot '..\.env') | ForEach-Object {
    if ($_ -match '^([A-Z0-9_]+)=(.*)$') { $settings[$Matches[1]] = $Matches[2].Trim() }
}
$base = 'http://localhost:8080/api/v1'
function Call($method, $path, $auth, $body = $null) {
    $args = @{ Method = $method; Uri = "$base$path"; Headers = $auth; ContentType = 'application/json' }
    if ($null -ne $body) { $args.Body = $body | ConvertTo-Json -Depth 15 }
    Invoke-RestMethod @args
}
$r = Call POST '/auth/login' @{} @{ email = $settings.SEED_SUPERADMIN_EMAIL; password = $settings.SEED_SUPERADMIN_PASSWORD }
$su = @{ Authorization = "Bearer $($r.access_token)" }

$providers = (Call GET '/platform/providers' $su).providers
$local = $providers | Where-Object key -eq 'local_ollama'
if (-not $local) {
    $local = Call POST '/platform/providers' $su @{ key = 'local_ollama'; name = 'Local Ollama'; kind = 'openai_compatible'; base_url = $BaseUrl; api_key = '' }
} else {
    $local = Call PATCH "/platform/providers/$($local.id)" $su @{ base_url = $BaseUrl; is_active = $true }
}

$existing = (Call GET '/platform/bindings' $su).bindings
$wanted = $Tiers
$payload = foreach ($b in $existing) {
    @{
        tier        = $b.tier
        provider_id = if ($wanted -contains $b.tier) { $local.id } else { $b.provider_id }
        model       = if ($wanted -contains $b.tier) { $Model } else { $b.model }
        temperature = $b.temperature
        top_p       = $b.top_p
        max_tokens  = $b.max_tokens
        timeout_ms  = $b.timeout_ms
        reasoning_effort = $b.reasoning_effort
    }
}
$out = Call PUT '/platform/bindings' $su @{ bindings = @($payload) }
$out.bindings | ForEach-Object { Write-Output ("  {0,-10} {1,-14} {2}" -f $_.tier, $_.provider_key, $_.model) }

Write-Output ''
foreach ($tier in $wanted) {
    $t = Call POST '/platform/test' $su @{ tier = $tier }
    Write-Output ("  {0,-8} ok={1} {2}ms  {3}" -f $tier, $t.ok, $t.latency_ms, ($t.sample + $t.error).Substring(0, [Math]::Min(80, ($t.sample + $t.error).Length)))
}
