// viewer/main.cpp
//
// omegamaps-viewer: the map viewer as its own executable, which the
// application launches with the map to show.
//
//     omegamaps-viewer [--theme cyber|dark|light] [map.json]
//
// With no map it asks for one. The theme defaults to the application's last
// choice, so a viewer started from a terminal still matches it.

#include <QApplication>
#include <QCommandLineParser>
#include <QFileInfo>
#include <QMessageBox>
#include <QSettings>
#include <QStyleFactory>
#include <QTimer>

#include <omegamaps/omegamaps.h>

#include "theme.h"
#include "viewerwindow.h"

using namespace omegamaps;

namespace {

QString libraryVersion() {
    char *v = omegamaps_version();
    const QString s = QString::fromUtf8(v);
    omegamaps_free(v);
    return s;
}

}  // namespace

int main(int argc, char **argv) {
    // WebEngine shares GL contexts with the widget stack; set before the
    // application object, where Qt looks for it.
    QCoreApplication::setAttribute(Qt::AA_ShareOpenGLContexts);
    QApplication app(argc, argv);
    QApplication::setOrganizationName(QStringLiteral("omegamaps"));
    QApplication::setApplicationName(QStringLiteral("omegamaps-viewer"));
    QApplication::setApplicationDisplayName(QStringLiteral("omegamaps map viewer"));
    QApplication::setApplicationVersion(libraryVersion());
    QApplication::setStyle(QStyleFactory::create(QStringLiteral("Fusion")));

    // OMEGAMAPS_QT_DIALOGS=1: Qt's own file dialogs instead of the
    // platform's. For a platform dialog that fails to appear -- which returns
    // nothing, exactly like a cancel.
    if (qEnvironmentVariableIntValue("OMEGAMAPS_QT_DIALOGS") != 0)
        QCoreApplication::setAttribute(Qt::AA_DontUseNativeDialogs);

    QCommandLineParser cli;
    cli.setApplicationDescription(QStringLiteral("omegamaps map viewer"));
    cli.addHelpOption();
    cli.addVersionOption();
    cli.addPositionalArgument(QStringLiteral("map"), QStringLiteral("A map.json written by a crawl"));
    const QCommandLineOption themeOpt(QStringLiteral("theme"),
        QStringLiteral("cyber, dark or light (default: the application's last choice)"), QStringLiteral("name"));
    cli.addOption(themeOpt);
    cli.process(app);

    // The application's settings, read-only: the viewer follows its theme
    // rather than keeping a second choice of its own.
    const QString theme = cli.isSet(themeOpt)
        ? cli.value(themeOpt)
        : QSettings(QStringLiteral("omegamaps"), QStringLiteral("omegamaps"))
              .value(QStringLiteral("theme"), QStringLiteral("light")).toString();
    ThemeManager::instance().setTheme(themeFromKey(theme));

    ViewerWindow w;
    w.show();
    // Started by another program, the viewer is not the active application;
    // without this its window can open behind the one that started it.
    w.raise();
    w.activateWindow();

    const QStringList args = cli.positionalArguments();
    if (!args.isEmpty()) {
        QString err;
        if (!w.openMap(args.first(), &err)) {
            QMessageBox::critical(&w, QStringLiteral("omegamaps map viewer"),
                                  QStringLiteral("Could not open %1:\n%2").arg(QFileInfo(args.first()).fileName(), err));
        }
    } else {
        QTimer::singleShot(0, &w, &ViewerWindow::chooseMap);
    }
    return app.exec();
}
