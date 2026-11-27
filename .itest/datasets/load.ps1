<#
.SYNOPSIS
    Idempotent loader for the TDB integration-test datasets.

.DESCRIPTION
    (Re)creates all datasets used by TDB integration tests:
      * PostgreSQL 17   : databases `shop` and `legacy`
      * MySQL 8.4       : databases `shop`, `legacy` + 6 legacy_* side databases
      * MariaDB 11.4    : databases `shop`, `legacy` + 6 legacy_* side databases
      * SQLite          : .dbs\sqlite\shop.db and .dbs\sqlite\weird.db

    Every loader is idempotent (drops/recreates its objects), so this script can be
    re-run at any time. It prints:
      * a verification table of object row counts,
      * an acceptance-check table (PASS/FAIL),
      * a per-step timing table.
    Output is logged to .artifacts\dataset-load.log.

    The database servers are expected to be already running (this script never
    starts, stops or reconfigures them):

      PostgreSQL 17.6   127.0.0.1:55432  user postgres (trust), role tdb/tdbpass
      MySQL 8.4.4       127.0.0.1:53306  user root (no password), user tdb/tdbpass
      MariaDB 11.4.4    127.0.0.1:53307  user root/tdbpass, user tdb/tdbpass

.EXAMPLE
    Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
    .\load.ps1

.EXAMPLE
    # Only verify the datasets that are already loaded (no reload).
    .\load.ps1 -VerifyOnly
#>
[CmdletBinding()]
param(
    [switch]$VerifyOnly
)

$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'
$PSNativeCommandUseErrorActionPreference = $false
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)

# ---------------------------------------------------------------------------
# Paths / environment
# ---------------------------------------------------------------------------
$datasets = $PSScriptRoot                       # ...\.itest\datasets
$itest     = Split-Path $datasets -Parent       # ...\.itest
$repo      = Split-Path $itest -Parent          # repo root
. (Join-Path $itest 'env.ps1')

$PSQL  = Join-Path $PGBIN 'psql.exe'
$MYSQL = Join-Path $MYBIN 'mysql.exe'
$MARIADB = Join-Path $MABIN 'mariadb.exe'

$pgHost = '127.0.0.1'; $pgPort = '55432'; $pgUser = 'postgres'
$myHost = '127.0.0.1'; $myPort = '53306'
$maHost = '127.0.0.1'; $maPort = '53307'

$sqliteDir = Join-Path $repo '.dbs\sqlite'
$artDir    = $env:ART
$logPath   = Join-Path $artDir 'dataset-load.log'
$env:PGCLIENTENCODING = 'UTF8'

New-Item -ItemType Directory -Force -ErrorAction Stop -Path $artDir, $sqliteDir, $datasets | Out-Null

$script:rows      = [System.Collections.Generic.List[object]]::new()
$script:checks    = [System.Collections.Generic.List[object]]::new()
$script:timings   = [ordered]@{}
$script:sqliteOut = @()
$script:failChecks = 0

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
function Write-Section([string]$title) {
    Write-Host ''
    Write-Host ('=' * 78) -ForegroundColor DarkCyan
    Write-Host $title -ForegroundColor Cyan
    Write-Host ('=' * 78) -ForegroundColor DarkCyan
}

function Step-Load {
    param([string]$Label, [scriptblock]$Action)
    Write-Host ''
    Write-Host "==> $Label" -ForegroundColor Cyan
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    & $Action
    $sw.Stop()
    $script:timings[$Label] = [math]::Round($sw.Elapsed.TotalSeconds, 2)
    Write-Host ("    done in {0:n2}s" -f $sw.Elapsed.TotalSeconds) -ForegroundColor DarkGray
}

function Show-Tail($out, [int]$n = 5) {
    @($out) | Select-Object -Last $n | ForEach-Object { Write-Host "    $_" -ForegroundColor DarkGray }
}

function New-Row([string]$db, [string]$obj, $count) {
    $script:rows.Add([pscustomobject]@{ Database = $db; Object = $obj; Rows = [string]$count })
}

function Add-Check([string]$area, [string]$name, [bool]$ok, [string]$detail) {
    if (-not $ok) { $script:failChecks++ }
    $script:checks.Add([pscustomobject]@{
        Area   = $area
        Check  = $name
        Result = $(if ($ok) { 'PASS' } else { 'FAIL' })
        Detail = $detail
    })
}

# --- PostgreSQL -----------------------------------------------------------
function PgQuery([string]$db, [string]$sql) {
    # Feed SQL through stdin: Windows PowerShell 5.1 mangles double quotes in native
    # command arguments, which breaks quoted identifiers such as "Mixed Case Table".
    $r = $sql | & $PSQL -h $pgHost -p $pgPort -U $pgUser -d $db -tA -f - 2>$null
    if ($LASTEXITCODE -ne 0) { throw "psql query failed on database '$db': $sql" }
    @($r) | ForEach-Object { "$_".Trim() }
}

function PgQueryOne([string]$db, [string]$sql) {
    $r = @(PgQuery $db $sql)
    if ($r.Count -eq 0) { return '' }
    return $r[0]
}

# --- MySQL ----------------------------------------------------------------
function MyQuery([string]$sql) {
    $r = $sql | & $MYSQL -h $myHost -P $myPort -u root --skip-column-names --default-character-set=utf8mb4 2>$null
    if ($LASTEXITCODE -ne 0) { throw "mysql query failed: $sql" }
    @($r) | ForEach-Object { "$_".Trim() }
}

function MyQueryOne([string]$sql) {
    $r = @(MyQuery $sql)
    if ($r.Count -eq 0) { return '' }
    return $r[0]
}

# --- MariaDB --------------------------------------------------------------
function MaQuery([string]$sql) {
    $r = $sql | & $MARIADB -h $maHost -P $maPort -u root -ptdbpass --skip-column-names --default-character-set=utf8mb4 2>$null
    if ($LASTEXITCODE -ne 0) { throw "mariadb query failed: $sql" }
    @($r) | ForEach-Object { "$_".Trim() }
}

function MaQueryOne([string]$sql) {
    $r = @(MaQuery $sql)
    if ($r.Count -eq 0) { return '' }
    return $r[0]
}

function Invoke-SqlLoad {
    param(
        [string]$Label,
        [string[]]$Command,
        [string]$FilePath
    )
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $exe = $Command[0]
    $rest = @($Command[1..($Command.Count - 1)])
    $out = & $exe @rest 2>&1
    $code = $LASTEXITCODE
    $sw.Stop()
    $script:timings[$Label] = [math]::Round($sw.Elapsed.TotalSeconds, 2)
    $text = (@($out) | ForEach-Object { "$_" }) -join "`n"
    if ($code -ne 0 -or $text -match 'ERROR \d{4}') {
        @($out) | Select-Object -Last 40 | ForEach-Object { Write-Host "    $_" -ForegroundColor Red }
        throw "loading $FilePath failed (client exit code $code)"
    }
    Show-Tail $out 4
    Write-Host ("    done in {0:n2}s" -f $sw.Elapsed.TotalSeconds) -ForegroundColor DarkGray
}

# ---------------------------------------------------------------------------
# Pre-flight
# ---------------------------------------------------------------------------
Write-Section "TDB dataset loader  ($(Get-Date -Format 'yyyy-MM-dd HH:mm:ss'))"
Write-Host "repo      : $repo"
Write-Host "datasets  : $datasets"
Write-Host "sqlite dir: $sqliteDir"
Write-Host "log       : $logPath"

$transcript = $false
try {
    Start-Transcript -Path $logPath -Force | Out-Null
    $transcript = $true
} catch {
    Write-Host "WARNING: could not start transcript: $_" -ForegroundColor Yellow
}

$totalSw = [System.Diagnostics.Stopwatch]::StartNew()
$fatal = $null

try {
    if (-not $VerifyOnly) {
        Write-Section 'Pre-flight: servers, roles and databases'
        Step-Load 'Pre-flight' {
            # PostgreSQL reachable?
            $null = PgQueryOne 'postgres' 'select 1'
            Write-Host '    PostgreSQL reachable' -ForegroundColor DarkGray

            # Ensure role tdb / database shop + legacy exist.
            if ((PgQueryOne 'postgres' "select count(*) from pg_roles where rolname = 'tdb'") -eq '0') {
                & $PSQL -h $pgHost -p $pgPort -U $pgUser -d postgres -v ON_ERROR_STOP=1 -c "CREATE ROLE tdb LOGIN SUPERUSER PASSWORD 'tdbpass'" | Out-Null
                if ($LASTEXITCODE -ne 0) { throw 'could not create PostgreSQL role tdb' }
                Write-Host '    created PostgreSQL role tdb' -ForegroundColor DarkGray
            }
            foreach ($db in 'shop', 'legacy') {
                if ((PgQueryOne 'postgres' "select count(*) from pg_database where datname = '$db'") -eq '0') {
                    & $PSQL -h $pgHost -p $pgPort -U $pgUser -d postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE $db" | Out-Null
                    if ($LASTEXITCODE -ne 0) { throw "could not create PostgreSQL database $db" }
                    Write-Host "    created PostgreSQL database $db" -ForegroundColor DarkGray
                }
            }
            $env:PGPASSWORD = 'tdbpass'
            try {
                $ok = (& $PSQL -h $pgHost -p $pgPort -U tdb -d shop -tAc 'select 1' 2>$null) -contains '1'
            } finally {
                Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
            }
            if (-not $ok) { throw "PostgreSQL role tdb/tdbpass cannot connect to 'shop'" }
            Write-Host '    PostgreSQL tdb/tdbpass works' -ForegroundColor DarkGray

            # MySQL / MariaDB reachable, tdb user usable (create/alter only if needed).
            $null = MyQuery 'SELECT 1'
            Write-Host '    MySQL reachable' -ForegroundColor DarkGray
            $env:MYSQL_PWD = 'tdbpass'
            try {
                $null = & $MYSQL -h $myHost -P $myPort -u tdb -e 'SELECT 1' 2>$null
                $ok = ($LASTEXITCODE -eq 0)
            } finally {
                Remove-Item Env:MYSQL_PWD -ErrorAction SilentlyContinue
            }
            if (-not $ok) {
                & $MYSQL -h $myHost -P $myPort -u root -e "CREATE USER IF NOT EXISTS 'tdb'@'%' IDENTIFIED BY 'tdbpass'; GRANT ALL PRIVILEGES ON *.* TO 'tdb'@'%';" | Out-Null
                if ($LASTEXITCODE -ne 0) { throw 'could not create MySQL user tdb' }
                Write-Host '    created MySQL user tdb' -ForegroundColor DarkGray
            } else {
                Write-Host '    MySQL tdb/tdbpass works' -ForegroundColor DarkGray
            }

            $null = MaQuery 'SELECT 1'
            Write-Host '    MariaDB reachable' -ForegroundColor DarkGray
            $null = & $MARIADB -h $maHost -P $maPort -u tdb -ptdbpass -e 'SELECT 1' 2>$null
            if ($LASTEXITCODE -ne 0) {
                & $MARIADB -h $maHost -P $maPort -u root -ptdbpass -e "CREATE USER IF NOT EXISTS 'tdb'@'%' IDENTIFIED BY 'tdbpass'; GRANT ALL PRIVILEGES ON *.* TO 'tdb'@'%'; FLUSH PRIVILEGES;" | Out-Null
                if ($LASTEXITCODE -ne 0) { throw 'could not create MariaDB user tdb' }
                Write-Host '    created MariaDB user tdb' -ForegroundColor DarkGray
            } else {
                Write-Host '    MariaDB tdb/tdbpass works' -ForegroundColor DarkGray
            }
        }

        # -------------------------------------------------------------------
        # Load PostgreSQL
        # -------------------------------------------------------------------
        Write-Section 'Loading PostgreSQL (shop, legacy)'
        Step-Load 'PostgreSQL shop' {
            $file = Join-Path $datasets 'pg_shop.sql'
            $out = & $PSQL -h $pgHost -p $pgPort -U $pgUser -d shop -v ON_ERROR_STOP=1 -f $file 2>&1
            $code = $LASTEXITCODE
            if ($code -ne 0) {
                @($out) | Select-Object -Last 40 | ForEach-Object { Write-Host "    $_" -ForegroundColor Red }
                throw "psql pg_shop.sql failed (exit $code)"
            }
            Show-Tail $out 2
        }
        Step-Load 'PostgreSQL legacy' {
            $file = Join-Path $datasets 'pg_legacy.sql'
            $out = & $PSQL -h $pgHost -p $pgPort -U $pgUser -d legacy -v ON_ERROR_STOP=1 -f $file 2>&1
            $code = $LASTEXITCODE
            if ($code -ne 0) {
                @($out) | Select-Object -Last 40 | ForEach-Object { Write-Host "    $_" -ForegroundColor Red }
                throw "psql pg_legacy.sql failed (exit $code)"
            }
            Show-Tail $out 2
        }

        # -------------------------------------------------------------------
        # Load MySQL
        # -------------------------------------------------------------------
        Write-Section 'Loading MySQL (shop, legacy)'
        Invoke-SqlLoad -Label 'MySQL shop' -FilePath (Join-Path $datasets 'mysql_shop.sql') -Command @(
            $MYSQL, '-h', $myHost, '-P', $myPort, '-u', 'root', '--default-character-set=utf8mb4',
            '--show-warnings', '-e', ('source ' + (Join-Path $datasets 'mysql_shop.sql').Replace('\', '/')))
        Invoke-SqlLoad -Label 'MySQL legacy' -FilePath (Join-Path $datasets 'mysql_legacy.sql') -Command @(
            $MYSQL, '-h', $myHost, '-P', $myPort, '-u', 'root', '--default-character-set=utf8mb4',
            '--show-warnings', '-e', ('source ' + (Join-Path $datasets 'mysql_legacy.sql').Replace('\', '/')))

        # -------------------------------------------------------------------
        # Load MariaDB
        # -------------------------------------------------------------------
        Write-Section 'Loading MariaDB (shop, legacy)'
        Invoke-SqlLoad -Label 'MariaDB shop' -FilePath (Join-Path $datasets 'mariadb_shop.sql') -Command @(
            $MARIADB, '-h', $maHost, '-P', $maPort, '-u', 'root', '-ptdbpass', '--default-character-set=utf8mb4',
            '--show-warnings', '-e', ('source ' + (Join-Path $datasets 'mariadb_shop.sql').Replace('\', '/')))
        Invoke-SqlLoad -Label 'MariaDB legacy' -FilePath (Join-Path $datasets 'mariadb_legacy.sql') -Command @(
            $MARIADB, '-h', $maHost, '-P', $maPort, '-u', 'root', '-ptdbpass', '--default-character-set=utf8mb4',
            '--show-warnings', '-e', ('source ' + (Join-Path $datasets 'mariadb_legacy.sql').Replace('\', '/')))

        # -------------------------------------------------------------------
        # Build SQLite databases
        # -------------------------------------------------------------------
        Write-Section 'Building SQLite databases'
        Step-Load 'SQLite build' {
            $genDir = Join-Path $datasets 'gen_sqlite'
            $genExe = Join-Path $genDir 'gen_sqlite.exe'
            $src = Join-Path $genDir 'main.go'
            $needBuild = -not (Test-Path $genExe)
            if (-not $needBuild) {
                $exeTime = (Get-Item $genExe).LastWriteTimeUtc
                $needBuild = ($exeTime -lt (Get-Item $src).LastWriteTimeUtc) -or
                             ($exeTime -lt (Get-Item (Join-Path $genDir 'go.mod')).LastWriteTimeUtc)
            }
            if ($needBuild) {
                $go = (Get-Command go -ErrorAction SilentlyContinue).Source
                if (-not $go) { $go = Join-Path $env:LOCALAPPDATA 'Programs\Go\bin\go.exe' }
                if (-not (Test-Path $go)) { throw 'Go toolchain not found (needed to build gen_sqlite.exe)' }
                Write-Host "    building gen_sqlite.exe ..." -ForegroundColor DarkGray
                $env:GOSUMDB = 'off'
                $env:GOFLAGS = '-mod=mod'
                Push-Location $genDir
                try {
                    $bout = & $go build -o gen_sqlite.exe . 2>&1
                    $bcode = $LASTEXITCODE
                } finally {
                    Pop-Location
                    Remove-Item Env:GOSUMDB -ErrorAction SilentlyContinue
                    Remove-Item Env:GOFLAGS -ErrorAction SilentlyContinue
                }
                if ($bcode -ne 0) {
                    @($bout) | Select-Object -Last 20 | ForEach-Object { Write-Host "    $_" -ForegroundColor Red }
                    throw 'go build of gen_sqlite failed'
                }
            }
            $script:sqliteOut = @(& $genExe -outdir $sqliteDir 2>&1)
            if ($LASTEXITCODE -ne 0) {
                $script:sqliteOut | Select-Object -Last 20 | ForEach-Object { Write-Host "    $_" -ForegroundColor Red }
                throw "gen_sqlite failed (exit $LASTEXITCODE)"
            }
            $script:sqliteOut | Where-Object { $_ -match 'built in' } | ForEach-Object { Write-Host "    $_" -ForegroundColor DarkGray }
        }
    }

    # -----------------------------------------------------------------------
    # Verification: row counts
    # -----------------------------------------------------------------------
    Write-Section 'Verification: row counts'

    $pgShopObjects = @(
        @('public.customers', 'select count(*) from public.customers'),
        @('public.products', 'select count(*) from public.products'),
        @('sales.orders (partitioned)', 'select count(*) from sales.orders'),
        @('sales.order_items', 'select count(*) from sales.order_items'),
        @('sales.payments', 'select count(*) from sales.payments'),
        @('public.reviews', 'select count(*) from public.reviews'),
        @('analytics.events', 'select count(*) from analytics.events'),
        @('inventory.inventory', 'select count(*) from inventory.inventory'),
        @('sales.invoices', 'select count(*) from sales.invoices'),
        @('sales.order_audit', 'select count(*) from sales.order_audit'),
        @('public.wide_table_40', 'select count(*) from public.wide_table_40'),
        @('public.empty_table_0', 'select count(*) from public.empty_table_0'),
        @('public.null_only', 'select count(*) from public.null_only'),
        @('public.unicode_test', 'select count(*) from public.unicode_test'),
        @('analytics.mv_daily_events', 'select count(*) from analytics.mv_daily_events'),
        @('sales.orders partitions', 'select count(*) from pg_inherits where inhparent = ''sales.orders''::regclass')
    )
    foreach ($o in $pgShopObjects) { New-Row 'PG shop' $o[0] (PgQueryOne 'shop' $o[1]) }

    $pgLegacy = @(
        @('schemas', "select count(*) from pg_namespace where nspname !~ '^pg_' and nspname <> 'information_schema'"),
        @('tables', "select count(*) from pg_class c join pg_namespace n on n.oid = c.relnamespace where c.relkind = 'r' and n.nspname !~ '^pg_' and n.nspname <> 'information_schema'"),
        @('public."Mixed Case Table"', 'select count(*) from public."Mixed Case Table"'),
        @('"Accounting"."GL Entries"', 'select count(*) from "Accounting"."GL Entries"'),
        @('"Human Resources".employees', 'select count(*) from "Human Resources".employees'),
        @('"CRM".deals', 'select count(*) from "CRM".deals'),
        @('finance.invoices', 'select count(*) from finance.invoices'),
        @('"UPPER CASE"."UPPER TABLE"', 'select count(*) from "UPPER CASE"."UPPER TABLE"')
    )
    foreach ($o in $pgLegacy) { New-Row 'PG legacy' $o[0] (PgQueryOne 'legacy' $o[1]) }

    $myShopObjects = @(
        @('customers', 'SELECT COUNT(*) FROM shop.customers'),
        @('products', 'SELECT COUNT(*) FROM shop.products'),
        @('orders', 'SELECT COUNT(*) FROM shop.orders'),
        @('order_items', 'SELECT COUNT(*) FROM shop.order_items'),
        @('payments', 'SELECT COUNT(*) FROM shop.payments'),
        @('order_events (partitioned)', 'SELECT COUNT(*) FROM shop.order_events'),
        @('order_audit', 'SELECT COUNT(*) FROM shop.order_audit'),
        @('order (reserved word)', 'SELECT COUNT(*) FROM shop.`order`'),
        @('empty_table', 'SELECT COUNT(*) FROM shop.empty_table')
    )
    foreach ($o in $myShopObjects) { New-Row 'MySQL shop' $o[0] (MyQueryOne $o[1]) }

    New-Row 'MySQL legacy' 'databases on server' (MyQueryOne 'SELECT COUNT(*) FROM information_schema.SCHEMATA')
    New-Row 'MySQL legacy' 'legacy tables' (MyQueryOne "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='legacy' AND TABLE_TYPE='BASE TABLE'")
    New-Row 'MySQL legacy' 'legacy views' (MyQueryOne "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='legacy' AND TABLE_TYPE='VIEW'")
    New-Row 'MySQL legacy' 'legacy_* side databases' (MyQueryOne "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE 'legacy%' AND SCHEMA_NAME <> 'legacy'")

    $maShopObjects = @(
        @('customers', 'SELECT COUNT(*) FROM shop.customers'),
        @('products', 'SELECT COUNT(*) FROM shop.products'),
        @('orders', 'SELECT COUNT(*) FROM shop.orders'),
        @('order_items', 'SELECT COUNT(*) FROM shop.order_items'),
        @('payments', 'SELECT COUNT(*) FROM shop.payments'),
        @('invoices (SEQUENCE)', 'SELECT COUNT(*) FROM shop.invoices'),
        @('order_audit', 'SELECT COUNT(*) FROM shop.order_audit'),
        @('products history (FOR SYSTEM_TIME ALL)', 'SELECT COUNT(*) FROM shop.products FOR SYSTEM_TIME ALL'),
        @('order (reserved word)', 'SELECT COUNT(*) FROM shop.`order`'),
        @('empty_table', 'SELECT COUNT(*) FROM shop.empty_table')
    )
    foreach ($o in $maShopObjects) { New-Row 'MariaDB shop' $o[0] (MaQueryOne $o[1]) }

    New-Row 'MariaDB legacy' 'databases on server' (MaQueryOne 'SELECT COUNT(*) FROM information_schema.SCHEMATA')
    New-Row 'MariaDB legacy' 'legacy tables' (MaQueryOne "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='legacy' AND TABLE_TYPE='BASE TABLE'")
    New-Row 'MariaDB legacy' 'legacy views' (MaQueryOne "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='legacy' AND TABLE_TYPE='VIEW'")
    New-Row 'MariaDB legacy' 'legacy_* side databases' (MaQueryOne "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE 'legacy%' AND SCHEMA_NAME <> 'legacy'")

    # SQLite: rows come from gen_sqlite -verify
    if (-not $VerifyOnly) {
        $sqliteLines = $script:sqliteOut
    } else {
        $genExe = Join-Path $datasets 'gen_sqlite\gen_sqlite.exe'
        if (-not (Test-Path $genExe)) { throw 'gen_sqlite.exe missing; run without -VerifyOnly to build it' }
        $sqliteLines = @(& $genExe -outdir $sqliteDir -verify 2>&1)
        if ($LASTEXITCODE -ne 0) { throw 'gen_sqlite -verify failed' }
    }
    $sqliteChecks = @()
    foreach ($line in @($sqliteLines)) {
        if ($line -match '^(shop|weird)\.([^\t]+)\t(\d+)$') {
            $dbName = if ($Matches[1] -eq 'shop') { 'SQLite shop' } else { 'SQLite weird' }
            New-Row $dbName $Matches[2] $Matches[3]
        } elseif ($line -match '^(weird)\.check\.([^\t]+)\t(OK)$') {
            $sqliteChecks += $Matches[2]
        }
    }

    $script:rows | Format-Table -AutoSize -Wrap | Out-String -Width 140 | Write-Host

    # -----------------------------------------------------------------------
    # Verification: acceptance checks
    # -----------------------------------------------------------------------
    Write-Section 'Verification: acceptance checks'

    # ---- PostgreSQL shop
    $pgCount = {
        param([string]$sql)
        [int64](PgQueryOne 'shop' $sql)
    }
    Add-Check 'PG shop' 'customers = 200000' ((& $pgCount 'select count(*) from public.customers') -eq 200000) ((PgQueryOne 'shop' 'select count(*) from public.customers'))
    Add-Check 'PG shop' 'products = 50000' ((& $pgCount 'select count(*) from public.products') -eq 50000) ((PgQueryOne 'shop' 'select count(*) from public.products'))
    Add-Check 'PG shop' 'orders = 500000 (partitioned)' ((& $pgCount 'select count(*) from sales.orders') -eq 500000) ((PgQueryOne 'shop' 'select count(*) from sales.orders'))
    Add-Check 'PG shop' 'order_items = 1500000' ((& $pgCount 'select count(*) from sales.order_items') -eq 1500000) ((PgQueryOne 'shop' 'select count(*) from sales.order_items'))
    Add-Check 'PG shop' 'payments = 500000' ((& $pgCount 'select count(*) from sales.payments') -eq 500000) ((PgQueryOne 'shop' 'select count(*) from sales.payments'))
    Add-Check 'PG shop' 'reviews = 200000' ((& $pgCount 'select count(*) from public.reviews') -eq 200000) ((PgQueryOne 'shop' 'select count(*) from public.reviews'))
    Add-Check 'PG shop' 'events = 1000000 (jsonb)' ((& $pgCount 'select count(*) from analytics.events') -eq 1000000) ((PgQueryOne 'shop' 'select count(*) from analytics.events'))
    Add-Check 'PG shop' 'inventory = 50000' ((& $pgCount 'select count(*) from inventory.inventory') -eq 50000) ((PgQueryOne 'shop' 'select count(*) from inventory.inventory'))

    $schemaCount = PgQueryOne 'shop' "select count(*) from pg_namespace where nspname in ('public','sales','inventory','analytics','reporting','LEGACY_2019','Warehouse')"
    Add-Check 'PG shop' 'schemas public/sales/inventory/analytics/reporting + 2 quoted' ($schemaCount -eq '7') "found $schemaCount of 7"
    $viewCount = PgQueryOne 'shop' "select count(*) from pg_class c join pg_namespace n on n.oid=c.relnamespace where c.relkind='v' and n.nspname !~ '^pg_' and n.nspname <> 'information_schema'"
    $mvCount = PgQueryOne 'shop' "select count(*) from pg_class c join pg_namespace n on n.oid=c.relnamespace where c.relkind='m' and n.nspname !~ '^pg_'"
    Add-Check 'PG shop' 'views >= 3 and 1 materialized view' (([int]$viewCount -ge 3) -and ([int]$mvCount -eq 1)) "views=$viewCount matviews=$mvCount"
    $funcCount = PgQueryOne 'shop' "select count(*) from pg_proc p join pg_namespace n on n.oid=p.pronamespace where n.nspname in ('public','sales','analytics','inventory','reporting') and p.prokind in ('f')"
    $procCount = PgQueryOne 'shop' "select count(*) from pg_proc p join pg_namespace n on n.oid=p.pronamespace where n.nspname in ('public','sales','analytics','inventory','reporting') and p.prokind = 'p'"
    $trgCount = PgQueryOne 'shop' "select count(*) from pg_trigger where not tgisinternal"
    Add-Check 'PG shop' 'functions >= 5, procedure >= 1, trigger >= 1' (([int]$funcCount -ge 5) -and ([int]$procCount -ge 1) -and ([int]$trgCount -ge 1)) "functions=$funcCount procedures=$procCount triggers=$trgCount"
    $tableFnRows = PgQueryOne 'shop' "select count(*) from analytics.fn_orders_by_status('paid')"
    Add-Check 'PG shop' 'TABLE-returning PL/pgSQL function callable' ([int]$tableFnRows -gt 0) "rows=$tableFnRows"
    $matviewRows = PgQueryOne 'shop' 'select count(*) from analytics.mv_daily_events'
    Add-Check 'PG shop' 'materialized view populated' ([int]$matviewRows -gt 0) "rows=$matviewRows"
    $ginCount = PgQueryOne 'shop' "select count(*) from pg_indexes where schemaname not in ('pg_catalog','information_schema') and indexdef like '%USING gin%'"
    $partialCount = PgQueryOne 'shop' "select count(*) from pg_indexes where schemaname not in ('pg_catalog','information_schema') and indexdef like '%WHERE%'"
    Add-Check 'PG shop' 'GIN and partial indexes present' (([int]$ginCount -ge 4) -and ([int]$partialCount -ge 5)) "gin=$ginCount partial=$partialCount"
    $fkCount = PgQueryOne 'shop' "select count(*) from pg_constraint where contype='f'"
    $checkCount = PgQueryOne 'shop' "select count(*) from pg_constraint where contype='c'"
    $uniqCount = PgQueryOne 'shop' "select count(*) from pg_constraint where contype='u'"
    Add-Check 'PG shop' 'FK / CHECK / UNIQUE constraints present' (([int]$fkCount -ge 8) -and ([int]$checkCount -ge 10) -and ([int]$uniqCount -ge 6)) "fk=$fkCount check=$checkCount unique=$uniqCount"
    $wideCols = PgQueryOne 'shop' "select count(*) from information_schema.columns where table_schema='public' and table_name='wide_table_40'"
    Add-Check 'PG shop' '40-column table has 40 columns' ($wideCols -eq '40') "columns=$wideCols"
    $emptyRows = PgQueryOne 'shop' 'select count(*) from public.empty_table_0'
    Add-Check 'PG shop' 'empty_table_0 has 0 rows' ($emptyRows -eq '0') "rows=$emptyRows"
    $nullRows = PgQueryOne 'shop' 'select count(*) from public.null_only where a is null and b is null and c is null and d is null and e is null'
    Add-Check 'PG shop' 'null_only: 5 rows, all NULL' ($nullRows -eq '5') "rows=$nullRows"
    $bigTextLen = PgQueryOne 'shop' 'select length(content) from public.large_text where id = 1'
    $longLineLen = PgQueryOne 'shop' 'select length(long_line) from public.large_text where id = 1'
    Add-Check 'PG shop' '1MB text value and long single line' (($bigTextLen -eq '1048576') -and ($longLineLen -eq '1000000')) "content=$bigTextLen long_line=$longLineLen"
    $reservedRows = PgQueryOne 'shop' 'select count(*) from public."order"'
    Add-Check 'PG shop' 'quoted reserved-word table public."order"' ($reservedRows -eq '3') "rows=$reservedRows"
    $comments = PgQueryOne 'shop' "select count(*) from pg_description d join pg_class c on c.oid=d.objoid join pg_namespace n on n.oid=c.relnamespace where n.nspname !~ '^pg_' and n.nspname <> 'information_schema'"
    Add-Check 'PG shop' 'table/column comments present' ([int]$comments -ge 15) "comments=$comments"
    $cjkCustomers = PgQueryOne 'shop' "select count(*) from public.customers where first_name in ('王伟','李娜') or last_name in ('张','李','陈')"
    $emojiEvents = PgQueryOne 'shop' "select count(*) from public.reviews where body like '%👍%'"
    Add-Check 'PG shop' 'Unicode round-trip (CJK/emoji stored)' (([int]$cjkCustomers -gt 0) -and ([int]$emojiEvents -gt 0)) "cjk_customers=$cjkCustomers emoji_reviews=$emojiEvents"
    $u1 = PgQueryOne 'shop' 'select phrase from public.unicode_test where id = 1'
    $u3 = PgQueryOne 'shop' 'select phrase from public.unicode_test where id = 3'
    $u5 = PgQueryOne 'shop' 'select phrase from public.unicode_test where id = 5'
    Add-Check 'PG shop' 'Unicode round-trip (exact read-back)' (($u1 -eq '你好，世界！数据库测试') -and ($u3 -eq '🚀🎉😀🔥👍') -and ($u5 -eq 'こんにちは世界、テストです')) "id1=[$u1] id3=[$u3] id5=[$u5]"
    $auditRows = PgQueryOne 'shop' 'select count(*) from sales.order_audit'
    Add-Check 'PG shop' 'audit trigger fired' ([int]$auditRows -gt 0) "audit_rows=$auditRows"

    # ---- PostgreSQL legacy
    $legacySchemas = PgQueryOne 'legacy' "select count(*) from pg_namespace where nspname !~ '^pg_' and nspname <> 'information_schema'"
    $legacyTables = PgQueryOne 'legacy' "select count(*) from pg_class c join pg_namespace n on n.oid=c.relnamespace where c.relkind='r' and n.nspname !~ '^pg_' and n.nspname <> 'information_schema'"
    Add-Check 'PG legacy' '12 schemas / 27 tables' (($legacySchemas -eq '12') -and ($legacyTables -eq '27')) "schemas=$legacySchemas tables=$legacyTables"
    $emptyLegacy = PgQueryOne 'legacy' 'select count(*) from "archive_2019"."Old Orders"'
    Add-Check 'PG legacy' 'empty legacy table present' ($emptyLegacy -eq '0') "rows=$emptyLegacy"

    # ---- MySQL shop
    Add-Check 'MySQL shop' 'customers = 150000' ((MyQueryOne 'SELECT COUNT(*) FROM shop.customers') -eq '150000') (MyQueryOne 'SELECT COUNT(*) FROM shop.customers')
    Add-Check 'MySQL shop' 'products = 40000' ((MyQueryOne 'SELECT COUNT(*) FROM shop.products') -eq '40000') (MyQueryOne 'SELECT COUNT(*) FROM shop.products')
    Add-Check 'MySQL shop' 'orders = 300000' ((MyQueryOne 'SELECT COUNT(*) FROM shop.orders') -eq '300000') (MyQueryOne 'SELECT COUNT(*) FROM shop.orders')
    Add-Check 'MySQL shop' 'order_items = 800000' ((MyQueryOne 'SELECT COUNT(*) FROM shop.order_items') -eq '800000') (MyQueryOne 'SELECT COUNT(*) FROM shop.order_items')
    $myFulltext = MyQueryOne "SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA='shop' AND INDEX_TYPE='FULLTEXT'"
    Add-Check 'MySQL shop' 'FULLTEXT index present' ([int]$myFulltext -ge 1) "fulltext_index_columns=$myFulltext"
    $myGenerated = MyQueryOne "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='shop' AND EXTRA LIKE '%GENERATED%'"
    Add-Check 'MySQL shop' 'generated columns present' ([int]$myGenerated -ge 2) "generated_columns=$myGenerated"
    $myPartitions = MyQueryOne "SELECT COUNT(*) FROM information_schema.PARTITIONS WHERE TABLE_SCHEMA='shop' AND TABLE_NAME='order_events' AND PARTITION_NAME IS NOT NULL"
    Add-Check 'MySQL shop' 'partitioned InnoDB table (4 partitions)' ($myPartitions -eq '4') "partitions=$myPartitions"
    $myJson = MyQueryOne "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='shop' AND DATA_TYPE='json'"
    Add-Check 'MySQL shop' 'JSON columns present' ([int]$myJson -ge 5) "json_columns=$myJson"
    $myFk = MyQueryOne "SELECT COUNT(*) FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE CONSTRAINT_SCHEMA='shop'"
    Add-Check 'MySQL shop' 'foreign keys present' ([int]$myFk -ge 4) "fks=$myFk"
    $myRoutines = MyQueryOne "SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA='shop'"
    $myTriggers = MyQueryOne "SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA='shop'"
    Add-Check 'MySQL shop' 'procedure + function + trigger' (([int]$myRoutines -eq 2) -and ([int]$myTriggers -eq 1)) "routines=$myRoutines triggers=$myTriggers"
    $myReserved = MyQueryOne 'SELECT COUNT(*) FROM shop.`order`'
    Add-Check 'MySQL shop' 'reserved-word table shop.`order`' ($myReserved -eq '3') "rows=$myReserved"
    $myUnicode = MyQueryOne 'SELECT full_name FROM shop.customers WHERE id = 4'
    $myEmoji = MyQueryOne "SELECT COUNT(*) FROM shop.products WHERE description LIKE '%🚀%'"
    Add-Check 'MySQL shop' 'Unicode round-trip (CJK/emoji)' (($myUnicode -eq '王伟 李') -and ([int]$myEmoji -gt 0)) "customer4=[$myUnicode] emoji_products=$myEmoji"

    # ---- MySQL legacy
    $myDbs = MyQueryOne 'SELECT COUNT(*) FROM information_schema.SCHEMATA'
    $myLegacyTables = MyQueryOne "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='legacy' AND TABLE_TYPE='BASE TABLE'"
    Add-Check 'MySQL legacy' '12 databases / 8 legacy tables / >=6 side DBs' (($myDbs -eq '12') -and ($myLegacyTables -eq '8')) "databases=$myDbs legacy_tables=$myLegacyTables"
    $myWeirdTables = MyQueryOne "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='legacy' AND TABLE_NAME IN ('order details','mixedcasetable','order','emptytable')"
    Add-Check 'MySQL legacy' 'mixed-case / spaces / reserved / empty tables' ([int]$myWeirdTables -eq 4) "matched=$myWeirdTables"

    # ---- MariaDB shop
    Add-Check 'MariaDB shop' 'customers = 100000' ((MaQueryOne 'SELECT COUNT(*) FROM shop.customers') -eq '100000') (MaQueryOne 'SELECT COUNT(*) FROM shop.customers')
    Add-Check 'MariaDB shop' 'products = 30000' ((MaQueryOne 'SELECT COUNT(*) FROM shop.products') -eq '30000') (MaQueryOne 'SELECT COUNT(*) FROM shop.products')
    Add-Check 'MariaDB shop' 'orders = 200000' ((MaQueryOne 'SELECT COUNT(*) FROM shop.orders') -eq '200000') (MaQueryOne 'SELECT COUNT(*) FROM shop.orders')
    Add-Check 'MariaDB shop' 'order_items = 300000' ((MaQueryOne 'SELECT COUNT(*) FROM shop.order_items') -eq '300000') (MaQueryOne 'SELECT COUNT(*) FROM shop.order_items')
    $maVersioned = MaQueryOne "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='shop' AND TABLE_NAME='products' AND TABLE_TYPE='SYSTEM VERSIONED'"
    $maHistory = MaQueryOne 'SELECT COUNT(*) FROM shop.products FOR SYSTEM_TIME ALL'
    Add-Check 'MariaDB shop' 'system-versioned products + history' (($maVersioned -eq '1') -and ([int]$maHistory -gt 30000)) "versioned=$maVersioned rows_with_history=$maHistory"
    $maSeq = MaQueryOne "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='shop' AND TABLE_TYPE='SEQUENCE'"
    $maInvoiceMin = MaQueryOne 'SELECT MIN(invoice_no) FROM shop.invoices'
    Add-Check 'MariaDB shop' 'SEQUENCE + sequence-backed invoices' (($maSeq -eq '1') -and ($maInvoiceMin -eq '500000')) "sequences=$maSeq min_invoice_no=$maInvoiceMin"
    $maDefiner = MaQueryOne "SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA='shop' AND DEFINER='tdb@%'"
    Add-Check 'MariaDB shop' 'DEFINER routines (procedure + function)' ($maDefiner -eq '2') "definer_tdb_routines=$maDefiner"
    $maJsonChecks = MaQueryOne "SELECT COUNT(*) FROM information_schema.CHECK_CONSTRAINTS WHERE CONSTRAINT_SCHEMA='shop' AND CHECK_CLAUSE LIKE '%json_valid%'"
    Add-Check 'MariaDB shop' 'JSON alias columns (json_valid checks)' ([int]$maJsonChecks -ge 4) "json_valid_checks=$maJsonChecks"
    $maGenerated = MaQueryOne "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='shop' AND EXTRA LIKE '%GENERATED%'"
    Add-Check 'MariaDB shop' 'generated columns present' ([int]$maGenerated -ge 2) "generated_columns=$maGenerated"
    $maUnicode = MaQueryOne 'SELECT full_name FROM shop.customers WHERE id = 4'
    $maEmoji = MaQueryOne "SELECT COUNT(*) FROM shop.products WHERE name LIKE '%😀%'"
    Add-Check 'MariaDB shop' 'Unicode round-trip (CJK/emoji)' (($maUnicode -eq '王伟 李') -and ([int]$maEmoji -gt 0)) "customer4=[$maUnicode] emoji_products=$maEmoji"

    # ---- MariaDB legacy
    $maDbs = MaQueryOne 'SELECT COUNT(*) FROM information_schema.SCHEMATA'
    $maLegacyTables = MaQueryOne "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='legacy' AND TABLE_TYPE='BASE TABLE'"
    Add-Check 'MariaDB legacy' '12 databases / 8 legacy tables / >=6 side DBs' (($maDbs -eq '12') -and ($maLegacyTables -eq '8')) "databases=$maDbs legacy_tables=$maLegacyTables"

    # ---- SQLite
    $sq = @{}
    foreach ($line in @($sqliteLines)) {
        if ($line -match '^(shop|weird)\.([^\t]+)\t(\d+)$') { $sq["$($Matches[1]).$($Matches[2])"] = [int]$Matches[3] }
    }
    $sqliteTotal = [int]($sq['shop.users']) + [int]($sq['shop.orders']) + [int]($sq['shop.order_items'])
    Add-Check 'SQLite shop' 'users+orders+items = 600000' ($sqliteTotal -eq 600000) "users=$($sq['shop.users']) orders=$($sq['shop.orders']) items=$($sq['shop.order_items'])"
    Add-Check 'SQLite weird' 'edge-case schema loaded' (([int]$sq['weird.select'] -ge 1) -and ([int]$sq['weird.order'] -ge 1) -and ([int]$sq['weird.empty table'] -eq 0)) "select=$($sq['weird.select']) order=$($sq['weird.order']) empty=$($sq['weird.empty table'])"
    $sqliteUnicode = ($sqliteChecks -contains 'unicode_roundtrip') -and ($sqliteChecks -contains 'unicode_cjk')
    Add-Check 'SQLite weird' 'Unicode round-trip checks' $sqliteUnicode "checks=$($sqliteChecks -join ',')"
    Add-Check 'SQLite weird' '1MB text + generated column + trigger checks' (($sq['weird.big text'] -eq 1) -and ($sq['weird.check.big_text_length'] -eq 1048576) -and ($sqliteChecks -contains 'generated_stored') -and ($sqliteChecks -contains 'trigger_audit')) "checks=$($sqliteChecks -join ',')"

    $script:checks | Format-Table -AutoSize -Wrap | Out-String -Width 200 | Write-Host

    # -----------------------------------------------------------------------
    # Timing summary
    # -----------------------------------------------------------------------
    Write-Section 'Timing summary'
    $timingRows = foreach ($k in $script:timings.Keys) {
        [pscustomobject]@{ Step = $k; Seconds = $script:timings[$k] }
    }
    $timingRows | Format-Table -AutoSize | Out-String -Width 120 | Write-Host

    $totalSw.Stop()
    Write-Host ("TOTAL load+verify time: {0:n1}s" -f $totalSw.Elapsed.TotalSeconds) -ForegroundColor Green
}
catch {
    $fatal = $_
} finally {
    $totalSw.Stop()
    if ($fatal) {
        Write-Host ''
        Write-Host "FATAL: $($fatal.Exception.Message)" -ForegroundColor Red
        Write-Host ("TOTAL elapsed: {0:n1}s" -f $totalSw.Elapsed.TotalSeconds)
    }
    if (-not $fatal) {
        if ($script:failChecks -gt 0) {
            Write-Host "STATUS: FAIL ($script:failChecks acceptance check(s) failed)" -ForegroundColor Red
        } else {
            Write-Host 'STATUS: PASS - all datasets loaded and verified' -ForegroundColor Green
        }
    }
    Write-Host ''
    Write-Host "Log: $logPath"
    if ($transcript) {
        try { Stop-Transcript | Out-Null } catch { }
    }
}

if ($fatal) { exit 1 }
if ($script:failChecks -gt 0) { exit 2 }
exit 0
