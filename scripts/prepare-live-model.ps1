# Creates a live alias from installed weights. No model download is required.
param([string]$SourceModel = 'qwen3:4b-instruct-2507-q4_K_M', [string]$Model = 'qwen3-live', [string]$OllamaUrl = 'http://localhost:11434')
$ErrorActionPreference = 'Stop'
$source = Invoke-RestMethod -Uri "$OllamaUrl/api/show" -Method Post -ContentType 'application/json' -Body (@{ model = $SourceModel } | ConvertTo-Json)
if ($source.model_info.'general.finetune' -match 'Thinking' -or ($source.thinking -and $false -notin $source.thinking.values)) { throw 'This model requires thinking. Use the Qwen3 Instruct variant for live judging.' }
$created = Invoke-RestMethod -Uri "$OllamaUrl/api/create" -Method Post -ContentType 'application/json' -TimeoutSec 60 -Body (@{ model = $Model; from = $SourceModel; stream = $false } | ConvertTo-Json -Depth 5)
if ($created.status -ne 'success') { throw 'The live model alias could not be created.' }
$probe = @{ model = $Model; messages = @(@{ role = 'user'; content = 'Return only JSON {"ready":true}. /no_think' }); max_tokens = 40; temperature = 0; reasoning_effort = 'none' }
$watch = [Diagnostics.Stopwatch]::StartNew()
$result = Invoke-RestMethod -Uri "$OllamaUrl/v1/chat/completions" -Method Post -ContentType 'application/json' -TimeoutSec 30 -Body ($probe | ConvertTo-Json -Depth 5)
$watch.Stop()
$reply = $result.choices[0].message.content | ConvertFrom-Json
if (-not $reply.ready -or $result.choices[0].finish_reason -eq 'length') { throw 'The live model did not produce a usable answer.' }
Write-Output ("Prepared {0}. JSON check passed in {1:N2}s. Bind monitor and judge to this model in Model settings." -f $Model, $watch.Elapsed.TotalSeconds)
