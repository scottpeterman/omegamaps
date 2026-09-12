// app/filedialogs.h
//
// File dialogs that survive a platform dialog that does not work.
//
// On some macOS and Qt combinations the native open panel never appears: the
// call returns "" at once, which is indistinguishable from a cancel, so a
// button that opens it simply does nothing. Every file dialog in both
// programs goes through here instead of QFileDialog's statics. The platform's
// dialog is tried first; one that comes back empty faster than a person can
// cancel is taken as never shown, and from then on this process uses Qt's own
// dialog, starting with asking again at once. OMEGAMAPS_QT_DIALOGS=1 (read in
// each main) starts out that way.

#ifndef OMEGAMAPS_APP_FILEDIALOGS_H
#define OMEGAMAPS_APP_FILEDIALOGS_H

#include <QString>

#include <functional>

class QWidget;

namespace omegamaps {

QString pickOpenFile(QWidget *parent, const QString &caption, const QString &dir, const QString &filter);
QString pickSaveFile(QWidget *parent, const QString &caption, const QString &path, const QString &filter);
QString pickDirectory(QWidget *parent, const QString &caption, const QString &dir);

// The rule itself, for the probe: show(true) is the platform dialog,
// show(false) Qt's. Under neverShownMs, an empty answer is "never shown".
QString pickWithFallback(const std::function<QString(bool native)> &show, int neverShownMs = 250);

}  // namespace omegamaps

#endif
