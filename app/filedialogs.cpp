// app/filedialogs.cpp

#include "filedialogs.h"

#include <QCoreApplication>
#include <QElapsedTimer>
#include <QFileDialog>

namespace omegamaps {

QString pickWithFallback(const std::function<QString(bool native)> &show, int neverShownMs) {
    if (QCoreApplication::testAttribute(Qt::AA_DontUseNativeDialogs)) return show(false);
    QElapsedTimer t;
    t.start();
    const QString got = show(true);
    if (!got.isEmpty() || t.elapsed() >= neverShownMs) return got;
    qInfo("omegamaps: the platform's file dialog returned without appearing (%lld ms); "
          "using Qt's file dialog from now on", static_cast<long long>(t.elapsed()));
    QCoreApplication::setAttribute(Qt::AA_DontUseNativeDialogs);
    return show(false);
}

namespace {

QFileDialog::Options opts(bool native) {
    return native ? QFileDialog::Options() : QFileDialog::DontUseNativeDialog;
}

}  // namespace

QString pickOpenFile(QWidget *parent, const QString &caption, const QString &dir, const QString &filter) {
    return pickWithFallback([&](bool native) {
        return QFileDialog::getOpenFileName(parent, caption, dir, filter, nullptr, opts(native));
    });
}

QString pickSaveFile(QWidget *parent, const QString &caption, const QString &path, const QString &filter) {
    return pickWithFallback([&](bool native) {
        return QFileDialog::getSaveFileName(parent, caption, path, filter, nullptr, opts(native));
    });
}

QString pickDirectory(QWidget *parent, const QString &caption, const QString &dir) {
    return pickWithFallback([&](bool native) {
        return QFileDialog::getExistingDirectory(parent, caption, dir, QFileDialog::ShowDirsOnly | opts(native));
    });
}

}  // namespace omegamaps
