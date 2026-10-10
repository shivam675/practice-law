$ErrorActionPreference = 'Stop'
$files = & docker compose run --rm --entrypoint gofmt go -l .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
if ($files) {
    $files | Write-Error
    throw 'Go files require gofmt.'
}
Write-Output 'Go formatting is valid.'
