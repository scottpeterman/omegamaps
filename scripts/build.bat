@echo off
REM scripts\build.bat
REM
REM Windows build: vet, tests, and the command-line tools. Needs only Go --
REM nothing here uses cgo, so no C toolchain. That changes when the Qt
REM application lands; see docs\BUILDING.md.
REM
REM   scripts\build.bat                    vet, test, build
REM   scripts\build.bat --no-test          build only
REM   scripts\build.bat --version v0.2.0   stamp this (default: git describe)
REM
REM Output: build\bin\<tool>.exe
REM
REM Tests run without -race, which needs cgo and mingw on Windows. The race
REM build is covered by scripts/build.sh on Linux and macOS.

setlocal
cd /d "%~dp0.."
set "ROOT=%CD%"
set "RUN_TESTS=1"
set "VERSION="

:parse
if "%~1"=="" goto parsed
if /i "%~1"=="--no-test" (set "RUN_TESTS=0" & shift & goto parse)
if /i "%~1"=="--version" (
    if "%~2"=="" (echo error: --version needs a value 1>&2 & exit /b 2)
    set "VERSION=%~2" & shift & shift & goto parse
)
echo unknown option: %~1 1>&2
exit /b 2
:parsed

REM --- preflight -------------------------------------------------------------
where go >nul 2>&1 || (echo error: go is not on PATH 1>&2 & exit /b 1)
if not exist "go.mod" (
    echo error: no go.mod at %ROOT% 1>&2
    echo   The module file is missing from this working copy. See docs\BUILDING.md. 1>&2
    exit /b 1
)
if not exist "go.sum" (
    echo ==^> go mod tidy, no go.sum yet
    go mod tidy || exit /b 1
)

if "%VERSION%"=="" (
    for /f "delims=" %%V in ('git describe --tags --always --dirty 2^>nul') do set "VERSION=%%V"
)
set "LDFLAGS=-s -w"
if not "%VERSION%"=="" set "LDFLAGS=-s -w -X github.com/scottpeterman/omegamaps/internal/buildinfo.Version=%VERSION%"
set "CGO_ENABLED=0"

REM --- vet and test ----------------------------------------------------------
if "%RUN_TESTS%"=="1" (
    echo ==^> go vet
    go vet ./... || exit /b 1
    echo ==^> go test
    go test -count=1 ./... || exit /b 1
    if "%PFSNMP_TEST_TARGET%"=="" (
        echo ==^> SNMP live tests: SKIPPED -- PFSNMP_TEST_TARGET is not set
        echo     snmpprobe and the vault-backed SNMP path are unverified against a real agent.
    )
)

REM --- build -----------------------------------------------------------------
if not exist "build\bin" mkdir "build\bin"
for /d %%D in (cmd\*) do (
    if exist "%%D\*.go" (
        echo ==^> build %%~nxD
        go build -trimpath -ldflags "%LDFLAGS%" -o "build\bin\%%~nxD.exe" ".\%%D" || exit /b 1
    )
)

echo.
echo artifacts:
for %%F in (build\bin\*.exe) do echo   %%F
echo.
build\bin\crawl.exe -version
endlocal
