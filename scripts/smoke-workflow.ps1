# Workflow smoke test: templates, rubrics, teams, assessments, assignments,
# and the durable scheduler actually firing.
#
#   pwsh ./scripts/smoke-workflow.ps1

$ErrorActionPreference = 'Stop'

$base = 'http://localhost:8080'
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
    try {
        & $block | Out-Null
        Fail $what
    } catch {
        $got = $_.Exception.Response.StatusCode.value__
        if ($got -ne $status) { Fail "expected $status, got $got ($what)" }
    }
}

function New-Student($label) {
    $email = "$label+$([guid]::NewGuid().ToString('N').Substring(0,6))@demo.test"
    $body = @{ email = $email; full_name = $label; password = $password; roles = @('student') } | ConvertTo-Json
    $u = Invoke-RestMethod "$base/api/v1/users" -Method Post -Headers $script:auth -Body $body -ContentType 'application/json'
    $u | Add-Member -NotePropertyName email -NotePropertyValue $email -Force
    return $u
}

Step 'sign in as administrator'
$loginBody = @{ email = $settings['SEED_ADMIN_EMAIL']; password = $password } | ConvertTo-Json
$login = Invoke-RestMethod "$base/api/v1/auth/login" -Method Post -Body $loginBody -ContentType 'application/json'
$script:auth = @{ Authorization = "Bearer $($login.access_token)" }
Pass "signed in as $($login.user.email)"

Step 'seeded moot template'
$templates = Invoke-RestMethod "$base/api/v1/templates" -Headers $auth
$moot = $templates.templates | Where-Object { $_.key -eq 'moot_court_standard' }
if (-not $moot) { Fail 'seeded moot template is missing' }
$version = $moot.versions | Where-Object { $_.status -eq 'published' } | Select-Object -First 1
if (-not $version) { Fail 'no published template version' }
Pass "$($moot.name) v$($version.version) published"

$full = Invoke-RestMethod "$base/api/v1/templates/versions/$($version.id)" -Headers $auth
if ($full.stages.Count -ne 8) { Fail "expected 8 stages, got $($full.stages.Count)" }
if ($full.participation.speakers -ne 2) { Fail 'expected 2 speakers per team' }
$kinds = ($full.stages | ForEach-Object { $_.kind } | Select-Object -Unique) -join ', '
Pass "$($full.stages.Count) stages over kinds: $kinds"
Pass "sides: $($full.participation.sides -join ', '); team size $($full.participation.min_team_size)-$($full.participation.max_team_size)"

$rubrics = Invoke-RestMethod "$base/api/v1/rubrics" -Headers $auth
$rubric = $rubrics.rubrics | Where-Object { $_.key -eq 'moot_standard_v1' }
if (-not $rubric) { Fail 'seeded rubric is missing' }
$weight = ($rubric.criteria | Measure-Object -Property weight -Sum).Sum
Pass "rubric: $($rubric.criteria.Count) criteria, weights total $weight"

Step 'template validation'
$badBody = @{
    rubric_id     = $rubric.id
    participation = $full.participation
    stages        = @(
        @{ id = 'oral'; kind = 'live_turn'; label = 'Oral'
           config = @{ duration_s = 999999; interruptions = 'enabled' } }
    )
} | ConvertTo-Json -Depth 10
Expect-Status 422 { Invoke-RestMethod "$base/api/v1/templates/$($moot.id)/versions" -Method Post -Headers $auth -Body $badBody -ContentType 'application/json' } 'an invalid stage list was accepted'
Pass 'over-long stage and unscored criteria rejected with 422'

Step 'team formation'
$s1 = New-Student 'Speaker One'
$s2 = New-Student 'Speaker Two'
$outsiderUser = New-Student 'Unattached Student'

$teamBody = @{
    name    = "Team $([guid]::NewGuid().ToString('N').Substring(0,6))"
    members = @(
        @{ user_id = $s1.id; role = 'speaker'; speaking_order = 1 },
        @{ user_id = $s2.id; role = 'speaker'; speaking_order = 2 }
    )
} | ConvertTo-Json -Depth 5
$team = Invoke-RestMethod "$base/api/v1/teams" -Method Post -Headers $auth -Body $teamBody -ContentType 'application/json'
Pass "$($team.name): $($team.members.Count) speakers"

$gapBody = @{
    name    = 'Gap Team'
    members = @(
        @{ user_id = $s1.id; role = 'speaker'; speaking_order = 1 },
        @{ user_id = $s2.id; role = 'speaker'; speaking_order = 3 }
    )
} | ConvertTo-Json -Depth 5
Expect-Status 422 { Invoke-RestMethod "$base/api/v1/teams" -Method Post -Headers $auth -Body $gapBody -ContentType 'application/json' } 'a gap in speaking order was accepted'
Pass 'speaking order must run 1..n without gaps'

$soloBody = @{
    name    = "Solo $([guid]::NewGuid().ToString('N').Substring(0,6))"
    members = @(@{ user_id = $s1.id; role = 'speaker'; speaking_order = 1 })
} | ConvertTo-Json -Depth 5
$solo = Invoke-RestMethod "$base/api/v1/teams" -Method Post -Headers $auth -Body $soloBody -ContentType 'application/json'
Pass 'a one-person team is a valid team (wrong shape for this template, checked later)'

Step 'assessment lifecycle'
$opens = (Get-Date).ToUniversalTime().AddSeconds(3).ToString('o')
$assessmentBody = @{
    template_version_id = $version.id
    title               = 'Devgarh Telecom Suspension, Spring Round'
    description         = 'Seeded demo assessment'
    opens_at            = $opens
} | ConvertTo-Json
$assessment = Invoke-RestMethod "$base/api/v1/assessments" -Method Post -Headers $auth -Body $assessmentBody -ContentType 'application/json'
Pass "created in status '$($assessment.status)'"

$assignBody = @{ team_id = $team.id; side = 'applicant' } | ConvertTo-Json
Expect-Status 409 { Invoke-RestMethod "$base/api/v1/assessments/$($assessment.id)/assignments" -Method Post -Headers $auth -Body $assignBody -ContentType 'application/json' } 'a draft assessment accepted an assignment'
Pass 'draft assessment refuses assignments'

$assessment = Invoke-RestMethod "$base/api/v1/assessments/$($assessment.id)/publish" -Method Post -Headers $auth
Pass "published, opens at $($assessment.opens_at)"

Step 'participation shape is enforced at assignment'
$wrongShape = @{ team_id = $solo.id; side = 'applicant' } | ConvertTo-Json
Expect-Status 400 { Invoke-RestMethod "$base/api/v1/assessments/$($assessment.id)/assignments" -Method Post -Headers $auth -Body $wrongShape -ContentType 'application/json' } 'a one-speaker team was assigned to a two-speaker assessment'
Pass 'team size and speaker count checked against the template'

$badSide = @{ team_id = $team.id; side = 'prosecution' } | ConvertTo-Json
Expect-Status 400 { Invoke-RestMethod "$base/api/v1/assessments/$($assessment.id)/assignments" -Method Post -Headers $auth -Body $badSide -ContentType 'application/json' } 'an unknown side was accepted'
Pass 'unknown side rejected'

Step 'assignment materialises a stage timeline'
$assignment = Invoke-RestMethod "$base/api/v1/assessments/$($assessment.id)/assignments" -Method Post -Headers $auth -Body $assignBody -ContentType 'application/json'
if ($assignment.stages.Count -ne 8) { Fail "expected 8 materialised stages, got $($assignment.stages.Count)" }
Pass "$($assignment.stages.Count) stages materialised, status '$($assignment.status)'"

$detail = Invoke-RestMethod "$base/api/v1/assignments/$($assignment.id)" -Headers $auth
$memorial = $detail.stages | Where-Object { $_.stage_id -eq 'memorial' }
if (-not $memorial.due_at) { Fail 'memorial stage has no deadline' }
if (-not $memorial.grace_until) { Fail 'memorial stage has no grace window' }
Pass "memorial due $($memorial.due_at)"
Pass "grace until $($memorial.grace_until)"

$evalStage = $detail.stages | Where-Object { $_.stage_id -eq 'memorial_eval' }
if ($evalStage.opens_at) { Fail 'an evaluation stage should have no clock time' }
Pass 'evaluation and review stages wait on their predecessor, not on the clock'

Expect-Status 409 { Invoke-RestMethod "$base/api/v1/assessments/$($assessment.id)/assignments" -Method Post -Headers $auth -Body $assignBody -ContentType 'application/json' } 'the same team was assigned twice'
Pass 'a team cannot be assigned to one assessment twice'

Step 'durable scheduler fires'
$prep = $null
foreach ($i in 1..12) {
    Start-Sleep -Seconds 2
    $detail = Invoke-RestMethod "$base/api/v1/assignments/$($assignment.id)" -Headers $auth
    $prep = $detail.stages | Where-Object { $_.stage_id -eq 'preparation' }
    if ($prep.status -eq 'active') { break }
}
if ($prep.status -ne 'active') { Fail "preparation never activated (status '$($prep.status)')" }
Pass "scheduled transition applied: preparation active at $($prep.started_at)"

if ($detail.status -ne 'in_progress') { Fail "assignment should be in_progress, got '$($detail.status)'" }
Pass "transition hook advanced the assignment to '$($detail.status)', current stage '$($detail.current_stage_id)'"

Step 'students see only their own work'
$sLogin = Invoke-RestMethod "$base/api/v1/auth/login" -Method Post -ContentType 'application/json' `
    -Body (@{ email = $s1.email; password = $password } | ConvertTo-Json)
$sAuth = @{ Authorization = "Bearer $($sLogin.access_token)" }

$mine = Invoke-RestMethod "$base/api/v1/assignments" -Headers $sAuth
if ($mine.assignments.Count -lt 1) { Fail 'a team speaker saw no assignments' }
Pass "speaker sees $($mine.assignments.Count) assignment: $($mine.assignments[0].assessment_title)"

$oLogin = Invoke-RestMethod "$base/api/v1/auth/login" -Method Post -ContentType 'application/json' `
    -Body (@{ email = $outsiderUser.email; password = $password } | ConvertTo-Json)
$oAuth = @{ Authorization = "Bearer $($oLogin.access_token)" }
$theirs = Invoke-RestMethod "$base/api/v1/assignments" -Headers $oAuth
if ($theirs.assignments.Count -ne 0) { Fail 'a student on no team saw an assignment' }
Pass 'a student on no team sees nothing'

Expect-Status 404 { Invoke-RestMethod "$base/api/v1/assignments/$($assignment.id)" -Headers $oAuth } 'an unrelated student read another team''s assignment'
Pass "an unrelated student cannot read another team's assignment"

Expect-Status 403 { Invoke-RestMethod "$base/api/v1/assessments" -Headers $sAuth } 'a student listed all assessments'
Pass 'a student cannot list organisation assessments'

Write-Host "`nAll workflow checks passed." -ForegroundColor Green
