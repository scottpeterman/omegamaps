@echo off
REM scripts\bundle-windows-msix.bat
REM
REM Wraps the folder that scripts\bundle-windows.bat produces (dist\omegamaps)
REM into an MSIX. Ported from omegasshqt's script of the same name.
REM
REM   scripts\bundle-windows-msix.bat
REM   scripts\bundle-windows-msix.bat --sign-self
REM   scripts\bundle-windows-msix.bat --cert mycert.pfx --password ****
REM   scripts\bundle-windows-msix.bat --version 0.1.3
REM
REM THE VERSION COMES FROM THE MANIFEST. --version stamps the staged copy's
REM <Identity Version> (and the .msix file name) without touching
REM packaging\msix\AppxManifest.xml.
REM
REM RUN bundle-windows.bat FIRST. This packages dist\omegamaps and builds
REM nothing.
REM
REM THE ICON has three parts and all three are needed:
REM   app\omegamaps.rc       compiled into omegamaps.exe (Explorer, shortcuts)
REM   art\msix\*.png         the tiles and logos the manifest names
REM   resources.pri          generated below; without it Windows ignores the
REM                          scale-200 and targetsize-*_altform-unplated
REM                          variants and shows a small plated icon
REM Regenerate the PNGs and the .ico with:  python art\make-icons.py
REM
REM SIGNING IS NOT OPTIONAL FOR MSIX. Unlike an MSI or an Inno installer, which
REM will run unsigned with a SmartScreen warning, Windows refuses to INSTALL an
REM unsigned MSIX at all. The three routes:
REM
REM   --sign-self     generates a self-signed certificate and signs with it.
REM                   Everyone installing it must first import the .cer into
REM                   Local Machine \ Trusted People. Fine for testing, not
REM                   something to ask strangers to do.
REM   --cert <pfx>    a real code signing certificate. Installs anywhere.
REM   the Store       Microsoft re-signs; you upload unsigned. Skip signing
REM                   here entirely and submit the .msix from --no-sign.
REM
REM THE MSVC RUNTIME IS CHECKED HERE TOO, not just in bundle-windows.bat.
REM Packaging a folder that is missing VCRUNTIME140.dll produces an MSIX that
REM installs cleanly, appears in the Start menu, and dies on launch -- and
REM under MSIX the missing-DLL dialog may not appear at all, so the only
REM symptom is a tile that does nothing. Refusing to pack is cheaper than
REM debugging that. It is a check and not a copy: the fix belongs in
REM bundle-windows.bat so the zip and the MSIX carry the same files.
REM
setlocal enabledelayedexpansion

set "ROOT=%~dp0.."
pushd "%ROOT%"

set "STAGE=dist\omegamaps"
set "MSIXDIR=dist\msix"
set "MANIFEST=packaging\msix\AppxManifest.xml"
set "ASSETS=art\msix"
set "SIGNMODE=none"
set "CERTFILE="
set "CERTPASS="
set "SUBJECT=CN=Scott Peterman"
set "VERSION="

:parse
if "%~1"=="" goto parsed
if /i "%~1"=="--sign-self" (set "SIGNMODE=self" & shift & goto parse)
if /i "%~1"=="--no-sign" (set "SIGNMODE=none" & shift & goto parse)
if /i "%~1"=="--cert" (
    if "%~2"=="" (echo error: --cert needs a .pfx path 1>&2 & goto fail)
    set "SIGNMODE=pfx" & set "CERTFILE=%~f2" & shift & shift & goto parse
)
if /i "%~1"=="--password" (
    set "CERTPASS=%~2" & shift & shift & goto parse
)
if /i "%~1"=="--subject" (
    if "%~2"=="" (echo error: --subject needs a value 1>&2 & goto fail)
    set "SUBJECT=%~2" & shift & shift & goto parse
)
if /i "%~1"=="--version" (
    if "%~2"=="" (echo error: --version needs a value, e.g. 0.1.2 1>&2 & goto fail)
    set "VERSION=%~2" & shift & shift & goto parse
)
if /i "%~1"=="--help" goto usage
if /i "%~1"=="-h" goto usage
echo error: unknown option: %~1 1>&2
goto fail
:parsed

REM --- what has to be there already ------------------------------------------

if not exist "%STAGE%\omegamaps.exe" (
    echo error: no %STAGE%\omegamaps.exe 1>&2
    echo     Run scripts\bundle-windows.bat first; this packages what that
    echo     produces rather than building its own copy.
    goto fail
)
if not exist "%MANIFEST%" (
    echo error: no %MANIFEST% 1>&2
    goto fail
)
for %%F in (StoreLogo.png Square44x44Logo.png Square44x44Logo.targetsize-48_altform-unplated.png) do (
    if not exist "%ASSETS%\%%F" (
        echo error: no %ASSETS%\%%F 1>&2
        echo     Generate the tile assets:  python art\make-icons.py
        goto fail
    )
)

REM --- the MSVC runtime ------------------------------------------------------
REM
REM windeployqt does not deploy these unless asked, and an older
REM bundle-windows.bat did not ask -- so a dist\omegamaps staged by it looks
REM complete and is not. Checked by name because that is the only way to tell:
REM the folder has ~40 DLLs in it either way.
REM
REM ucrtbase.dll is deliberately absent from this list. The UCRT ships with
REM Windows from 10 onward, which is below the manifest's 10.0.19041 floor.

set "CRTMISSING="
for %%F in (VCRUNTIME140.dll VCRUNTIME140_1.dll MSVCP140.dll) do (
    if not exist "%STAGE%\%%F" set "CRTMISSING=!CRTMISSING! %%F"
)
if not "!CRTMISSING!"=="" (
    echo error: the MSVC runtime is missing from %STAGE%: !CRTMISSING! 1>&2
    echo     The package would install and then fail to start on any machine
    echo     without the VS 2022 redistributable -- including, under MSIX,
    echo     with no error dialog at all.
    echo.
    echo     Fix it in the staging step rather than here, so the zip and the
    echo     MSIX ship the same files:
    echo         scripts\bundle-windows.bat --zip
    echo     which now passes --compiler-runtime to windeployqt and copies the
    echo     three DLLs from %%VCToolsRedistDir%% if that leaves them out.
    goto fail
)

REM windeployqt sometimes drops the redist INSTALLER beside the exe instead of
REM the DLLs. Inside an MSIX that is worse than useless: the package cannot run
REM it, and an .exe demanding elevation inside a packaged application is the
REM kind of thing Store submission asks about.
if exist "%STAGE%\vc_redist.x64.exe" (
    echo ==^> dropping %STAGE%\vc_redist.x64.exe from the layout
)

REM --- the SDK tools ---------------------------------------------------------
REM
REM makeappx and signtool live in the Windows SDK, which is NOT on PATH in a
REM plain Developer Command Prompt. Newest SDK first, same descending-sort
REM trick the Qt lookup uses.

set "SDKBIN="
for /f "delims=" %%D in ('dir /b /ad /o-n "C:\Program Files (x86)\Windows Kits\10\bin\10.*" 2^>nul') do (
    if "!SDKBIN!"=="" (
        if exist "C:\Program Files (x86)\Windows Kits\10\bin\%%D\x64\makeappx.exe" (
            set "SDKBIN=C:\Program Files (x86)\Windows Kits\10\bin\%%D\x64"
        )
    )
)
if "%SDKBIN%"=="" (
    where makeappx.exe >nul 2>nul
    if not errorlevel 1 for /f "delims=" %%P in ('where makeappx.exe') do set "SDKBIN=%%~dpP"
)
if "%SDKBIN%"=="" (
    echo error: makeappx.exe not found. 1>&2
    echo     It ships in the Windows SDK, under
    echo     C:\Program Files ^(x86^)\Windows Kits\10\bin\^<version^>\x64.
    echo     Install "Windows 10/11 SDK" from the Visual Studio Installer.
    goto fail
)
if not exist "%SDKBIN%\makepri.exe" (
    echo error: makepri.exe not found beside makeappx.exe in %SDKBIN% 1>&2
    goto fail
)
echo ==^> SDK tools at %SDKBIN%

REM --- assemble --------------------------------------------------------------
REM
REM Into a copy rather than in place: dist\omegamaps is the portable package and
REM stays shippable on its own. An MSIX layout is that folder plus a manifest
REM and an Assets directory, and leaving those behind in the zip would ship two
REM things that only one of them needs.

echo ==^> assembling %MSIXDIR%
if exist "%MSIXDIR%" rmdir /s /q "%MSIXDIR%"
mkdir "%MSIXDIR%"
xcopy /e /i /q /y "%STAGE%" "%MSIXDIR%" >nul
if errorlevel 1 goto fail
if exist "%MSIXDIR%\vc_redist.x64.exe" del /q "%MSIXDIR%\vc_redist.x64.exe"
mkdir "%MSIXDIR%\Assets"
xcopy /q /y "%ASSETS%\*.png" "%MSIXDIR%\Assets" >nul
if errorlevel 1 goto fail
copy /y "%MANIFEST%" "%MSIXDIR%\AppxManifest.xml" >nul
if errorlevel 1 goto fail

REM --- version ---------------------------------------------------------------
REM
REM MSIX wants four parts, and the Store requires the fourth to be 0. A three-
REM part --version gets .0 appended. Only the STAGED manifest is rewritten, and
REM only the Identity element's Version -- a text replace would also hit
REM MinVersion/MaxVersionTested on TargetDeviceFamily.

if not "%VERSION%"=="" (
    set "V4=%VERSION%"
    for /f "tokens=1-4 delims=." %%a in ("%VERSION%") do (
        if "%%d"=="" (set "V4=%%a.%%b.%%c.0")
        if "%%c"=="" (echo error: --version needs at least three parts, e.g. 0.1.2 1>&2 & goto fail)
    )
    echo ==^> stamping version !V4!
    powershell -NoProfile -Command "$p = (Resolve-Path '%MSIXDIR%\AppxManifest.xml').Path; $x = New-Object xml; $x.PreserveWhitespace = $true; $x.Load($p); $x.Package.Identity.Version = '!V4!'; $x.Save($p)"
    if errorlevel 1 goto fail
)

set "PKGVER="
for /f "delims=" %%V in ('powershell -NoProfile -Command "$x = New-Object xml; $x.Load((Resolve-Path '%MSIXDIR%\AppxManifest.xml').Path); $x.Package.Identity.Version"') do set "PKGVER=%%V"
if "%PKGVER%"=="" (
    echo error: could not read Identity Version from the staged manifest 1>&2
    goto fail
)
for /f "tokens=1-3 delims=." %%a in ("%PKGVER%") do set "SHORTVER=%%a.%%b.%%c"
echo ==^> package version %PKGVER%

REM --- resources.pri ---------------------------------------------------------
REM
REM Without a resources.pri, Windows sees only the base file names in Assets:
REM the scale-200 and targetsize-*_altform-unplated variants are ignored, so
REM the taskbar and Start show a small, plated (coloured square) icon. makepri
REM indexes the variants so the shell can pick the right one.
REM
REM The config goes beside the layout, not in it, so it is not packed.

set "PRICONF=dist\priconfig.xml"
echo ==^> generating resources.pri
"%SDKBIN%\makepri.exe" createconfig /cf "%PRICONF%" /dq en-US /pv 10.0.0 /o
if errorlevel 1 goto fail
"%SDKBIN%\makepri.exe" new /pr "%MSIXDIR%" /cf "%PRICONF%" /mn "%MSIXDIR%\AppxManifest.xml" /of "%MSIXDIR%\resources.pri" /o
if errorlevel 1 goto fail

REM --- pack ------------------------------------------------------------------

set "PKG=dist\OmegaMaps-%SHORTVER%-x64.msix"
echo ==^> packing %PKG%
if exist "%PKG%" del /q "%PKG%"
"%SDKBIN%\makeappx.exe" pack /d "%MSIXDIR%" /p "%PKG%" /o
if errorlevel 1 (
    echo.
    echo     makeappx names one missing or mismatched asset at a time. If it
    echo     complained about a file, check %ASSETS% has it and rerun
    echo     python art\make-icons.py if not.
    goto fail
)

REM --- sign ------------------------------------------------------------------

if "%SIGNMODE%"=="none" (
    echo ==^> not signed.
    echo     Windows will NOT install an unsigned MSIX. Either sign it
    echo     ^(--sign-self for testing, --cert for real^) or submit this file
    echo     to the Store, which signs it for you.
    goto done
)

REM REUSED IF IT IS ALREADY THERE. Generating a new certificate on every run
REM means a new key on every run, so the .cer already imported into
REM TrustedPeople no longer matches what signed the package -- and Windows
REM refuses the install with 0x800B0109, "terminated in a root certificate
REM which is not trusted", which reads like the import never worked rather
REM than like the certificate changed underneath it. Every run would also
REM leave another orphaned certificate in Cert:\CurrentUser\My.
REM
REM Delete dist\omegamaps-test.pfx to force a fresh one.
if "%SIGNMODE%"=="self" if exist "dist\omegamaps-test.pfx" (
    echo ==^> reusing dist\omegamaps-test.pfx
    set "CERTFILE=dist\omegamaps-test.pfx"
    set "CERTPASS=omega"
    set "SIGNMODE=pfx-self"
)

if "%SIGNMODE%"=="self" (
    echo ==^> generating a self-signed certificate for %SUBJECT%
    REM The subject MUST equal the manifest's Publisher exactly, or signtool
    REM refuses with a publisher-mismatch error. Both default to the same
    REM string here; change them together or not at all.
    powershell -NoProfile -Command ^
        "$c = New-SelfSignedCertificate -Type Custom -Subject '%SUBJECT%'" ^
        " -KeyUsage DigitalSignature -FriendlyName 'OmegaMaps test signing'" ^
        " -CertStoreLocation 'Cert:\CurrentUser\My'" ^
        " -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.3','2.5.29.19={text}');" ^
        " Export-PfxCertificate -Cert $c -FilePath 'dist\omegamaps-test.pfx'" ^
        " -Password (ConvertTo-SecureString -String 'omega' -Force -AsPlainText) | Out-Null;" ^
        " Export-Certificate -Cert $c -FilePath 'dist\omegamaps-test.cer' | Out-Null"
    if errorlevel 1 goto fail
    set "CERTFILE=dist\omegamaps-test.pfx"
    set "CERTPASS=omega"
)

echo ==^> signing
"%SDKBIN%\signtool.exe" sign /fd SHA256 /a /f "%CERTFILE%" /p "%CERTPASS%" "%PKG%"
if errorlevel 1 (
    echo.
    echo     If this said the publisher name does not match the certificate
    echo     subject: the Publisher in %MANIFEST% and the certificate subject
    echo     have to be identical, character for character.
    goto fail
)

if "%SIGNMODE%"=="self" goto selfnote
if "%SIGNMODE%"=="pfx-self" goto selfnote
goto done

:selfnote
echo.
echo Self-signed. In an ELEVATED POWERSHELL ^(not cmd^), once per machine:
echo       Import-Certificate -FilePath dist\omegamaps-test.cer -CertStoreLocation Cert:\LocalMachine\TrustedPeople

:done
echo.
echo   %PKG%
echo.
echo install it with:
echo       Add-AppxPackage -Path %PKG%
echo.
echo CHECK IT ON A MACHINE THAT DID NOT BUILD IT. This box has the VS runtime
echo and Qt on PATH, so a package missing either one still runs here.
echo.
popd
exit /b 0

:usage
echo Wraps dist\omegamaps into an MSIX. Run scripts\bundle-windows.bat first.
echo.
echo   scripts\bundle-windows-msix.bat [--sign-self ^| --cert ^<pfx^> [--password ^<pw^>] ^| --no-sign]
echo                                   [--subject "CN=Your Name"] [--version 0.1.2]
popd
exit /b 0

:fail
echo.
echo msix packaging failed
popd
exit /b 1