// app/viewerlaunch.h
//
// Starting the map viewer (omegamaps-viewer, viewer/) on a map. The viewer is
// its own process: the application does not link WebEngine, and a viewer
// left open outlives the run -- or the application -- that opened it.
//
// Where the viewer is, is a setting. The application looks beside itself,
// which covers a build tree and an install; a viewer anywhere else -- built
// in another directory, run from an IDE's build -- is located once by the
// user (MainWindow offers it when none is found) and remembered.

#ifndef OMEGAMAPS_APP_VIEWERLAUNCH_H
#define OMEGAMAPS_APP_VIEWERLAUNCH_H

#include <QString>
#include <QStringList>

namespace omegamaps {

// The application's setting for a viewer the user located.
inline const char *kViewerPathKey = "viewer/path";

// Where the viewer executable is looked for, in order:
//   $OMEGAMAPS_VIEWER, when set
//   the saved setting (viewer/path)
//   <appDir>/omegamaps-viewer[.exe]            Linux, Windows, the build tree
//   <appDir>/../../../omegamaps-viewer.app/...  macOS: a sibling bundle
//   <appDir>/omegamaps-viewer.app/...           macOS: from outside a bundle
// appDir is the directory holding the application's own executable.
QStringList viewerCandidates(const QString &appDir);

// The first candidate that exists and is executable, or "".
QString findViewer(const QString &appDir);

// What the user picked, as the program to run: an .app bundle becomes the
// executable inside it, an executable stays as it is. "" when it is neither.
QString viewerExecutableFor(const QString &picked);

// Whether exe is the map viewer: it has to answer --version as
// omegamaps-viewer. Not asked on Windows, where a windowed program answers
// --version with a message box; there the check is only that it runs.
bool looksLikeViewer(const QString &exe, QString *why);

// Remembers exe as the viewer, for every later launch.
void saveViewerPath(const QString &exe);

// How a viewer executable is started. On macOS a program inside an .app goes
// through LaunchServices -- open -n -a <bundle> --args ... -- which brings it
// to the front and fails out loud. A detached exec does neither: the viewer
// comes up behind the application's window, or not at all, and nothing says
// which. -n because each map is its own viewer; without it LaunchServices
// hands the arguments to one already running, which ignores them. macos is a
// parameter so the rule can be tested anywhere.
struct ViewerCommand {
    QString program;
    QStringList args;
    bool waitForIt = false;  // open returns once the app is up; exec does not
};
ViewerCommand viewerCommand(const QString &exe, const QStringList &viewerArgs, bool macos);

// Starts the viewer on mapPath in the given theme. On success *used is the
// program started. False with *err when the viewer is missing or would not
// start; *notFound says which, since only a missing viewer is worth offering
// to locate.
bool launchViewer(const QString &mapPath, const QString &themeKey, QString *err, bool *notFound = nullptr,
                  QString *used = nullptr);

}  // namespace omegamaps

#endif
