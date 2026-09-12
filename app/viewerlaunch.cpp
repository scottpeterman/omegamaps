// app/viewerlaunch.cpp

#include "viewerlaunch.h"

#include <QCoreApplication>
#include <QDir>
#include <QFileInfo>
#include <QProcess>
#include <QSettings>

namespace omegamaps {

namespace {

QString tr(const char *s) { return QCoreApplication::translate("omegamaps", s); }

bool isProgram(const QString &path) {
    const QFileInfo fi(path);
    return fi.isFile() && fi.isExecutable();
}

}  // namespace

QStringList viewerCandidates(const QString &appDir) {
    QStringList out;
    const QString env = qEnvironmentVariable("OMEGAMAPS_VIEWER");
    if (!env.isEmpty()) out << env;
    const QString saved = QSettings().value(QLatin1String(kViewerPathKey)).toString();
    if (!saved.isEmpty()) out << saved;
    const QDir dir(appDir);
#if defined(Q_OS_WIN)
    out << dir.filePath(QStringLiteral("omegamaps-viewer.exe"));
#elif defined(Q_OS_MACOS)
    // From the application, appDir is omegamaps.app/Contents/MacOS and the
    // viewer is its own bundle beside omegamaps.app. From a plain executable
    // (a probe in build/tests, pointed at build/app), appDir is the directory
    // holding the bundles. A bare executable covers a non-bundle build.
    const QString inBundle = QStringLiteral("omegamaps-viewer.app/Contents/MacOS/omegamaps-viewer");
    out << QDir::cleanPath(dir.filePath(QStringLiteral("../../../") + inBundle))
        << dir.filePath(inBundle)
        << dir.filePath(QStringLiteral("omegamaps-viewer"));
#else
    out << dir.filePath(QStringLiteral("omegamaps-viewer"));
#endif
    return out;
}

QString findViewer(const QString &appDir) {
    for (const QString &c : viewerCandidates(appDir)) {
        if (isProgram(c)) return QFileInfo(c).absoluteFilePath();
    }
    return QString();
}

QString viewerExecutableFor(const QString &picked) {
    const QFileInfo fi(picked);
    // A macOS bundle is a directory, and the open dialog hands back the
    // bundle rather than the program inside it. Plain directory logic, so it
    // is testable anywhere: the executable named for the bundle, or failing
    // that the only one there.
    if (fi.isDir() && fi.suffix() == QLatin1String("app")) {
        const QDir macos(fi.absoluteFilePath() + QStringLiteral("/Contents/MacOS"));
        const QString named = macos.filePath(fi.completeBaseName());
        if (isProgram(named)) return QFileInfo(named).absoluteFilePath();
        const QFileInfoList exes = macos.entryInfoList(QDir::Files | QDir::Executable);
        return exes.size() == 1 ? exes.first().absoluteFilePath() : QString();
    }
    return isProgram(picked) ? fi.absoluteFilePath() : QString();
}

bool looksLikeViewer(const QString &exe, QString *why) {
    if (!isProgram(exe)) {
        if (why) *why = tr("%1 is not a program").arg(QDir::toNativeSeparators(exe));
        return false;
    }
#if defined(Q_OS_WIN)
    Q_UNUSED(why);
    return true;
#else
    // stdout only, and any line: Qt writes its own warnings to stderr
    // (XDG_RUNTIME_DIR, platform plugins) ahead of the answer, and a
    // merged read took the first of those for the program's reply.
    QProcess p;
    p.setProcessChannelMode(QProcess::SeparateChannels);
    p.start(exe, {QStringLiteral("--version")});
    if (!p.waitForFinished(10000)) {
        p.kill();
        p.waitForFinished(1000);
        if (why) *why = tr("%1 did not answer --version").arg(QDir::toNativeSeparators(exe));
        return false;
    }
    const QString out = QString::fromLocal8Bit(p.readAllStandardOutput()).trimmed();
    bool isViewer = false;
    for (const QString &line : out.split(QLatin1Char('\n')))
        if (line.trimmed().startsWith(QLatin1String("omegamaps-viewer "))) isViewer = true;
    if (!isViewer) {
        if (why) {
            *why = tr("%1 is not the map viewer: asked its version, it said \"%2\"")
                       .arg(QDir::toNativeSeparators(exe), out.left(120));
        }
        return false;
    }
    return true;
#endif
}

void saveViewerPath(const QString &exe) {
    QSettings().setValue(QLatin1String(kViewerPathKey), QFileInfo(exe).absoluteFilePath());
}

ViewerCommand viewerCommand(const QString &exe, const QStringList &viewerArgs, bool macos) {
    const QString marker = QStringLiteral(".app/Contents/MacOS/");
    const int at = exe.lastIndexOf(marker);
    if (macos && at > 0) {
        const QString bundle = exe.left(at + 4);  // through ".app"
        return {QStringLiteral("/usr/bin/open"),
                QStringList{QStringLiteral("-n"), QStringLiteral("-a"), bundle, QStringLiteral("--args")} + viewerArgs,
                true};
    }
    return {exe, viewerArgs, false};
}

bool launchViewer(const QString &mapPath, const QString &themeKey, QString *err, bool *notFound, QString *used) {
    if (notFound) *notFound = false;
    const QString appDir = QCoreApplication::applicationDirPath();
    const QString exe = findViewer(appDir);
    if (exe.isEmpty()) {
        if (notFound) *notFound = true;
        if (err) {
            QStringList looked;
            for (const QString &c : viewerCandidates(appDir)) looked << QDir::toNativeSeparators(c);
            *err = tr("The map viewer (omegamaps-viewer) was not found. Looked for:\n%1\n\n"
                      "Locate it once and it is remembered.")
                       .arg(looked.join(QLatin1Char('\n')));
        }
        return false;
    }
    const QStringList args{QStringLiteral("--theme"), themeKey, QFileInfo(mapPath).absoluteFilePath()};
#if defined(Q_OS_MACOS)
    const ViewerCommand cmd = viewerCommand(exe, args, true);
#else
    const ViewerCommand cmd = viewerCommand(exe, args, false);
#endif
    if (used) *used = exe;
    qInfo("omegamaps: map viewer: %s %s", qPrintable(cmd.program), qPrintable(cmd.args.join(QLatin1Char(' '))));
    if (cmd.waitForIt) {
        // open returns as soon as the app is launched (well under a second);
        // its exit status and stderr are the only report of a failed launch.
        QProcess p;
        p.start(cmd.program, cmd.args);
        const bool ran = p.waitForStarted(5000) && p.waitForFinished(15000);
        if (!ran || p.exitStatus() != QProcess::NormalExit || p.exitCode() != 0) {
            if (!ran) p.kill();
            const QString why = QString::fromLocal8Bit(p.readAllStandardError()).trimmed();
            if (err) {
                *err = tr("Could not start %1: %2")
                           .arg(QDir::toNativeSeparators(exe), why.isEmpty() ? p.errorString() : why);
            }
            return false;
        }
        return true;
    }
    // Detached: the viewer is the user's window now, not a child to be
    // reaped, and it keeps running if the application exits first.
    if (!QProcess::startDetached(cmd.program, cmd.args, QFileInfo(mapPath).absolutePath())) {
        if (err) *err = tr("Could not start %1").arg(QDir::toNativeSeparators(exe));
        return false;
    }
    return true;
}

}  // namespace omegamaps
