# Submission smoke test: upload, extraction through the AI plane, the
# deterministic structure check, locking, and the stage advancing on submit.
#
#   pwsh ./scripts/smoke-submissions.ps1

$ErrorActionPreference = 'Stop'

$base = if ($env:STAGING_URL) { $env:STAGING_URL.TrimEnd('/') } else { 'http://localhost:8080' }
$envFile = Join-Path $PSScriptRoot '..\.env'
$settings = @{}
Get-Content $envFile | ForEach-Object {
    if ($_ -match '^\s*([A-Z0-9_]+)\s*=\s*(.*)$') { $settings[$Matches[1]] = $Matches[2].Trim() }
}
$password = $settings['SEED_ADMIN_PASSWORD']

function Step($name) { Write-Host "`n--- $name" -ForegroundColor Cyan }
function Pass($msg)  { Write-Host "  ok   $msg" -ForegroundColor Green }
function Fail($msg)  { Write-Host "  FAIL $msg" -ForegroundColor Red; exit 1 }

function Expect-Status($status, $block, $what) {
    try { & $block | Out-Null; Fail $what }
    catch {
        $got = $_.Exception.Response.StatusCode.value__
        if ($got -ne $status) { Fail "expected $status, got $got ($what)" }
    }
}

function New-Student($label) {
    $email = "$label+$([guid]::NewGuid().ToString('N').Substring(0,6))@demo.test"
    $body = @{ email = $email; full_name = $label; password = $password; roles = @('student') } | ConvertTo-Json
    $u = Invoke-RestMethod "$base/api/v1/users" -Method Post -Headers $script:auth -Body $body -ContentType 'application/json'
    $u | Add-Member -NotePropertyName email -NotePropertyValue $email -Force
    $u
}

function Get-Token($email) {
    $r = Invoke-RestMethod "$base/api/v1/auth/login" -Method Post -ContentType 'application/json' `
        -Body (@{ email = $email; password = $password } | ConvertTo-Json)
    @{ Authorization = "Bearer $($r.access_token)" }
}

# Invoke-RestMethod's -Form is awkward across PowerShell versions, so the
# multipart body is built by hand.
function Send-Upload($url, $headers, $path, $filename, $contentType) {
    $boundary = [guid]::NewGuid().ToString()
    $bytes = [System.IO.File]::ReadAllBytes($path)
    $enc = [System.Text.Encoding]::UTF8

    $head = "--$boundary`r`nContent-Disposition: form-data; name=`"file`"; filename=`"$filename`"`r`nContent-Type: $contentType`r`n`r`n"
    $tail = "`r`n--$boundary--`r`n"

    $body = New-Object System.IO.MemoryStream
    $body.Write($enc.GetBytes($head), 0, $enc.GetByteCount($head))
    $body.Write($bytes, 0, $bytes.Length)
    $body.Write($enc.GetBytes($tail), 0, $enc.GetByteCount($tail))

    Invoke-RestMethod $url -Method Post -Headers $headers -Body $body.ToArray() `
        -ContentType "multipart/form-data; boundary=$boundary"
}

Step 'sign in'
$script:auth = Get-Token $settings['SEED_ADMIN_EMAIL']
Pass 'signed in as administrator'

Step 'a template whose submission stage opens immediately'
$rubrics = Invoke-RestMethod "$base/api/v1/rubrics" -Headers $auth
$rubric = $rubrics.rubrics | Where-Object { $_.key -eq 'moot_standard_v1' }
$allCriteria = @($rubric.criteria | ForEach-Object { $_.key })

$templates = Invoke-RestMethod "$base/api/v1/templates" -Headers $auth
$seeded = $templates.templates | Where-Object { $_.key -eq 'moot_court_standard' }
$seededVersion = Invoke-RestMethod "$base/api/v1/templates/versions/$($seeded.versions[0].id)" -Headers $auth

$tplKey = "smoke_upload_$([guid]::NewGuid().ToString('N').Substring(0,8))"
$tpl = Invoke-RestMethod "$base/api/v1/templates" -Method Post -Headers $auth -ContentType 'application/json' -Body (@{
    assessment_type = 'moot_court'
    key             = $tplKey
    name            = 'Smoke: immediate memorial'
    description     = 'Submission stage opens at once so the upload path can be exercised'
} | ConvertTo-Json)

$versionBody = @{
    rubric_id     = $rubric.id
    participation = $seededVersion.participation
    stages        = @(
        @{ id = 'memorial'; kind = 'artifact_submission'; label = 'Written Memorial'
           opens_after_s = 0; due_after_s = 86400
           config = @{ formats = @('pdf', 'docx', 'txt'); max_bytes = 26214400
                       lock_on_submit = $true; format_rules = 'indian_national_v1' } },
        @{ id = 'evaluation'; kind = 'automated_evaluation'; label = 'Evaluation'
           config = @{ rubric_scope = $allCriteria } },
        @{ id = 'moderation'; kind = 'human_review'; label = 'Teacher Review'
           config = @{ required = $true; overrides_allowed = $true } }
    )
} | ConvertTo-Json -Depth 10

$version = Invoke-RestMethod "$base/api/v1/templates/$($tpl.id)/versions" -Method Post -Headers $auth -Body $versionBody -ContentType 'application/json'
$version = Invoke-RestMethod "$base/api/v1/templates/versions/$($version.id)/publish" -Method Post -Headers $auth
Pass "template v$($version.version) published with $($version.stages.Count) stages"

Step 'team and assignment'
$s1 = New-Student 'Speaker One'
$s2 = New-Student 'Speaker Two'
$stranger = New-Student 'Unrelated Student'

$team = Invoke-RestMethod "$base/api/v1/teams" -Method Post -Headers $auth -ContentType 'application/json' -Body (@{
    name    = "Team $([guid]::NewGuid().ToString('N').Substring(0,6))"
    members = @(
        @{ user_id = $s1.id; role = 'speaker'; speaking_order = 1 },
        @{ user_id = $s2.id; role = 'speaker'; speaking_order = 2 }
    )
} | ConvertTo-Json -Depth 5)

$assessment = Invoke-RestMethod "$base/api/v1/assessments" -Method Post -Headers $auth -ContentType 'application/json' -Body (@{
    template_version_id = $version.id
    title               = 'Devgarh Telecom Suspension, upload round'
    opens_at            = (Get-Date).ToUniversalTime().AddSeconds(2).ToString('o')
} | ConvertTo-Json)
$assessment = Invoke-RestMethod "$base/api/v1/assessments/$($assessment.id)/publish" -Method Post -Headers $auth

$assignment = Invoke-RestMethod "$base/api/v1/assessments/$($assessment.id)/assignments" -Method Post `
    -Headers $auth -ContentType 'application/json' -Body (@{ team_id = $team.id; side = 'applicant' } | ConvertTo-Json)
Pass "assignment created with $($assignment.stages.Count) stages"

Step 'wait for the submission stage to open'
$memorial = $null
foreach ($i in 1..12) {
    Start-Sleep -Seconds 2
    $detail = Invoke-RestMethod "$base/api/v1/assignments/$($assignment.id)" -Headers $auth
    $memorial = $detail.stages | Where-Object { $_.stage_id -eq 'memorial' }
    if ($memorial.status -eq 'active') { break }
}
if ($memorial.status -ne 'active') { Fail "memorial stage never opened (status '$($memorial.status)')" }
Pass "memorial stage is active, due $($memorial.due_at)"

Step 'build a compliant memorial'
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) "memorial-$([guid]::NewGuid().ToString('N').Substring(0,8)).txt"
$filler = ('submission ' * 120).Trim()
@"
COVER PAGE
Team Code TC-14
Before the Supreme Court of Ardhanari

TABLE OF CONTENTS
Index of Authorities
Statement of Jurisdiction

INDEX OF AUTHORITIES
Anuradha Bhasin v. Union of India, (2020) 3 SCC 637
Modern Dental College v. State of M.P., (2016) 7 SCC 353

STATEMENT OF JURISDICTION
The Respondents approach this Court under Article 136 of the Constitution.

STATEMENT OF FACTS
$filler

STATEMENT OF ISSUES
I. Whether the suspension satisfies proportionality.

SUMMARY OF ARGUMENTS
$filler

ARGUMENTS ADVANCED
$filler

PRAYER
Wherefore it is prayed that this Court may set aside the impugned order.
"@ | Set-Content -Path $tmp -Encoding utf8

$uploadUrl = "$base/api/v1/assignments/$($assignment.id)/stages/memorial/submissions"
$s1Auth = Get-Token $s1.email

Step 'access control on upload'
$strangerAuth = Get-Token $stranger.email
Expect-Status 404 { Send-Upload $uploadUrl $strangerAuth $tmp 'memorial.txt' 'text/plain' } 'a student outside the team uploaded a submission'
Pass 'a non-member cannot upload'

Step 'unsupported content is refused by its bytes'
$badPath = Join-Path ([System.IO.Path]::GetTempPath()) "bad-$([guid]::NewGuid().ToString('N').Substring(0,8)).pdf"
[System.IO.File]::WriteAllBytes($badPath, [byte[]](0x00, 0x01, 0x02, 0x03, 0x04, 0x05))
Expect-Status 415 { Send-Upload $uploadUrl $s1Auth $badPath 'memorial.pdf' 'application/pdf' } 'binary junk named .pdf was accepted'
Pass 'a file is identified by its bytes, not its name'

Step 'upload'
$artifact = Send-Upload $uploadUrl $s1Auth $tmp 'memorial.txt' 'text/plain'
Pass "stored version $($artifact.version), $($artifact.byte_size) bytes, $($artifact.word_count) words"
if ($artifact.is_late) { Fail 'submission before the deadline was marked late' }
if (-not $artifact.locked_at) { Fail 'lock_on_submit did not lock the submission' }
Pass "locked at $($artifact.locked_at)"

Step 'structure was checked deterministically'
$c = $artifact.compliance
if (-not $c) { Fail 'no compliance report returned' }
Pass "rule set '$($c.rule_set_key)': $($c.passed) passed, $($c.failed) failed, $($c.warnings) warnings"
if ($c.sections.Count -ne 9) { Fail "expected 9 detected sections, got $($c.sections.Count)" }
Pass "detected sections: $(($c.sections | ForEach-Object { $_.key }) -join ', ')"
$pageRule = $c.findings | Where-Object { $_.rule -eq 'document.page_limit' }
if ($pageRule.status -ne 'not_checkable') { Fail "a text file cannot report pages; got '$($pageRule.status)'" }
Pass 'page limit reported as not checkable for a format with no page count'
if ($c.failed -ne 0) {
    ($c.findings | Where-Object { $_.status -eq 'fail' }) | ForEach-Object { Write-Host "       $($_.rule) $($_.section): $($_.message)" }
    Fail 'a compliant memorial reported failures'
}
Pass 'no structural failures'

Step 'locking holds'
Expect-Status 409 { Send-Upload $uploadUrl $s1Auth $tmp 'memorial.txt' 'text/plain' } 'a locked stage accepted a second submission'
Pass 'a locked submission cannot be replaced'

Step 'submitting advanced the workflow'
$detail = Invoke-RestMethod "$base/api/v1/assignments/$($assignment.id)" -Headers $auth
$memorial = $detail.stages | Where-Object { $_.stage_id -eq 'memorial' }
if ($memorial.status -ne 'completed') { Fail "memorial should be completed, got '$($memorial.status)'" }
Pass "memorial completed at $($memorial.completed_at)"

$next = $detail.stages | Where-Object { $_.stage_id -eq 'evaluation' }
if ($next.status -ne 'active') { Fail "evaluation should have been activated, got '$($next.status)'" }
Pass "the following evaluation stage activated without a timer"

Step 'reading submissions back'
$mine = Invoke-RestMethod "$base/api/v1/assignments/$($assignment.id)/submissions" -Headers $s1Auth
if ($mine.submissions.Count -ne 1) { Fail "expected 1 submission, got $($mine.submissions.Count)" }
Pass "the team sees $($mine.submissions.Count) submission"

Expect-Status 404 { Invoke-RestMethod "$base/api/v1/assignments/$($assignment.id)/submissions" -Headers $strangerAuth } 'an unrelated student listed another team''s submissions'
Pass 'an unrelated student cannot list them'

$downloaded = Invoke-WebRequest "$base/api/v1/submissions/$($artifact.id)/download" -Headers $auth -UseBasicParsing
if ($downloaded.StatusCode -ne 200) { Fail 'download failed' }
if ($downloaded.Content.Length -lt 100) { Fail 'downloaded file is suspiciously small' }
Pass "downloaded $($downloaded.RawContentLength) bytes as a teacher"

Expect-Status 404 { Invoke-WebRequest "$base/api/v1/submissions/$($artifact.id)/download" -Headers $strangerAuth -UseBasicParsing } 'an unrelated student downloaded a submission'
Pass 'an unrelated student cannot download it'

Remove-Item $tmp, $badPath -ErrorAction SilentlyContinue
Write-Host "`nAll submission checks passed." -ForegroundColor Green
