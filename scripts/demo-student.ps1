# Provisions one end-to-end student demo: rubric, template, assessment, team,
# assignment. Prints the sign-in details and the assignment URL.
#
#   pwsh scripts/demo-student.ps1
param([string]$StudentEmail = 'student1@demo.test', [int]$OralSeconds = 240)
$ErrorActionPreference = 'Stop'

$settings = @{}
Get-Content (Join-Path $PSScriptRoot '..\.env') | ForEach-Object {
    if ($_ -match '^([A-Z0-9_]+)=(.*)$') { $settings[$Matches[1]] = $Matches[2].Trim() }
}
$base = 'http://localhost:8080/api/v1'
$web = 'http://localhost:5173'

function Call($method, $path, $auth, $body = $null) {
    $args = @{ Method = $method; Uri = "$base$path"; Headers = $auth; ContentType = 'application/json' }
    if ($null -ne $body) { $args.Body = $body | ConvertTo-Json -Depth 15 }
    Invoke-RestMethod @args
}
function Login($email, $password) {
    $r = Call POST '/auth/login' @{} @{ email = $email; password = $password }
    @{ Authorization = "Bearer $($r.access_token)" }
}

$admin = Login $settings.SEED_ADMIN_EMAIL $settings.SEED_ADMIN_PASSWORD
$suffix = [guid]::NewGuid().ToString('N').Substring(0, 6)

$student = (Call GET '/users' $admin).users | Where-Object email -eq $StudentEmail
if (-not $student) { throw "No such user: $StudentEmail" }

$team = Call POST '/teams' $admin @{
    name    = "Counsel for the Applicant ($suffix)"
    members = @(@{ user_id = $student.id; role = 'speaker'; speaking_order = 1 })
}

$rubric = Call POST '/rubrics' $admin @{
    key      = "appellate_advocacy_$suffix"
    name     = 'Appellate advocacy'
    criteria = @(
        @{ key = 'issue_framing'; name = 'Issue framing'; description = 'States the question for decision and the relief sought precisely.'; weight = 20; max_score = 10; scope = @('written', 'oral'); guidance = 'Quote the sentence where the issue is framed.' },
        @{ key = 'legal_reasoning'; name = 'Legal reasoning'; description = 'Applies legality, necessity and proportionality to the facts on the record.'; weight = 35; max_score = 10; scope = @('written', 'oral'); guidance = 'Quote the step of reasoning being scored.' },
        @{ key = 'use_of_record'; name = 'Use of the record'; description = 'Supports each claim with material actually in the memorial.'; weight = 25; max_score = 10; scope = @('written', 'oral'); guidance = 'Quote the passage relied on. Do not credit an unsupported claim.' },
        @{ key = 'responsiveness'; name = 'Responsiveness to the bench'; description = 'Answers the question asked before returning to the prepared argument.'; weight = 20; max_score = 10; scope = @('oral'); guidance = 'Quote the question and the first sentence of the answer.' }
    )
}

$template = Call POST '/templates' $admin @{
    key = "moot_demo_$suffix"; name = 'Moot court: internet suspension'; assessment_type = 'moot_court'
}

$version = Call POST "/templates/$($template.id)/versions" $admin @{
    rubric_id     = $rubric.id
    participation = @{
        sides = @('applicant'); speakers = 1; min_team_size = 1; max_team_size = 1
        ai_actors = @(@{ profile_key = 'presiding_judge'; role = 'judge'; display_name = 'Presiding Judge'; presiding = $true })
    }
    stages = @(
        @{ id = 'memorial'; kind = 'artifact_submission'; label = 'Written memorial'
           due_after_s = 7200
           config = @{ formats = @('txt', 'pdf', 'docx'); max_bytes = 2000000; lock_on_submit = $true } },
        @{ id = 'oral'; kind = 'live_turn'; label = 'Oral argument'
           config = @{ duration_s = $OralSeconds; speaker_order = 1; interruptions = 'enabled'
                       warn_at_s = @(60, 15); extension_s = 30; max_extensions = 1
                       ai_profiles = @('presiding_judge') } },
        @{ id = 'evaluation'; kind = 'automated_evaluation'; label = 'Automated evaluation'
           config = @{ rubric_scope = @('issue_framing', 'legal_reasoning', 'use_of_record', 'responsiveness')
                       sources = @('memorial', 'oral') } },
        @{ id = 'review'; kind = 'human_review'; label = 'Teacher review'
           config = @{ required = $true; overrides_allowed = $true } }
    )
}
Call POST "/templates/versions/$($version.id)/publish" $admin | Out-Null

$assessment = Call POST '/assessments' $admin @{
    template_version_id = $version.id
    title               = 'Iyer v. State of Devagarh - internet suspension'
    opens_at            = (Get-Date).ToUniversalTime().AddSeconds(2).ToString('o')
}
Call POST "/assessments/$($assessment.id)/publish" $admin | Out-Null
$assignment = Call POST "/assessments/$($assessment.id)/assignments" $admin @{ team_id = $team.id; side = 'applicant' }

foreach ($i in 1..30) {
    $detail = Call GET "/assignments/$($assignment.id)" $admin
    if (($detail.stages | Where-Object stage_id -eq 'oral').status -eq 'active') { break }
    Start-Sleep -Seconds 1
}
$detail.stages | ForEach-Object { Write-Output ("  {0,-12} {1,-22} {2}" -f $_.stage_id, $_.stage_kind, $_.status) }

Write-Output ''
Write-Output "Sign in:    $web/"
Write-Output "Student:    $StudentEmail / $($settings.SEED_DEMO_PASSWORD)"
Write-Output "Teacher:    teacher@demo.test / $($settings.SEED_DEMO_PASSWORD)"
Write-Output "Assignment: $web/work/$($assignment.id)"
Write-Output "Oral room:  $web/session/$($assignment.id)/oral"
Write-Output "Memorial:   samples/four-minute-student-memorial.txt"
