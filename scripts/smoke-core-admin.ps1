# Bounded administration/resource smoke. All mutations use a new test tenant; no grades are published.
$ErrorActionPreference='Stop'
$settings=@{}
Get-Content (Join-Path $PSScriptRoot '..\.env') | ForEach-Object {if($_ -match '^([A-Z0-9_]+)=(.*)$'){$settings[$Matches[1]]=$Matches[2].Trim()}}
$origin=if($env:STAGING_URL){$env:STAGING_URL.TrimEnd('/')}else{'http://localhost:8080'}
$base="$origin/api/v1"
function Call($method,$path,$auth,$body=$null){
 $args=@{Method=$method;Uri="$base$path";Headers=$auth;ContentType='application/json';TimeoutSec=45}
 if($null -ne $body){$args.Body=$body|ConvertTo-Json -Depth 15}
 Invoke-RestMethod @args
}
function Login($email,$password){$response=Call POST '/auth/login' @{} @{email=$email;password=$password};@{Authorization="Bearer $($response.access_token)"}}
function Check($condition,$message){if(-not $condition){throw $message};Write-Output "PASS: $message"}
function Denied($code,$label,$action){try{& $action|Out-Null;throw "Unexpected success: $label"}catch{if([int]$_.Exception.Response.StatusCode -ne $code){throw}};Write-Output "PASS: $label ($code)"}
function Upload($assessmentID,$auth,$visibility,$title,$text,$kind='guidance'){
 $boundary=[guid]::NewGuid().ToString('N')
 $parts=@()
 foreach($field in @(@('title',$title),@('kind',$kind),@('visibility',$visibility))){$parts+="--$boundary`r`nContent-Disposition: form-data; name=`"$($field[0])`"`r`n`r`n$($field[1])`r`n"}
 $parts+="--$boundary`r`nContent-Disposition: form-data; name=`"file`"; filename=`"material.txt`"`r`nContent-Type: text/plain`r`n`r`n$text`r`n--$boundary--`r`n"
 Invoke-RestMethod "$base/assessments/$assessmentID/resources" -Method Post -Headers $auth -ContentType "multipart/form-data; boundary=$boundary" -Body ([Text.Encoding]::UTF8.GetBytes(($parts -join ''))) -TimeoutSec 45
}
if(-not $settings.SEED_SUPERADMIN_EMAIL -or -not $settings.SEED_SUPERADMIN_PASSWORD){throw 'Platform seed credentials are needed for the isolated tenant smoke.'}
$platform=Login $settings.SEED_SUPERADMIN_EMAIL $settings.SEED_SUPERADMIN_PASSWORD
$existingAdmin=Login $settings.SEED_ADMIN_EMAIL $settings.SEED_ADMIN_PASSWORD
$suffix=[guid]::NewGuid().ToString('N').Substring(0,12)
$password='Smoke-'+[guid]::NewGuid().ToString('N')+'-42'
$orgName="Core smoke $suffix"
$email="admin-$suffix@test.invalid"
$organization=Call POST '/admin/organizations' $platform @{name=$orgName;admin_email=$email;admin_password=$password}
$admin=Login $email $password
Check (@((Call GET '/admin/organizations' $platform).organizations|Where-Object id -eq $organization.id).Count -eq 1) 'Platform can create and list an isolated organisation'
Denied 409 'Duplicate organisation cannot seed an extra administrator' {Call POST '/admin/organizations' $platform @{name=$orgName;admin_email="extra-$suffix@test.invalid";admin_password=$password}}
Denied 403 'Tenant administrator cannot list platform organisations' {Call GET '/admin/organizations' $admin}
Denied 403 'Tenant administrator cannot create an organisation' {Call POST '/admin/organizations' $admin @{name="Denied $suffix";admin_email=$email;admin_password=$password}}
$own=Call GET '/organization' $admin
Check ($own.id -eq $organization.id) 'Organisation settings are scoped to the current tenant'
Call PUT '/organization' $admin @{name="$orgName renamed";locale='en-IN';timezone='Asia/Kolkata'}|Out-Null
$own=Call GET '/organization' $admin
Check ($own.name -eq "$orgName renamed" -and $own.settings.timezone -eq 'Asia/Kolkata') 'Own organisation settings update'
$one=Call POST '/users' $admin @{email="one-$suffix@test.invalid";full_name='Smoke Student One';password=$password;roles=@('student')}
$two=Call POST '/users' $admin @{email="two-$suffix@test.invalid";full_name='Smoke Student Two';password=$password;roles=@('student')}
$student=Login $one.email $password
$other=Login $two.email $password
Denied 403 'Student cannot read organisation settings' {Call GET '/organization' $student}
Denied 403 'Student cannot change organisation settings' {Call PUT '/organization' $student @{name='Denied';locale='en';timezone='UTC'}}
$team=Call POST '/teams' $admin @{name="Smoke team $suffix";members=@(@{user_id=$two.id;role='speaker';speaking_order=1})}
Call PUT "/teams/$($team.id)" $admin @{name="Edited team $suffix";members=@(@{user_id=$one.id;role='speaker';speaking_order=1})}|Out-Null
Check ((Call GET "/teams/$($team.id)" $admin).members[0].user_id -eq $one.id) 'Unassigned team membership can change'
Denied 403 'Student cannot edit a team' {Call PUT "/teams/$($team.id)" $student @{name='Denied';members=@(@{user_id=$two.id;role='speaker';speaking_order=1})}}
$rubric=Call POST '/rubrics' $admin @{key="smoke_$suffix";name='Smoke rubric';criteria=@(@{key='reasoning';name='Reasoning';weight=1;max_score=10;scope=@();description='Reasoning';guidance='Use evidence.'})}
$template=Call POST '/templates' $admin @{key="smoke_$suffix";name='Smoke template';assessment_type='viva'}
$version=Call POST "/templates/$($template.id)/versions" $admin @{rubric_id=$rubric.id;participation=@{sides=@('candidate','staff');speakers=1;min_team_size=1;max_team_size=1;ai_actors=@()};stages=@(@{id='wait';kind='wait';label='Materials';config=@{visible_resources=@('guidance')}},@{id='review';kind='human_review';label='Review';config=@{required=$true;overrides_allowed=$true}},@{id='evaluation';kind='automated_evaluation';label='Evaluation';config=@{rubric_scope=@('reasoning')}})}
Call POST "/templates/versions/$($version.id)/publish" $admin | Out-Null
$assessment=Call POST '/assessments' $admin @{template_version_id=$version.id;title="Smoke resources $suffix";opens_at='2099-01-01T00:00:00Z'}
Call POST "/assessments/$($assessment.id)/publish" $admin | Out-Null
$assignment=Call POST "/assessments/$($assessment.id)/assignments" $admin @{team_id=$team.id;side='staff'}
$locked=Call GET "/teams/$($team.id)" $admin
Check $locked.membership_locked 'Assigned team exposes locked membership'
Denied 409 'Assignment history blocks membership replacement' {Call PUT "/teams/$($team.id)" $admin @{name='Denied';members=@(@{user_id=$two.id;role='speaker';speaking_order=1})}}
Call PUT "/teams/$($team.id)" $admin @{name="Renamed assigned team $suffix";members=@(@{user_id=$one.id;role='speaker';speaking_order=1})}|Out-Null
Check ((Call GET "/teams/$($team.id)" $admin).name -eq "Renamed assigned team $suffix") 'Assigned team name can still change'
Upload $assessment.id $admin 'all' 'Public guidance' 'PUBLIC MATERIAL CHECK' | Out-Null
Upload $assessment.id $admin 'staff' 'Staff guidance' 'STAFF SECRET CHECK' | Out-Null
Upload $assessment.id $admin 'candidate' 'Other side guidance' 'OTHER SIDE CHECK' | Out-Null
$listed=Call GET "/assessments/$($assessment.id)/resources" $admin
Check ($listed.resources.Count -eq 3) 'Teacher upload extracts and lists all scoped materials'
$visible=Call GET "/assignments/$($assignment.id)/resources" $student
Check ($visible.resources.Count -eq 1 -and $visible.resources[0].body -match 'PUBLIC MATERIAL CHECK') 'Staff-only and other-side content excluded even for a side named staff'
Denied 403 'Student cannot list teacher material metadata' {Call GET "/assessments/$($assessment.id)/resources" $student}
Denied 403 'Student cannot upload materials' {Upload $assessment.id $student 'all' 'Denied' 'Denied'}
Denied 404 'Another student cannot read the assignment materials' {Call GET "/assignments/$($assignment.id)/resources" $other}
Denied 404 'Cross-tenant resource list refused' {Call GET "/assessments/$($assessment.id)/resources" $existingAdmin}
Denied 404 'Cross-tenant upload refused before parsing' {Invoke-RestMethod "$base/assessments/$($assessment.id)/resources" -Method Post -Headers $existingAdmin -ContentType 'multipart/form-data' -Body 'not multipart'}
Denied 400 'Unknown side rejected' {Upload $assessment.id $admin 'respondent' 'Denied' 'Denied'}
Denied 400 'Unknown resource kind rejected' {Upload $assessment.id $admin 'all' 'Denied' 'Denied' 'unknown'}
Denied 400 'Truncated extraction rejected' {Upload $assessment.id $admin 'all' 'Too long' ('a'*4000001)}
Check ((Call GET "/assessments/$($assessment.id)/resources" $admin).resources.Count -eq 3) 'Failed parsing creates no material record'
Denied 403 'Student cannot issue recovery links' {Call POST "/users/$($two.id)/password-reset" $student}
Denied 404 'Cross-tenant recovery issuance refused' {Call POST "/users/$($two.id)/password-reset" $existingAdmin}
$link=Call POST "/users/$($two.id)/password-reset" $admin
$token=($link.reset_path -split '#token=')[1]
$newPassword='Smoke-new-'+[guid]::NewGuid().ToString('N')
Call POST '/auth/reset-password' @{} @{token=$token;password=$newPassword}|Out-Null
Denied 401 'Password reset revokes the old access session' {Call GET '/me' $other}
Denied 400 'Reset link works once' {Call POST '/auth/reset-password' @{} @{token=$token;password=$newPassword}}
# The production login bucket starts with five tokens; this smoke performs a sixth login.
Start-Sleep -Seconds 4
$afterReset=Login $two.email $newPassword
Check ((Call GET '/me' $afterReset).user.id -eq $two.id) 'New password signs in after reset'
Write-Output "PASS: core administration HTTP smoke. Isolated test tenant retained: $($organization.slug)"
