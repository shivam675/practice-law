param([switch]$Speech, [switch]$Regrade)
$ErrorActionPreference = 'Stop'
$settings = @{}
Get-Content (Join-Path $PSScriptRoot '..\.env') | ForEach-Object { if ($_ -match '^([A-Z0-9_]+)=(.*)$') { $settings[$Matches[1]] = $Matches[2].Trim() } }
$base = 'http://localhost:8080/api/v1'
function Call($method, $path, $auth, $body = $null) {
    $args = @{ Method=$method; Uri="$base$path"; Headers=$auth; ContentType='application/json' }
    if ($null -ne $body) { $args.Body = $body | ConvertTo-Json -Depth 15 }
    Invoke-RestMethod @args
}
function Login($email,$password) { $r=Call POST '/auth/login' @{} @{email=$email;password=$password}; @{Authorization="Bearer $($r.access_token)"} }
function Check($condition,$message) { if (-not $condition) { throw $message }; Write-Output "PASS: $message" }
function Denied($code,$action) { try { & $action | Out-Null; throw 'Unexpected success' } catch { if ($_.Exception.Response.StatusCode.value__ -ne $code) { throw } }; Write-Output "PASS: request refused ($code)" }
$admin=Login $settings.SEED_ADMIN_EMAIL $settings.SEED_ADMIN_PASSWORD
$student=Login 'student1@demo.test' $settings.SEED_DEMO_PASSWORD
$other=Login 'student2@demo.test' $settings.SEED_DEMO_PASSWORD
$teacher=Login 'teacher@demo.test' $settings.SEED_DEMO_PASSWORD
$suffix=[guid]::NewGuid().ToString('N').Substring(0,8)
$users=(Call GET '/users' $admin).users
$s1=$users | Where-Object email -eq 'student1@demo.test'
$team=Call POST '/teams' $admin @{name="End-to-end $suffix";members=@(@{user_id=$s1.id;role='speaker';speaking_order=1})}
$rubric=Call POST '/rubrics' $admin @{key="e2e_$suffix";name='Development reasoning check';criteria=@(@{key='reasoning';name='Reasoning';description='Clear reasoning supported by the supplied record';weight=100;max_score=10;scope=@('written','oral');guidance='Use a verbatim quote as evidence.'})}
$template=Call POST '/templates' $admin @{key="e2e_$suffix";name='Development full assessment';assessment_type='moot_court'}
$version=Call POST "/templates/$($template.id)/versions" $admin @{
 rubric_id=$rubric.id; participation=@{sides=@('applicant');speakers=1;min_team_size=1;max_team_size=1;ai_actors=@(@{profile_key='presiding_judge';role='judge';display_name='Presiding Judge';presiding=$true})}
 stages=@(
 @{id='written';kind='artifact_submission';label='Written response';due_after_s=900;config=@{formats=@('txt');max_bytes=100000;lock_on_submit=$true}},
 @{id='oral';kind='live_turn';label='Oral response';config=@{duration_s=600;speaker_order=1;interruptions='enabled';ai_profiles=@('presiding_judge')}},
 @{id='evaluation';kind='automated_evaluation';label='Evaluation';config=@{rubric_scope=@('reasoning');sources=@('written','oral')}},
 @{id='review';kind='human_review';label='Teacher review';config=@{required=$true;overrides_allowed=$true}}
 )
}
Call POST "/templates/versions/$($version.id)/publish" $admin | Out-Null
$assessment=Call POST '/assessments' $admin @{template_version_id=$version.id;title="End-to-end verification $suffix";opens_at=(Get-Date).ToUniversalTime().AddSeconds(2).ToString('o')}
Call POST "/assessments/$($assessment.id)/publish" $admin | Out-Null
$assignment=Call POST "/assessments/$($assessment.id)/assignments" $admin @{team_id=$team.id;side='applicant'}
Write-Output "Assignment: $($assignment.id)"
foreach($i in 1..20){$detail=Call GET "/assignments/$($assignment.id)" $admin;if(($detail.stages|Where-Object stage_id -eq 'oral').status -eq 'active'){break};Start-Sleep -Seconds 1}
Check (($detail.stages|Where-Object stage_id -eq 'oral').status -eq 'active') 'Scheduled oral stage opened'
$boundary=[guid]::NewGuid().ToString()
$text='The decision must be supported by evidence. The record states that four identical orders were issued without a fresh review. Repeating an order does not demonstrate why a less restrictive measure was unavailable. The applicant therefore asks for a reasoned review rather than an automatic renewal.'
$body="--$boundary`r`nContent-Disposition: form-data; name=`"file`"; filename=`"response.txt`"`r`nContent-Type: text/plain`r`n`r`n$text`r`n--$boundary--`r`n"
Invoke-RestMethod "$base/assignments/$($assignment.id)/stages/written/submissions" -Method Post -Headers $student -ContentType "multipart/form-data; boundary=$boundary" -Body ([Text.Encoding]::UTF8.GetBytes($body)) | Out-Null
Check $true 'Written source uploaded'
$detail=Call GET "/assignments/$($assignment.id)" $admin
Check (($detail.stages|Where-Object stage_id -eq 'evaluation').status -eq 'pending') 'Evaluation waits for the active oral stage'
Denied 400 { Call POST "/assignments/$($assignment.id)/stages/oral/session" $student @{consent=$false} }
Denied 404 { Call POST "/assignments/$($assignment.id)/stages/oral/session" $other @{consent=$true} }
$joined=Call POST "/assignments/$($assignment.id)/stages/oral/session" $student @{consent=$true}
$rejoined=Call POST "/assignments/$($assignment.id)/stages/oral/session" $student @{consent=$true}
Check ($joined.id -eq $rejoined.id) 'Reconnect reuses the same session'
if ($Speech) {
    $docker=(Get-Command docker -ErrorAction SilentlyContinue).Source
    if (-not $docker) { $docker=Join-Path $env:LOCALAPPDATA 'Programs\DockerDesktop\resources\bin\docker.exe' }
    @{ticket=$joined.ticket;text=$text} | ConvertTo-Json -Compress | & $docker compose exec -T media python smoke_speech.py
    if ($LASTEXITCODE -ne 0) { throw 'Real speech check failed' }
}
$mediaSecret=$settings.MEDIA_SERVICE_TOKEN
if (-not $mediaSecret) { $mediaSecret='local_speech_development_only_change_me' }
$ticket=@{Authorization="Bearer $($joined.ticket)";'X-Media-Service-Token'=$mediaSecret}
$turn=@{id=[guid]::NewGuid().ToString();text=$text;duration_ms=15000}
$before=Call GET "/sessions/$($joined.id)" $student
$beforeCount=@($before.transcript | Where-Object speaker -ne 'Examiner').Count
Denied 401 { Call POST '/media/turn' @{Authorization="Bearer $($joined.ticket)"} $turn }
$reply=Call POST '/media/turn' $ticket $turn
Check $reply.saved 'Transcript persisted'
Call POST '/media/turn' $ticket $turn | Out-Null
$live=Call GET "/sessions/$($joined.id)" $student
Check (@($live.transcript | Where-Object speaker -ne 'Examiner').Count -eq ($beforeCount+1)) 'Duplicate turn is not stored twice'
$watch=[Diagnostics.Stopwatch]::StartNew()
$question=Call POST '/media/question' $ticket
$watch.Stop()
$live=Call GET "/sessions/$($joined.id)" $student
Check ([bool]$question.question -or @($live.transcript | Where-Object speaker -eq 'Examiner').Count -gt 0) 'Configured judge returned a question'
Check (@($live.transcript | Where-Object speaker -eq 'Presiding Judge').Count -gt 0) 'Configured AI profile supplied the judge question'
Check ($watch.Elapsed.TotalSeconds -lt 5) "Question path bounded: $($watch.Elapsed.TotalSeconds.ToString('F2')) seconds"
Denied 404 {Call GET "/sessions/$($joined.id)" $other}
$hidden=Call GET "/assignments/$($assignment.id)/report" $student
Check ($hidden.status -eq 'unpublished' -and $hidden.scores.Count -eq 0) 'Draft scores are hidden from the student'
Call POST "/sessions/$($joined.id)/end" $student | Out-Null
Denied 409 {Call POST '/media/turn' $ticket @{id=[guid]::NewGuid().ToString();text='late turn';duration_ms=1000}}
foreach($i in 1..300){$report=Call GET "/assignments/$($assignment.id)/report" $teacher;if($report.scores.Count -gt 0){break};Start-Sleep -Seconds 2}
Check ($report.scores.Count -eq 1) 'Configured source stages graded'
Denied 403 {Call POST "/assignments/$($assignment.id)/report/publish" $student @{notes='Student cannot publish'}}
$score=$report.scores[0]
if ($Regrade) {
    Denied 403 {Call POST "/assignments/$($assignment.id)/report/regrade" $student @{score_id=$score.id;reason='Not permitted'}}
    Denied 400 {Call POST "/assignments/$($assignment.id)/report/regrade" $teacher @{score_id=$score.id;reason=''}}
    Call POST "/assignments/$($assignment.id)/report/regrade" $teacher @{score_id=$score.id;reason='Development verification of one-criterion regrading.'} | Out-Null
    $report=Call GET "/assignments/$($assignment.id)/report" $teacher
    Check ($report.scores.Count -eq 1 -and $report.scores[0].id -ne $score.id) 'Regrade replaced the criterion in a complete new evaluation'
    Denied 409 {Call POST "/assignments/$($assignment.id)/report/regrade" $teacher @{score_id=$score.id;reason='Stale score must be rejected'}}
    $score=$report.scores[0]
}
Call PATCH "/assignments/$($assignment.id)/report/scores" $teacher @{score_id=$score.id;score=8;reason='Development check: verified the recorded reasoning.'} | Out-Null
$published=Call POST "/assignments/$($assignment.id)/report/publish" $teacher @{notes='Development smoke test. Review the record before making each claim.'}
Check ($published.total -eq 80 -and $published.maximum -eq 100) 'Teacher override updates the weighted total'
$visible=Call GET "/assignments/$($assignment.id)/report" $student
Check ($visible.status -eq 'published' -and $visible.scores.Count -eq 1) 'Student can read the published report'
Denied 404 {Call GET "/assignments/$($assignment.id)/report" $other}
Check ((Call GET "/assignments/$($assignment.id)" $teacher).status -eq 'finalized') 'Assignment finalized through the workflow engine'
Write-Output 'PASS: full control-plane assessment flow'


