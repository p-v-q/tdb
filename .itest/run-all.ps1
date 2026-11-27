# Runs the whole TDB integration suite against the real terminal binary and the
# real databases, then writes a summary at .artifacts/itest-summary.md.
#
#   .\run-all.ps1                 # every group
#   .\run-all.ps1 -Group chrome   # one group (smoke, interaction, chrome,
#                                 #   conntion, editor, grid, showcase, pg, dialect)
#   .\run-all.ps1 -SkipBuild      # reuse the current bin\tdb.exe
#
# Prerequisites: the three database servers and the datasets must be up
# (see .itest\datasets\README.md and .itest\env.ps1).

param(
    [string]$Group = 'all',
    [switch]$SkipBuild
)

$ErrorActionPreference = 'Continue'
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
. "$PSScriptRoot\env.ps1"

$root = Split-Path $PSScriptRoot -Parent
$art = Join-Path $root '.artifacts'
New-Item -ItemType Directory -Force -Path $art | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$logDir = Join-Path $art "itest-run-$stamp"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$summaryPath = Join-Path $art 'itest-summary.md'

$groups = [ordered]@{
    'smoke'       = 'TestSmoke'
    'interaction' = 'TestInteraction|TestDoubleClick|TestTabContextMenu|TestPaneResize'
    'chrome'      = 'TestChrome'
    'connection'  = 'TestConnForm|TestVault'
    'showcase'    = 'TestShowcase'
    'editor'      = 'TestEditor'
    'grid'        = 'TestGrid|TestResults'
    'pg'          = 'TestPG|TestBrowse|TestDDL'
    'dialect'     = 'TestMySQL|TestMaria|TestSQLiteBig'
}
if ($Group -ne 'all') {
    if (-not $groups.Contains($Group)) {
        throw "unknown group '$Group'; known: $($groups.Keys -join ', ')"
    }
    $groups = [ordered]@{ $Group = $groups[$Group] }
}

function Write-Summary([string]$text) {
    Add-Content -Path $summaryPath -Value $text -Encoding utf8
}

if (-not $SkipBuild) {
    Push-Location $root
    & go build -o 'bin\tdb.exe' ./cmd/tdb
    $buildExit = $LASTEXITCODE
    Pop-Location
    if ($buildExit -ne 0) { throw "go build failed with $buildExit" }
    Write-Host "built bin\tdb.exe"
}

$binBuilt = (Get-Item (Join-Path $root 'bin\tdb.exe')).LastWriteTime
Set-Content -Path $summaryPath -Encoding utf8 -Value @(
    "# TDB integration suite - $(Get-Date -Format 'yyyy-MM-dd HH:mm')",
    '',
    "Binary: bin\tdb.exe (built $binBuilt)",
    '',
    '| Group | Passed | Failed | Skipped | Seconds | Log |',
    '|---|---|---|---|---|---|'
)

$totalPass = 0
$totalFail = 0
$totalSkip = 0
$totalSeconds = 0

Push-Location $PSScriptRoot
foreach ($name in $groups.Keys) {
    $pattern = $groups[$name]
    $log = Join-Path $logDir "$name.log"
    Write-Host "== $name ($pattern)"
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    & go test ./scenarios -run $pattern -v -count=1 -timeout 2700s 2>&1 |
        Tee-Object -FilePath $log | Out-Host
    $exit = $LASTEXITCODE
    $sw.Stop()

    $text = Get-Content -Raw -Path $log
    $passed = ([regex]::Matches($text, '(?m)^--- PASS: ')).Count
    $failed = ([regex]::Matches($text, '(?m)^--- FAIL: ')).Count
    $skipped = ([regex]::Matches($text, '(?m)^--- SKIP: ')).Count
    $totalPass += $passed
    $totalFail += $failed
    $totalSkip += $skipped
    $totalSeconds += [int]$sw.Elapsed.TotalSeconds
    $relative = $log.Substring($root.Length + 1)
    Write-Summary ("| $name | $passed | $failed | $skipped | {0:N0} | $relative |" -f $sw.Elapsed.TotalSeconds)
    if ($exit -ne 0 -and $failed -eq 0) {
        Write-Summary ("| $name (build/setup failure) | 0 | 1 | 0 | {0:N0} | $relative |" -f $sw.Elapsed.TotalSeconds)
    }
}
Pop-Location

Write-Summary ''
Write-Summary ("**Total: {0} passed, {1} failed, {2} skipped in {3:N0}s.**" -f $totalPass, $totalFail, $totalSkip, $totalSeconds)
Write-Summary ''
Write-Summary "Artifacts per scenario: .artifacts\itest\<TestName>\ (*.png screenshots of the real terminal, *.txt screen dumps, *.html snapshots)."
Write-Host "summary written to $summaryPath"
if ($totalFail -gt 0) { exit 1 }
