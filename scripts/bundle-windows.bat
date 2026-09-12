@echo off
REM scripts\bundle-windows.bat
REM
REM Packages omegamaps as a self-contained folder using windeployqt.
REM
REM   scripts\bundle-windows.bat
REM   scripts\bundle-windows.bat --qt C:\Qt\6.10.3\msvc2022_64 --zip
REM   scripts\bundle-windows.bat --no-viewer
REM
REM Run it from an "x64 Native Tools Command Prompt for VS 2022", with a
REM mingw-w64 gcc also on PATH. Both, not either: see TWO COMPILERS below.
REM
REM This is scripts\bundle-windows.bat from omegassh with the anytermqt
REM handling removed, the gcc check added, and the themes copy dropped --
REM omegamaps keeps its icons and stylesheet in app\resources.qrc, so they are
REM inside the executable and there is no Resources\ directory to stage.
REM
REM TWO COMPILERS. Nothing in omegassh needed this. The run model here is a Go
REM c-archive (capi\), and cgo on Windows compiles with gcc -- there is no MSVC
REM cgo backend. link.exe then links that archive into the Qt application. So
REM the build needs mingw-w64 gcc to produce the archive and MSVC to consume
REM it, and the two have to agree on the target: an i686 gcc, or MSYS2's
REM "msys" (not "mingw64") gcc, produces an archive that either fails to link
REM or links into an image the loader refuses with 0xc000007b -- a dialog that
REM names no DLL and no reason. gcc -dumpmachine is checked below because that
REM is the cheapest place to catch it.
REM
REM DEPENDENCIES, and how each is found. Qt takes a flag, falls back to an
REM environment variable, then guesses; whatever it settles on is echoed before
REM the build, so a wrong guess is visible at the top rather than inferred from
REM a compiler error two hundred lines down.
REM
REM   Qt          --qt <prefix>
REM               %OMEGAMAPS_QT%
REM               %CMAKE_PREFIX_PATH%
REM               %Qt6_DIR%  (walked back up from lib\cmake\Qt6)
REM               the newest C:\Qt\6.*\msvc*_64
REM
REM windeployqt is taken from the Qt prefix, NOT from PATH. It must come from
REM the Qt the application linked against: a windeployqt from a different Qt
REM copies DLLs that do not match what the binary asks for, and the result runs
REM on the build machine and fails on a clean one. One variable feeding both
REM makes them agree by construction rather than by luck.
REM
REM Options:
REM   --zip              also write dist\omegamaps-windows-x64.zip
REM   --no-viewer        skip omegamaps-viewer and its WebEngine payload
REM   --build-dir <dir>  default build-win
REM   --help

setlocal enabledelayedexpansion

set "ROOT=%~dp0.."
pushd "%ROOT%"

set "QTPREFIX="
set "MAKEZIP=0"
set "WANTVIEWER=1"
set "BUILD_DIR=build-win"
set "STAGE=dist\omegamaps"

REM --- options ---------------------------------------------------------------

:parse
if "%~1"=="" goto parsed
if /i "%~1"=="--zip" (set "MAKEZIP=1" & shift & goto parse)
if /i "%~1"=="--no-viewer" (set "WANTVIEWER=0" & shift & goto parse)
if /i "%~1"=="--qt" (
    if "%~2"=="" (echo error: --qt needs a path 1>&2 & goto fail)
    set "QTPREFIX=%~f2" & shift & shift & goto parse
)
if /i "%~1"=="--build-dir" (
    if "%~2"=="" (echo error: --build-dir needs a path 1>&2 & goto fail)
    set "BUILD_DIR=%~2" & shift & shift & goto parse
)
if /i "%~1"=="--help" goto usage
if /i "%~1"=="-h" goto usage
echo error: unknown option: %~1 1>&2
goto fail
:parsed

REM --- Qt --------------------------------------------------------------------

if "%QTPREFIX%"=="" if not "%OMEGAMAPS_QT%"=="" set "QTPREFIX=%OMEGAMAPS_QT%"
if "%QTPREFIX%"=="" if not "%CMAKE_PREFIX_PATH%"=="" set "QTPREFIX=%CMAKE_PREFIX_PATH%"

REM Qt6_DIR points at <prefix>\lib\cmake\Qt6, so walk back up three.
if "%QTPREFIX%"=="" if not "%Qt6_DIR%"=="" (
    for %%P in ("%Qt6_DIR%\..\..\..") do set "QTPREFIX=%%~fP"
)

REM Newest first, and NOT with dir /o-n. That sorts as TEXT, which puts 6.8.3
REM above 6.10.3 -- and the resulting mismatch is the exact thing this script
REM is supposed to prevent, because it deploys one Qt's DLLs beside a binary
REM linked against another. The parts are compared as numbers instead, in
REM :findqt below.
REM
REM CALLED, NOT INLINED, and that is not tidiness. This used to be a for /f
REM around a PowerShell one-liner sitting inside "if ... if exist ( ... )".
REM cmd parses a parenthesised block in full before running any of it, which
REM eats one level of caret escaping -- so the ^\| inside the for /f command
REM string became a real pipe, split the statement, and the loop produced
REM nothing. QTPREFIX stayed empty and the script reported "no Qt prefix" on a
REM machine with Qt exactly where it was looking. A subroutine is parsed when
REM it is called, at which point single carets mean what they say.
if "%QTPREFIX%"=="" call :findqt

if not "%QTPREFIX%"=="" if "%QTGUESSED%"=="1" (
    echo ==^> guessed Qt at %QTPREFIX%
    echo     ^(pass --qt or set OMEGAMAPS_QT to choose a different one^)
)

if "%QTPREFIX%"=="" (
    echo error: no Qt prefix. Either:
    echo         scripts\bundle-windows.bat --qt C:\Qt\6.10.3\msvc2022_64
    echo     or  set OMEGAMAPS_QT=C:\Qt\6.10.3\msvc2022_64
    if exist "C:\Qt" (
        echo     C:\Qt exists but holds no 6.x directory with an msvc*_64 kit:
        for /f "delims=" %%D in ('dir /b /ad "C:\Qt" 2^>nul') do echo         C:\Qt\%%D
    ) else (
        echo     C:\Qt does not exist, so nothing could be guessed.
    )
    goto fail
)

REM Strip a trailing backslash once, here. It is harmless in every path this
REM script builds -- "%QTPREFIX%\bin\windeployqt.exe" tolerates the double
REM separator -- but the last gate compares %QTPREFIX% against the cached
REM Qt6_DIR as a substring, and a prefix ending in \ becomes one ending in /
REM that the cache entry does not contain. The result is the bundle failing
REM its own consistency check over a typed backslash.
if "%QTPREFIX:~-1%"=="\" set "QTPREFIX=%QTPREFIX:~0,-1%"
if not exist "%QTPREFIX%\bin\windeployqt.exe" (
    echo error: no windeployqt.exe under %QTPREFIX%\bin 1>&2
    echo     That does not look like a Qt prefix. It should be the directory
    echo     holding bin\, lib\ and include\ -- e.g. C:\Qt\6.10.3\msvc2022_64,
    echo     not C:\Qt and not the lib\cmake\Qt6 inside it.
    goto fail
)

REM A MINGW Qt CANNOT BE USED HERE even though mingw gcc is also required. The
REM Qt libraries are linked by link.exe into an MSVC binary; a mingw_64 Qt
REM exports a different C++ ABI and link.exe cannot resolve a symbol of it.
REM The failure is several hundred unresolved Qt symbols, which reads like a
REM missing library rather than the wrong kit.
echo %QTPREFIX% | findstr /i /c:"mingw" >nul
if not errorlevel 1 (
    echo error: %QTPREFIX% looks like a MinGW Qt kit. 1>&2
    echo     The application is linked by MSVC, so Qt must be an msvc*_64 kit.
    echo     mingw gcc is still needed -- but only to compile the Go c-archive.
    goto fail
)

REM --- toolchain -------------------------------------------------------------

where cl.exe >nul 2>nul
if errorlevel 1 (
    echo error: cl.exe not on PATH. 1>&2
    echo     Run this from an "x64 Native Tools Command Prompt for VS 2022",
    echo     or run vcvars64.bat in this shell first.
    goto fail
)

where go.exe >nul 2>nul
if errorlevel 1 (
    echo error: go.exe not on PATH; the run model is a Go c-archive. 1>&2
    goto fail
)

where gcc.exe >nul 2>nul
if errorlevel 1 (
    echo error: gcc.exe not on PATH. 1>&2
    echo     cgo has no MSVC backend: capi\ is compiled by mingw-w64 gcc and
    echo     only then linked by link.exe. Install the MSYS2 mingw-w64 x86_64
    echo     toolchain -- or w64devkit -- and put its bin\ on PATH.
    echo         pacman -S mingw-w64-x86_64-gcc
    echo     and use C:\msys64\mingw64\bin, NOT C:\msys64\usr\bin.
    goto fail
)

REM The target triple, not the version. See TWO COMPILERS at the top.
set "GCCTRIPLE="
for /f "delims=" %%T in ('gcc -dumpmachine 2^>nul') do set "GCCTRIPLE=%%T"
echo !GCCTRIPLE! | findstr /i /c:"x86_64-w64-mingw32" >nul
if errorlevel 1 (
    echo error: gcc targets !GCCTRIPLE!, not x86_64-w64-mingw32. 1>&2
    echo     A 32-bit or MSYS-target gcc produces a c-archive that link.exe
    echo     either rejects or links into an image the loader refuses with
    echo     0xc000007b at startup -- a dialog that names no DLL.
    for /f "delims=" %%W in ('where gcc.exe') do echo         found: %%W
    goto fail
)

REM --- what it settled on ----------------------------------------------------

echo ==^> Qt         %QTPREFIX%
echo ==^> gcc        !GCCTRIPLE!
echo ==^> build dir  %BUILD_DIR%
if "%WANTVIEWER%"=="0" echo ==^> viewer     skipped ^(--no-viewer^)

REM --- build -----------------------------------------------------------------

REM A CACHED Qt6_DIR BEATS -DCMAKE_PREFIX_PATH, so an existing build directory
REM configured against a different Qt keeps using it and says nothing: the
REM binary links one Qt while windeployqt below deploys another. Wiping is the
REM only reliable answer -- CMake offers no way to un-cache a find_package
REM result -- and a reconfigure costs seconds next to a mismatched package that
REM fails on somebody else's machine.
if exist "%BUILD_DIR%\CMakeCache.txt" (
    findstr /c:"Qt6_DIR:PATH=" "%BUILD_DIR%\CMakeCache.txt" > "%TEMP%\omegamaps_qtdir.txt" 2>nul
    set "CACHED="
    for /f "tokens=2 delims==" %%V in ('type "%TEMP%\omegamaps_qtdir.txt"') do set "CACHED=%%V"
    del "%TEMP%\omegamaps_qtdir.txt" >nul 2>nul
    if not "!CACHED!"=="" (
        echo !CACHED! | findstr /i /c:"%QTPREFIX:\=/%" >nul
        if errorlevel 1 (
            echo ==^> %BUILD_DIR% was configured against a different Qt:
            echo         cached: !CACHED!
            echo         wanted: %QTPREFIX%
            echo     wiping it, or the build and windeployqt would disagree.
            rmdir /s /q "%BUILD_DIR%"
        )
    )
)

set "VIEWEROPT=-DOMEGAMAPS_BUILD_VIEWER=ON"
if "%WANTVIEWER%"=="0" set "VIEWEROPT=-DOMEGAMAPS_BUILD_VIEWER=OFF"

echo ==^> configuring
cmake -S . -B "%BUILD_DIR%" -DCMAKE_PREFIX_PATH="%QTPREFIX%" ^
      -DCMAKE_BUILD_TYPE=Release -DOMEGAMAPS_BUILD_TESTS=OFF %VIEWEROPT%
if errorlevel 1 goto fail

echo ==^> building
cmake --build "%BUILD_DIR%" --config Release --target omegamaps
if errorlevel 1 goto fail

REM WHETHER THE VIEWER WAS BUILT IS NOT WHETHER IT WAS ASKED FOR. The top-level
REM CMakeLists downgrades a missing or incomplete Qt WebEngine to a warning and
REM builds the application without the viewer -- deliberately, because WebEngine
REM is a separate download that several Qt kits do not carry. A warning scrolls
REM past in a few hundred lines of MSBuild output, so the target is only built
REM if it exists, and its absence is stated again at the end where it is read.
set "HAVEVIEWER=0"
if "%WANTVIEWER%"=="1" (
    cmake --build "%BUILD_DIR%" --config Release --target omegamaps-viewer >nul 2>nul
    if not errorlevel 1 set "HAVEVIEWER=1"
)

REM Multi-config generators (MSBuild, the default here) put it under Release\;
REM single-config ones (Ninja) do not.
set "APPDIR=%BUILD_DIR%\app\Release"
if not exist "%APPDIR%\omegamaps.exe" set "APPDIR=%BUILD_DIR%\app"
if not exist "%APPDIR%\omegamaps.exe" (
    echo error: no omegamaps.exe after the build 1>&2
    echo     Looked in %BUILD_DIR%\app\Release\ and %BUILD_DIR%\app\
    goto fail
)
if "%HAVEVIEWER%"=="1" if not exist "%APPDIR%\omegamaps-viewer.exe" set "HAVEVIEWER=0"

REM --- stage -----------------------------------------------------------------

echo ==^> staging into %STAGE%
if exist "%STAGE%" rmdir /s /q "%STAGE%"
mkdir "%STAGE%"

copy /y "%APPDIR%\omegamaps.exe" "%STAGE%\omegamaps.exe" >nul
if errorlevel 1 goto fail
if "%HAVEVIEWER%"=="1" (
    REM Beside the application, not in a subdirectory: app\viewerlaunch.cpp
    REM looks for omegamaps-viewer.exe in QCoreApplication::applicationDirPath.
    copy /y "%APPDIR%\omegamaps-viewer.exe" "%STAGE%\omegamaps-viewer.exe" >nul
    if errorlevel 1 goto fail
)

REM --- the command-line tools ------------------------------------------------
REM
REM Built here rather than taken from build\bin, which may be stale or absent
REM on a machine that has only ever run this script. They are pure Go with
REM CGO_ENABLED=0 -- no cgo, so no gcc and no DLLs, and they run from the same
REM folder with nothing else installed.
REM
REM IN THE PACKAGE BECAUSE THE PACKAGE IS FOR SOMEBODY WITHOUT A TOOLCHAIN.
REM omvault is how a vault gets created and how a forgotten one gets listed;
REM telling a Windows user to install Go first to reach it defeats the point
REM of shipping a zip.
echo ==^> building the command-line tools
REM Set once, outside the loop, and cleared after. CGO_ENABLED=0 is what makes
REM these three standalone: with cgo on they would link against the mingw
REM runtime and stop being a copy-anywhere executable.
set "CGO_ENABLED=0"
for %%T in (crawl omvault mapview) do (
    go build -trimpath -o "%STAGE%\%%T.exe" "./cmd/%%T"
    if errorlevel 1 (
        echo error: go build ./cmd/%%T failed 1>&2
        set "CGO_ENABLED="
        goto fail
    )
)
set "CGO_ENABLED="

REM --- licensing -------------------------------------------------------------
REM
REM NOT OPTIONAL, AND NOT A TIDINESS ITEM. omegamaps is conveyed under GPLv3,
REM which requires the licence text to accompany the binary; the MIT and
REM BSD components require their copyright notices and warranty disclaimers to
REM be reproduced in the materials distributed with a binary. A zip containing
REM only executables satisfies neither, and THIRD_PARTY_NOTICES.md says so
REM about itself in its own first paragraph.
echo ==^> staging the licences
if not exist "LICENSE" (
    echo error: LICENSE is missing from the repository. 1>&2
    echo     GPLv3 requires it to accompany the binary.
    goto fail
)
copy /y "LICENSE" "%STAGE%\" >nul
if errorlevel 1 goto fail

REM THE WHOLE DIRECTORY, not a file list. licenses\ holds the notices file and
REM the GPL, LGPL and Apache texts it points at BY RELATIVE PATH -- the Qt
REM entry names LGPL-3.0.txt "beside this file". Copying the notices without
REM its neighbours produces a package that cites licence texts it does not
REM carry, which is worse than one that never mentions them.
if not exist "licenses\THIRD_PARTY_NOTICES.md" (
    echo error: licenses\THIRD_PARTY_NOTICES.md is missing. 1>&2
    echo     The MIT and BSD components require their notices to be reproduced
    echo     in the materials distributed with a binary. Generate it with:
    echo         python3 scripts\gen-third-party-notices.py
    goto fail
)
xcopy /e /i /q /y "licenses" "%STAGE%\licenses" >nul
if errorlevel 1 goto fail

REM --- windeployqt -----------------------------------------------------------
REM
REM Once per executable, into the same directory. Running it on omegamaps.exe
REM alone is not enough when the viewer is present: windeployqt walks the
REM import table of the binary it is given, and Qt6WebEngineWidgets is imported
REM by omegamaps-viewer.exe only. The application deploys clean, the viewer
REM starts and immediately exits, and the application reports that the viewer
REM is not installed -- which is true but for the wrong reason.
REM
REM --no-translations is NOT passed. omegassh could, having no WebEngine;
REM here dropping translations risks taking qtwebengine_locales with them, and
REM WebEngine fails to initialise without a locale .pak. A few hundred KB of
REM .qm files is not worth the class of bug.

echo ==^> running windeployqt from %QTPREFIX%\bin
"%QTPREFIX%\bin\windeployqt.exe" --release --no-system-d3d-compiler ^
    --compiler-runtime "%STAGE%\omegamaps.exe"
if errorlevel 1 goto fail

if "%HAVEVIEWER%"=="1" (
    "%QTPREFIX%\bin\windeployqt.exe" --release --no-system-d3d-compiler ^
        "%STAGE%\omegamaps-viewer.exe"
    if errorlevel 1 goto fail
)

REM --- the MSVC runtime ------------------------------------------------------
REM
REM WITHOUT THIS THE PACKAGE DIES ON A CLEAN BOX and runs fine here, because
REM this machine has the VS 2022 redistributable installed and the loader finds
REM it system-wide. omegamaps.exe is MSVC-built, so it needs VCRUNTIME140.dll,
REM VCRUNTIME140_1.dll and MSVCP140.dll beside it or on the target machine.
REM
REM --compiler-runtime above is asked for but not trusted. It locates the
REM redistributable through %VCINSTALLDIR%, which only a Developer Command
REM Prompt sets, and depending on the Qt version it may drop vc_redist.x64.exe
REM into the folder INSTEAD of the DLLs. An installer sitting in a directory is
REM not a deployed runtime, and the folder looks equally plausible either way.
REM So the DLLs are checked by name and copied if they are absent.
REM
REM ucrtbase.dll is deliberately not in the list. The UCRT ships with Windows
REM itself from 10 onward, which is below anything this targets.

set "CRTOK=1"
for %%F in (VCRUNTIME140.dll VCRUNTIME140_1.dll MSVCP140.dll) do (
    if not exist "%STAGE%\%%F" set "CRTOK=0"
)

if "!CRTOK!"=="0" (
    if "%VCToolsRedistDir%"=="" (
        echo error: MSVC runtime DLLs missing from %STAGE%, and 1>&2
        echo         %%VCToolsRedistDir%% is unset so they cannot be located.
        echo     Run this from an "x64 Native Tools Command Prompt for VS 2022";
        echo     that is what sets it. A plain cmd with cl.exe on PATH is not
        echo     enough -- the build works and the deployment silently does not.
        goto fail
    )
    REM Newest CRT directory wins. There is normally one, but a machine with
    REM several VS toolsets installed can have more. In a subroutine for the
    REM same reason :findqt is: this for /f used to sit in this block, where
    REM the 2^>nul was un-escaped by the block parse and redirected the FOR
    REM statement itself.
    call :findcrt
    if "!CRTDIR!"=="" (
        REM !VAR! AND NOT %VAR%, and this is not style. Percent-expansion
        REM happens when cmd parses this whole if-block, before it runs a line
        REM of it -- so on a machine where VS lives under
        REM "C:\Program Files (x86)\..." the ")" in "(x86)" closes the block
        REM early and the rest of the path becomes a stray command:
        REM "\Microsoft was unexpected at this time." Delayed expansion happens
        REM at execution, after the parsing that would have broken.
        echo error: no Microsoft.VC*.CRT directory under 1>&2
        echo         !VCToolsRedistDir!x64
        echo     The VS installation has no redistributable component. Add
        echo     "MSVC v143 - VS 2022 C++ x64/x86 Redistributable MSMs" -- or
        echo     just the latest v143 build tools -- in the VS Installer.
        goto fail
    )
    echo ==^> copying the MSVC runtime from !CRTDIR!
    copy /y "!CRTDIR!\VCRUNTIME140.dll"   "%STAGE%\" >nul
    copy /y "!CRTDIR!\VCRUNTIME140_1.dll" "%STAGE%\" >nul
    copy /y "!CRTDIR!\MSVCP140.dll"       "%STAGE%\" >nul
)

REM If windeployqt left the installer behind, drop it. Shipping an .exe that
REM asks for elevation inside a folder that is meant to be unzip-and-run is the
REM wrong signal to whoever receives it.
if exist "%STAGE%\vc_redist.x64.exe" del /q "%STAGE%\vc_redist.x64.exe"

REM --- verify ----------------------------------------------------------------
REM
REM The platform plugin is the one whose absence is fatal and whose error
REM message does not name it usefully: without platforms\qwindows.dll the
REM application exits with "could not find or load the Qt platform plugin".

if not exist "%STAGE%\platforms\qwindows.dll" (
    echo error: platforms\qwindows.dll missing; the package would not start 1>&2
    goto fail
)
if not exist "%STAGE%\Qt6Widgets.dll" (
    echo error: Qt6Widgets.dll missing; windeployqt did not do its job 1>&2
    goto fail
)

REM Svg is easy to lose and fails quietly. app\icons.cpp renders the device
REM icons through QSvgRenderer, so a missing Qt6Svg.dll is not a crash -- it is
REM a topology view where every node draws blank.
if not exist "%STAGE%\Qt6Svg.dll" (
    echo error: Qt6Svg.dll missing; every device icon would render blank 1>&2
    goto fail
)

REM The licences, as a gate. The block above either put these here or failed,
REM but this is the last point before the zip is written and a package that
REM ships without them is one that cannot lawfully be handed to anyone.
if not exist "%STAGE%\LICENSE" (
    echo error: LICENSE missing from the package; it may not be distributed 1>&2
    goto fail
)
for %%F in (THIRD_PARTY_NOTICES.md GPL-3.0.txt LGPL-3.0.txt) do (
    if not exist "%STAGE%\licenses\%%F" (
        echo error: licenses\%%F missing from the package 1>&2
        goto fail
    )
)

REM The MSVC runtime again, as a gate rather than as a copy. The block above
REM either put these here or failed; if they are still absent something skipped
REM it, and this is the last point before the zip where that is cheap to catch.
for %%F in (VCRUNTIME140.dll VCRUNTIME140_1.dll MSVCP140.dll) do (
    if not exist "%STAGE%\%%F" (
        echo error: %%F missing; the package dies at launch on a machine 1>&2
        echo        without the VS 2022 redistributable, with a missing-DLL
        echo        dialog that says nothing about Qt or about omegamaps.
        goto fail
    )
)

REM WebEngine. QtWebEngineProcess.exe is the piece to check by name: the viewer
REM starts, spawns it, and exits when it is not there. The .pak files and the
REM locales are looked for by pattern rather than by path -- their exact
REM directory has moved between Qt 6 releases, and hardcoding one layout would
REM turn a working bundle into a failing gate. A missing locale is a warning
REM here rather than an error for the same reason; confirm the layout against
REM the Qt version in use before making it fatal.
if "%HAVEVIEWER%"=="1" (
    if not exist "%STAGE%\QtWebEngineProcess.exe" (
        echo error: QtWebEngineProcess.exe missing; the viewer would exit 1>&2
        echo        the moment it is opened, and the application would report
        echo        that the map viewer is not installed.
        goto fail
    )
    call :findunder "%STAGE%" qtwebengine_resources.pak file PAKS
    if "!PAKS!"=="" (
        echo error: qtwebengine_resources.pak missing; WebEngine would not 1>&2
        echo        initialise and the map would open to a blank window.
        goto fail
    )
    call :findunder "%STAGE%" qtwebengine_locales dir LOCALES
    if "!LOCALES!"=="" (
        echo ==^> WARNING: no qtwebengine_locales directory in the package.
        echo     WebEngine wants a locale .pak; on some Qt builds it falls back
        echo     and on others it aborts. Check this bundle on a clean machine
        echo     before shipping it.
    )
)

REM LAST AND MOST IMPORTANT. Everything above proves files are present; this
REM proves they are the RIGHT files. A Qt6Core.dll from a different Qt than the
REM binary was linked against is present, correctly named, and wrong -- and the
REM package still runs on this machine, because the real Qt is on PATH here.
for /f "tokens=2 delims==" %%V in ('findstr /c:"Qt6_DIR:PATH=" "%BUILD_DIR%\CMakeCache.txt"') do set "USEDQT=%%V"
echo !USEDQT! | findstr /i /c:"%QTPREFIX:\=/%" >nul
if errorlevel 1 (
    echo error: the build and windeployqt used different Qt installations. 1>&2
    echo         linked against: !USEDQT!
    echo         deployed from:  %QTPREFIX%
    echo     The package would fail on a machine without Qt installed.
    echo     Delete %BUILD_DIR% and run this again.
    goto fail
)
echo ==^> platform plugin, Qt DLLs, Svg, MSVC runtime present, one Qt throughout

REM --- zip -------------------------------------------------------------------

if "%MAKEZIP%"=="1" (
    echo ==^> writing dist\omegamaps-windows-x64.zip
    if exist "dist\omegamaps-windows-x64.zip" del /q "dist\omegamaps-windows-x64.zip"
    powershell -NoProfile -Command "Compress-Archive -Path 'dist\omegamaps' -DestinationPath 'dist\omegamaps-windows-x64.zip'"
    if errorlevel 1 goto fail
)

echo.
if "%WANTVIEWER%"=="1" if "%HAVEVIEWER%"=="0" (
    echo NOTE: packaged WITHOUT the map viewer. Qt WebEngine was not usable in
    echo       %QTPREFIX%
    echo       The application works; opening a map will say the viewer is not
    echo       installed. Add it with the Qt installer ^(Additional Libraries^)
    echo       or: aqt ... --archives qtdeclarative -m qtwebengine qtwebchannel qtpositioning
    echo.
)
echo the package contains omegamaps.exe, the crawl, omvault and mapview
echo command-line tools, LICENSE and licenses\.
echo.
echo run it with:
echo       %STAGE%\omegamaps.exe
popd
exit /b 0


REM ===========================================================================
REM Subroutines. Everything below runs via CALL, outside any parenthesised
REM block, so a single caret escapes what it looks like it escapes.
REM ===========================================================================

REM :findqt -- sets QTPREFIX to the newest C:\Qt\6.x with an msvc*_64 kit,
REM and QTGUESSED=1 if it found one.
:findqt
set "QTGUESSED=0"
if not exist "C:\Qt" exit /b 0
set "QTBESTNUM=0"
set "QTBEST="
for /f "delims=" %%D in ('dir /b /ad "C:\Qt\6.*" 2^>nul') do call :considerqt "%%D"
if "%QTBEST%"=="" exit /b 0
set "QTPREFIX=%QTBEST%"
set "QTGUESSED=1"
exit /b 0

REM :considerqt <version-dir-name> -- keep it if it is newer than QTBEST and
REM actually holds an MSVC kit. A 6.x with only a mingw kit is not a candidate:
REM link.exe cannot resolve a mingw Qt's C++ symbols.
:considerqt
set "QTV=%~1"
set "QTKIT="
for /f "delims=" %%E in ('dir /b /ad "C:\Qt\%~1\msvc*_64" 2^>nul') do if "!QTKIT!"=="" set "QTKIT=%%E"
if "%QTKIT%"=="" exit /b 0
if not exist "C:\Qt\%~1\%QTKIT%\bin\windeployqt.exe" exit /b 0

set "QTMAJ=" & set "QTMIN=" & set "QTPAT="
REM Parenthesised, because "do set A & set B" puts only the first SET in the
REM loop body -- the rest run once after it, where %%b is not a loop variable
REM and expands to the literal text %b.
for /f "tokens=1-3 delims=." %%a in ("%QTV%") do (
    set "QTMAJ=%%a"
    set "QTMIN=%%b"
    set "QTPAT=%%c"
)
call :decimal "!QTMAJ!" QTMAJ
call :decimal "!QTMIN!" QTMIN
call :decimal "!QTPAT!" QTPAT
set /a QTNUM=%QTMAJ%*1000000 + %QTMIN%*1000 + %QTPAT%
if %QTNUM% GTR %QTBESTNUM% (
    set "QTBESTNUM=%QTNUM%"
    set "QTBEST=C:\Qt\%QTV%\%QTKIT%"
)
exit /b 0

REM :decimal <text> <outvar> -- a number set/a will not misread. Empty becomes
REM 0, and leading zeros are stripped because set/a reads 08 as octal and
REM fails on it. Qt has not shipped a zero-padded component, but this costs
REM nothing and the failure it prevents is "invalid number" mid-loop.
:decimal
set "DEC=%~1"
if "%DEC%"=="" set "DEC=0"
:decimalstrip
if "%DEC%"=="0" goto decimaldone
if not "%DEC:~0,1%"=="0" goto decimaldone
set "DEC=%DEC:~1%"
if "%DEC%"=="" set "DEC=0"
goto decimalstrip
:decimaldone
set "%~2=%DEC%"
exit /b 0

REM :findcrt -- newest Microsoft.VC*.CRT under %VCToolsRedistDir%x64.
:findcrt
set "CRTDIR="
REM !CRTDIR! and not %CRTDIR%: /o-n lists newest first and this wants the
REM FIRST hit, but %CRTDIR% is expanded once when the FOR is parsed, so every
REM iteration would test the same empty string and the last -- oldest --
REM directory would win.
for /f "delims=" %%D in ('dir /b /ad /o-n "%VCToolsRedistDir%x64\Microsoft.VC*.CRT" 2^>nul') do (
    if "!CRTDIR!"=="" set "CRTDIR=%VCToolsRedistDir%x64\%%D"
)
exit /b 0

REM :findunder <root> <name> <file^|dir> <outvar> -- recursive search, so the
REM WebEngine payload is located by name rather than by a path that has moved
REM between Qt 6 releases.
:findunder
set "%~4="
if /i "%~3"=="dir" (
    for /f "delims=" %%F in ('dir /b /s /ad "%~1\%~2" 2^>nul') do set "%~4=%%F"
) else (
    for /f "delims=" %%F in ('dir /b /s "%~1\%~2" 2^>nul') do set "%~4=%%F"
)
exit /b 0

:usage
echo Packages omegamaps as a self-contained folder using windeployqt.
echo.
echo   scripts\bundle-windows.bat [--qt ^<prefix^>] [--zip] [--no-viewer]
echo                              [--build-dir ^<dir^>]
echo.
echo Needs cl.exe ^(MSVC^), gcc.exe ^(mingw-w64 x86_64^) and go.exe on PATH.
echo Environment: OMEGAMAPS_QT, CMAKE_PREFIX_PATH, Qt6_DIR
popd
exit /b 0

:fail
echo.
echo bundle failed
popd
exit /b 1
