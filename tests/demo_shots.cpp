// tests/demo_shots.cpp
//
// README screenshots from the company-free demo network (internal/demonet,
// cmd/demonet). Everything on screen is the real application: the window
// replays a recording of a real crawl -- the real crawler, with only per-
// device collection simulated -- and the map viewer opens the map that crawl
// wrote. Nothing here is drawn for the occasion.
//
//   QTWEBENGINE_DISABLE_SANDBOX=1 QT_QPA_PLATFORM=offscreen \
//       ./build/tests/demo_shots demo.events.jsonl demo.json shots/ [speed]
//
// scripts/demo-screenshots.sh runs demonet and then this. Writes, per theme
// (light, dark):
//
//   main_crawling_<t>.png    the window mid-crawl: depth bars, devices in
//                            flight, the preview filling in
//   main_complete_<t>.png    the finished run
//   not_dialed_<t>.png       the log filtered to what was deliberately skipped
//   map_viewer_<t>.png       the viewer, Cisco icons, the whole fabric
//   map_detail_<t>.png       closer in, spine1.dc1 selected with its card
//   map_detail_line_<t>.png  the same with the line icons
//   map_leaves_<t>.png       with undiscovered neighbours and leaves shown
//
// and once: map_full.png (the viewer's own PNG export, leaves shown) and
// demo.drawio (its draw.io export). Settings and the vault live in a
// throwaway directory; nothing of yours is read or written. Exit status is
// the number of failed steps.

#include <QApplication>
#include <QDir>
#include <QElapsedTimer>
#include <QFile>
#include <QJsonObject>
#include <QPushButton>
#include <QSettings>
#include <QStyleFactory>
#include <QTemporaryDir>
#include <QThread>
#include <QTimer>
#include <QWebEnginePage>
#include <QWebEngineView>

#include <cstdio>
#include <functional>
#include <memory>

#include "crawlform.h"
#include "icons.h"
#include "mainwindow.h"
#include "runbridge.h"
#include "theme.h"
#include "topologypreview.h"
#include "vault.h"
#include "viewerwindow.h"

using namespace omegamaps;

namespace {

int failures = 0;

void step(bool ok, const QString &what) {
    std::printf("%s  %s\n", ok ? "ok   " : "FAIL ", qPrintable(what));
    std::fflush(stdout);
    if (!ok) ++failures;
}

bool waitFor(const std::function<bool()> &done, int ms) {
    QElapsedTimer t;
    t.start();
    while (!done()) {
        if (t.elapsed() > ms) return false;
        QCoreApplication::processEvents(QEventLoop::AllEvents, 20);
        QThread::msleep(5);
    }
    return true;
}

void settle(int ms) { waitFor([] { return false; }, ms); }

QVariant js(QWebEngineView *v, const QString &code, int ms = 15000) {
    auto st = std::make_shared<std::pair<bool, QVariant>>(false, QVariant());
    v->page()->runJavaScript(code, [st](const QVariant &r) { *st = {true, r}; });
    waitFor([st] { return st->first; }, ms);
    return st->second;
}

void save(QWidget &w, const QString &path) {
    settle(1200);  // theme changes and layouts repaint asynchronously
    step(w.grab().save(path), QStringLiteral("saved %1").arg(QDir::toNativeSeparators(path)));
}

const char *kMaster = "demo-master-passphrase";

// The form as somebody would fill it in for this network. Through QSettings,
// the way the form remembers itself, so it loads exactly as it would.
void seedSettings() {
    QSettings s;
    s.clear();
    s.setValue(QStringLiteral("icons"), QStringLiteral("cisco"));
    s.beginGroup(QStringLiteral("crawl"));
    s.setValue(QStringLiteral("seeds"), QStringLiteral("10.1.0.1"));
    s.setValue(QStringLiteral("domains"), QStringLiteral("example.net"));
    s.setValue(QStringLiteral("exclude"), QStringLiteral("linux, phone, AIR-AP"));
    s.setValue(QStringLiteral("output_dir"), QStringLiteral("/data/omegamaps/demo"));
    s.setValue(QStringLiteral("map_name"), QStringLiteral("demo"));
    s.setValue(QStringLiteral("depth"), 7);
    s.setValue(QStringLiteral("concurrency"), 10);
    s.setValue(QStringLiteral("methods"), 1);  // SNMP only
}

bool makeVault(const QString &path) {
    Vault v(path);
    if (v.create(QString::fromLatin1(kMaster)) != VaultError::Ok) return false;
    CredentialInput snmp;
    snmp.name = QStringLiteral("demo-ro");
    snmp.auth = AuthMethod::SnmpV2c;
    snmp.password = QStringLiteral("not-a-real-community");
    snmp.tags = {QStringLiteral("demo")};
    CredentialInput ssh;
    ssh.name = QStringLiteral("netops");
    ssh.username = QStringLiteral("netops");
    ssh.auth = AuthMethod::Password;
    ssh.password = QStringLiteral("not-a-real-password");
    ssh.tags = {QStringLiteral("demo")};
    return v.store(snmp) == VaultError::Ok && v.store(ssh) == VaultError::Ok;
}

// One replay in one theme: the mid-crawl grab, the finished grab, and the
// log filtered to the devices that were deliberately not dialed.
void mainWindowShots(const QString &events, const QString &map, const QString &vault, double speed,
                     ThemeId theme, const QString &name, const QString &out) {
    ThemeManager::instance().setTheme(theme);
    MainWindow w;
    w.resize(1600, 1000);
    w.setVaultPath(vault);
    w.show();
    step(w.vault()->unlock(QString::fromLatin1(kMaster)) == VaultError::Ok, QStringLiteral("demo vault unlocked"));
    w.credentialsPanel()->refresh();

    bool grabbed = false, finished = false;
    QObject::connect(w.bridge(), &RunBridge::progressChanged, &w, [&](const RunProgress &p) {
        // Partway: a few depths in, with plenty in flight and the preview
        // well populated. The recording's own pacing decides when.
        if (!grabbed && !p.finished && p.depth >= 3 && p.running.size() >= 6) {
            grabbed = true;
            QCoreApplication::processEvents();
            save(w, QStringLiteral("%1/main_crawling_%2.png").arg(out, name));
        }
    });
    QObject::connect(w.bridge(), &RunBridge::finished, &w, [&] { finished = true; });

    step(w.openReplay(events, map, speed), QStringLiteral("replay opened (%1)").arg(name));
    step(waitFor([&] { return finished; }, 10 * 60 * 1000), QStringLiteral("replay finished"));
    step(grabbed, QStringLiteral("a mid-crawl moment was caught"));
    settle(800);  // the preview's deferred layout, and the map's links
    w.preview()->fit();
    save(w, QStringLiteral("%1/main_complete_%2.png").arg(out, name));

    QPushButton *notDialed = nullptr;
    for (QPushButton *b : w.findChildren<QPushButton *>())
        if (b->isCheckable() && b->text().startsWith(QLatin1String("Not dialed"))) notDialed = b;
    step(notDialed != nullptr, QStringLiteral("the log's Not dialed filter"));
    if (notDialed) {
        notDialed->setChecked(true);
        save(w, QStringLiteral("%1/not_dialed_%2.png").arg(out, name));
    }
    w.close();
}

}  // namespace

int main(int argc, char **argv) {
    QApplication app(argc, argv);
    QApplication::setStyle(QStyleFactory::create(QStringLiteral("Fusion")));
    if (argc < 4) {
        std::fprintf(stderr, "usage: %s <recording> <map.json> <outdir> [speed]\n", argv[0]);
        return 2;
    }
    const QString events = QFileInfo(QString::fromLocal8Bit(argv[1])).absoluteFilePath();
    const QString map = QFileInfo(QString::fromLocal8Bit(argv[2])).absoluteFilePath();
    const QString out = QFileInfo(QString::fromLocal8Bit(argv[3])).absoluteFilePath();
    const double speed = argc > 4 ? QString::fromLocal8Bit(argv[4]).toDouble() : 4.0;
    QDir().mkpath(out);

    // Throwaway settings and vault: nothing of the user's is touched.
    QTemporaryDir tmp;
    QApplication::setOrganizationName(QStringLiteral("omegamaps-demo"));
    QApplication::setApplicationName(QStringLiteral("demo_shots"));
    seedSettings();
    const QString vault = QDir(tmp.path()).filePath(QStringLiteral("vault.json"));
    step(makeVault(vault), QStringLiteral("demo vault with two credentials"));
    IconLibrary::instance().setIconSet(IconSet::Cisco);

    // --- the application window ------------------------------------------
    const struct { ThemeId id; const char *name; } themes[] = {{ThemeId::Light, "light"}, {ThemeId::Dark, "dark"}};
    // DEMO_SHOTS_VIEWER_ONLY=1 skips the replays, for iterating on the map.
    if (qEnvironmentVariableIsEmpty("DEMO_SHOTS_VIEWER_ONLY")) {
        for (const auto &t : themes)
            mainWindowShots(events, map, vault, speed, t.id, QLatin1String(t.name), out);
    }

    // --- the map viewer ---------------------------------------------------
    ThemeManager::instance().setTheme(ThemeId::Light);
    ViewerWindow v;
    v.setLayoutDir(QDir(tmp.path()).filePath(QStringLiteral("layouts")));
    v.setIconSet(QStringLiteral("cisco"));
    v.resize(1600, 1000);
    v.show();
    bool ready = false;
    QObject::connect(&v, &ViewerWindow::pageReady, [&](bool ok) { ready = ok; });
    QString err;
    step(v.openMap(map, &err), QStringLiteral("viewer opened the map %1").arg(err));
    step(waitFor([&] { return ready; }, 30000), QStringLiteral("viewer page ready"));
    settle(1500);  // layout animation

    // The layout the shots use. DEMO_SHOTS_LAYOUT overrides, by the value of
    // the page's layout list (dagre, dagre-lr, breadthfirst, cose, ...).
    const QString layout = qEnvironmentVariableIsEmpty("DEMO_SHOTS_LAYOUT")
                               ? QStringLiteral("dagre") : qEnvironmentVariable("DEMO_SHOTS_LAYOUT");
    js(v.view(), QStringLiteral("(function(){var s=document.getElementById('layoutSelect'); s.value='%1';"
                                " s.dispatchEvent(new Event('change')); return s.value;})()").arg(layout));
    settle(2500);

    auto setIcons = [&](const char *set) {
        v.setIconSet(QLatin1String(set));
        const bool ok = waitFor([&] {
            return js(v.view(), QStringLiteral("window.omegamapsStatus().icon_set")).toString() == QLatin1String(set);
        }, 5000);
        step(ok, QStringLiteral("viewer switched to %1 icons").arg(QLatin1String(set)));
        settle(800);  // the icons are images: let them load before the grab
    };
    auto clickId = [&](const char *id, int times) {
        for (int i = 0; i < times; ++i) {
            js(v.view(), QStringLiteral("document.getElementById('%1').click()").arg(QLatin1String(id)));
            settle(350);
        }
    };

    // The whole fabric, then a closer look with a device selected so its
    // detail card is part of the picture.
    for (const auto &t : themes) {
        ThemeManager::instance().setTheme(t.id);
        setIcons("cisco");
        clickId("btnFit", 1);
        save(v, QStringLiteral("%1/map_viewer_%2.png").arg(out, QLatin1String(t.name)));
    }
    ThemeManager::instance().setTheme(ThemeId::Light);
    auto focus = [&](const char *id, int hops, int padding) {
        const bool ok = js(v.view(), QStringLiteral("window.omegamapsFocus('%1', %2, %3) &&"
                                                    " !!document.querySelector('#nodeDetail.active')")
                                         .arg(QLatin1String(id)).arg(hops).arg(padding)).toBool();
        step(ok, QStringLiteral("%1 selected and framed, detail card shown").arg(QLatin1String(id)));
        settle(800);
    };
    focus("spine1.dc1", 1, 60);
    for (const auto &t : themes) {
        ThemeManager::instance().setTheme(t.id);
        setIcons("cisco");
        save(v, QStringLiteral("%1/map_detail_%2.png").arg(out, QLatin1String(t.name)));
        setIcons("line");
        save(v, QStringLiteral("%1/map_detail_line_%2.png").arg(out, QLatin1String(t.name)));
    }
    ThemeManager::instance().setTheme(ThemeId::Light);
    setIcons("cisco");

    // Everything a neighbour reported: leaves and undiscovered devices. Close
    // in on one leaf switch, where its servers, access switch and access
    // point all show -- at full width there are too many to read.
    js(v.view(), QStringLiteral(
        "(function(){['hideUndiscovered','hideLeaf'].forEach(function(id){"
        "var e=document.getElementById(id); if(e && e.checked) e.click();}); return true;})()"));
    settle(2500);
    // access3.dc1 and its neighbours, with room enough around them that
    // leaf3.dc1's servers beside it are in frame too.
    focus("access3.dc1", 1, 220);
    for (const auto &t : themes) {
        ThemeManager::instance().setTheme(t.id);
        save(v, QStringLiteral("%1/map_leaves_%2.png").arg(out, QLatin1String(t.name)));
    }
    ThemeManager::instance().setTheme(ThemeId::Light);
    settle(800);

    // The viewer's own exports, as the buttons produce them.
    auto exportTo = [&](const char *kind, const QString &path) {
        const QJsonObject r = QJsonObject::fromVariantMap(
            js(v.view(), QStringLiteral("window.omegamapsExport('%1','icons')").arg(QLatin1String(kind))).toMap());
        const QString data = r.value(QStringLiteral("data")).toString();
        QFile f(path);
        const bool ok = !data.isEmpty() && f.open(QIODevice::WriteOnly) &&
                        f.write(r.value(QStringLiteral("encoding")).toString() == QLatin1String("base64")
                                    ? QByteArray::fromBase64(data.toLatin1())
                                    : data.toUtf8()) > 0;
        step(ok, QStringLiteral("exported %1").arg(QDir::toNativeSeparators(path)));
    };
    exportTo("png", out + QStringLiteral("/map_full.png"));
    exportTo("drawio", out + QStringLiteral("/demo.drawio"));

    std::printf("%s\n", failures ? "SOME SHOTS FAILED" : "all shots taken");
    return failures;
}
