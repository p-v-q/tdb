# Shared environment for TDB integration testing.
# Dot-source from any shell:  . C:\Users\qxwxi\repos\tdb\.itest\env.ps1
$Root = 'C:\Users\qxwxi\repos\tdb'
$env:GOPATH = Join-Path $Root '.gopath'
$env:GOMODCACHE = Join-Path $Root '.gopath\pkg\mod'
$env:GOCACHE = Join-Path $Root '.gocache'
$env:GOTMPDIR = Join-Path $Root '.gotmp'
$env:TMP = $env:GOTMPDIR
$env:TEMP = $env:GOTMPDIR
$env:GOTOOLCHAIN = 'local'
$PGBIN = Join-Path $Root '.dbs\pgsql\bin'
$MYBIN = Join-Path $Root '.dbs\mysql-8.4.4-winx64\bin'
$MABIN = Join-Path $Root '.dbs\mariadb-11.4.4-winx64\bin'
$env:PATH = "$env:LOCALAPPDATA\Programs\Go\bin;$PGBIN;$MYBIN;$MABIN;$env:PATH"
$env:PGPORT = '55432'
$env:PGHOST = '127.0.0.1'
$env:PGUSER = 'postgres'
$env:TD = $Root
$env:ART = Join-Path $Root '.artifacts'
