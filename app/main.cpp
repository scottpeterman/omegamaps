// app/main.cpp

#include <QApplication>
#include <QCommandLineParser>
#include <QSettings>
#include <QStyleFactory>

#include "mainwindow.h"
#include "runbridge.h"
#include "theme.h"

using namespace omegamaps;

int main(int argc, char **argv) {
    QApplication app(argc, argv);
    QApplication::setOrganizationName(QStringLiteral("omegamaps"));
    QApplication::setApplicationName(QStringLiteral("omegamaps"));
    QApplication::setApplicationVersion(RunBridge::libraryVersion());
    // Fusion draws every control from the stylesheet the same way on every
    // platform; the native styles each ignore a different part of it.
    QApplication::setStyle(QStyleFactory::create(QStringLiteral("Fusion")));

    // OMEGAMAPS_QT_DIALOGS=1: Qt's own file dialogs instead of the
    // platform's. For a platform dialog that fails to appear -- which returns
    // nothing, exactly like a cancel.
    if (qEnvironmentVariableIntValue("OMEGAMAPS_QT_DIALOGS") != 0)
        QCoreApplication::setAttribute(Qt::AA_DontUseNativeDialogs);

    QCommandLineParser cli;
    cli.setApplicationDescription(QStringLiteral("omegamaps: network topology discovery"));
    cli.addHelpOption();
    cli.addVersionOption();
    cli.addPositionalArgument(QStringLiteral("recording"),
                              QStringLiteral("A crawl recorded with crawl -events, to replay"));
    const QCommandLineOption mapOpt(QStringLiteral("map"),
        QStringLiteral("map.json for the replay (default: map.json beside the recording)"),
        QStringLiteral("path"));
    const QCommandLineOption speedOpt(QStringLiteral("speed"),
        QStringLiteral("Replay speed: 1 is real time, 0 is instant (default 10)"),
        QStringLiteral("factor"), QStringLiteral("10"));
    const QCommandLineOption themeOpt(QStringLiteral("theme"),
        QStringLiteral("cyber, dark or light (default: the last one used)"),
        QStringLiteral("name"));
    cli.addOptions({mapOpt, speedOpt, themeOpt});
    cli.process(app);

    const QString theme = cli.isSet(themeOpt)
                              ? cli.value(themeOpt)
                              : QSettings().value(QStringLiteral("theme"), QStringLiteral("light")).toString();
    ThemeManager::instance().setTheme(themeFromKey(theme));

    MainWindow w;
    w.show();
    // Keyring or OMEGAMAPS_VAULT_PASSWORD, never a prompt: a vault that needs
    // its password stays locked until the credentials panel asks for it.
    w.tryQuietUnlock();
    if (!cli.positionalArguments().isEmpty())
        w.openReplay(cli.positionalArguments().first(), cli.value(mapOpt),
                     cli.value(speedOpt).toDouble());
    return app.exec();
}
