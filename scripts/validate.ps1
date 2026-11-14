[CmdletBinding()]
param([switch]$Race, [switch]$Scan, [string]$GoExecutable='go')
$ErrorActionPreference='Stop'
$settings=@('TEMP','TMP','TMPDIR','GOTMPDIR','GOCACHE','GOMODCACHE','GOWORK','GOFLAGS','GOTOOLCHAIN','CGO_ENABLED','GOOS','GOARCH')
$saved=@{}; foreach($key in $settings){$saved[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
function Invoke-Go([string[]]$Arguments){ & $GoExecutable @Arguments; if($LASTEXITCODE -ne 0){throw "go $($Arguments -join ' ') failed ($LASTEXITCODE)"} }
$base=if($env:DEVELOPMENT_ROOT){$env:DEVELOPMENT_ROOT}elseif($env:RUNNER_TEMP){Join-Path $env:RUNNER_TEMP 'development'}else{'V:\Development'}
if(-not(Test-Path -LiteralPath $base) -and -not $env:RUNNER_TEMP){throw 'Development storage is unavailable; set DEVELOPMENT_ROOT to an approved external directory.'}
$project=Join-Path $base 'go-oracle-session';$runID=[DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss-ffff')
$scratch=Join-Path $project "tmp\validate-$runID";$output=Join-Path $project "artifacts\runs\validate-$runID"
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $env:TEMP=$scratch;$env:TMP=$scratch;$env:TMPDIR=$scratch;$env:GOTMPDIR=Join-Path $scratch 'go-build'
    $env:GOCACHE=Join-Path $project 'caches\go-build';$env:GOMODCACHE=Join-Path $project 'caches\go-modules'
    foreach($directory in @($scratch,$output,$env:GOTMPDIR,$env:GOCACHE,$env:GOMODCACHE)){New-Item -ItemType Directory -Path $directory -Force|Out-Null}
    $env:GOWORK='off';$env:GOFLAGS='-mod=readonly';$env:GOTOOLCHAIN='go1.27.4';$env:CGO_ENABLED='0'
    Remove-Item Env:GOOS,Env:GOARCH -ErrorAction SilentlyContinue
    $version=& $GoExecutable env GOVERSION;if($LASTEXITCODE -ne 0 -or $version -ne 'go1.27.4'){throw "Expected go1.27.4; got $version"}
    $goRoot=& $GoExecutable env GOROOT;if($LASTEXITCODE -ne 0){throw 'Cannot inspect compiler root'}
    $unformatted=@(& (Join-Path $goRoot 'bin\gofmt.exe') -l .)
    if($LASTEXITCODE -ne 0 -or $unformatted.Count -ne 0){throw "Formatting required: $unformatted"}
    Invoke-Go -Arguments @('test','-count=1','./...');Invoke-Go -Arguments @('vet','./...');Invoke-Go -Arguments @('mod','verify');Invoke-Go -Arguments @('build','./...')
    if($Race){$env:CGO_ENABLED='1';try{Invoke-Go -Arguments @('test','-race','-count=1','./...')}finally{$env:CGO_ENABLED='0'}}
    $binary=Join-Path $output 'go-oracle-session.test.exe';Invoke-Go -Arguments @('test','-c','-trimpath','-buildvcs=false','-o',$binary,'./session')
    & $GoExecutable version -m $binary | Set-Content -LiteralPath (Join-Path $output 'build-metadata.txt');if($LASTEXITCODE -ne 0){throw 'Metadata inspection failed'}
    & $GoExecutable env -json GOVERSION GOOS GOARCH GOFLAGS CGO_ENABLED GOWORK | Set-Content -LiteralPath (Join-Path $output 'build-settings.json');if($LASTEXITCODE -ne 0){throw 'Build settings inspection failed'}
    & $GoExecutable list -m -json all | Set-Content -LiteralPath (Join-Path $output 'modules.json');if($LASTEXITCODE -ne 0){throw 'Module graph inspection failed'}
    if($Scan){Invoke-Go -Arguments @('run','golang.org/x/vuln/cmd/govulncheck@v1.9.0','./...');Invoke-Go -Arguments @('run','golang.org/x/vuln/cmd/govulncheck@v1.9.0','-mode=binary',$binary)}
    Write-Output "Validation passed. Evidence: $output"
} finally {
    foreach($key in $settings){[Environment]::SetEnvironmentVariable($key,$saved[$key],'Process')}
    Pop-Location
}
