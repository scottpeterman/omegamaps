// tests/viewer_probe.cpp
//
// Runs the map viewer window with no display and checks it the way a person
// would use it: the map drawn, the theme on the page, every export written and
// readable, a layout saved in one window restored in the next, and the page
// kept on its own server.
//
//     QTWEBENGINE_DISABLE_SANDBOX=1 QT_QPA_PLATFORM=offscreen \
//         ./build/tests/viewer_probe [map.json] [/tmp/omv]
//
// Without a map it uses the fake lab's (below). Grabs go to <prefix>-light,
// -dark and -cyber.png. Exit status is the number of failed checks. The
// sandbox variable is for running as root, which Chromium otherwise refuses.

#include <QAction>
#include <QApplication>
#include <QDir>
#include <QElapsedTimer>
#include <QFile>
#include <QFileInfo>
#include <QImage>
#include <QLabel>
#include <QJsonDocument>
#include <QJsonObject>
#include <QMouseEvent>
#include <QSet>
#include <QSettings>
#include <QTemporaryDir>
#include <QThread>
#include <QWebEnginePage>
#include <QWebEngineView>

#include <cstdio>
#include <functional>
#include <memory>

#include "aboutdialog.h"
#include "filedialogs.h"
#include "pathprompt.h"
#include "theme.h"
#include "viewerlaunch.h"
#include "viewerwindow.h"

using namespace omegamaps;

namespace {

// The fake lab's map (internal/fakedev/lab.go), as crawl wrote it.
const char *kLabMap = R"LABMAP(
{
 "eng-rtr-1": {
  "node_details": {
   "ip": "172.16.128.2",
   "platform": "cisco_ios"
  },
  "peers": {
   "eng-spine-1": {
    "ip": "172.16.2.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/1",
      "Gi0/0"
     ]
    ]
   },
   "eng-spine-2": {
    "ip": "172.16.2.6",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/2",
      "Gi0/0"
     ]
    ]
   },
   "usa-rtr-1": {
    "ip": "172.16.100.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/3",
      "Gi0/2"
     ]
    ]
   },
   "wan-core-1": {
    "ip": "172.16.1.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/0",
      "Gi0/2"
     ]
    ]
   }
  }
 },
 "eng-spine-1": {
  "node_details": {
   "ip": "172.16.2.2",
   "platform": "cisco_ios"
  },
  "peers": {
   "eng-leaf-1": {
    "ip": "172.16.3.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/2",
      "Gi0/0"
     ]
    ]
   },
   "eng-rtr-1": {
    "ip": "172.16.128.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/0",
      "Gi0/1"
     ]
    ]
   },
   "eng-spine-2": {
    "ip": "172.16.2.6",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/1",
      "Gi0/1"
     ]
    ]
   }
  }
 },
 "eng-spine-2": {
  "node_details": {
   "ip": "172.16.2.6",
   "platform": "cisco_ios"
  },
  "peers": {
   "eng-host-9": {
    "ip": "172.16.3.9",
    "platform": "linux",
    "connections": [
     [
      "Gi0/3",
      "eth0"
     ]
    ]
   },
   "eng-rtr-1": {
    "ip": "172.16.128.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/0",
      "Gi0/2"
     ]
    ]
   },
   "eng-spine-1": {
    "ip": "172.16.2.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/1",
      "Gi0/1"
     ]
    ]
   }
  }
 },
 "usa-rtr-1": {
  "node_details": {
   "ip": "172.16.100.2",
   "platform": "cisco_ios"
  },
  "peers": {
   "eng-rtr-1": {
    "ip": "172.16.128.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/2",
      "Gi0/3"
     ]
    ]
   },
   "wan-core-1": {
    "ip": "172.16.1.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/0",
      "Gi0/1"
     ]
    ]
   }
  }
 },
 "wan-core-1": {
  "node_details": {
   "ip": "172.16.1.2",
   "platform": "cisco_ios"
  },
  "peers": {
   "eng-rtr-1": {
    "ip": "172.16.128.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/2",
      "Gi0/0"
     ]
    ]
   },
   "usa-rtr-1": {
    "ip": "172.16.100.2",
    "platform": "cisco_ios",
    "connections": [
     [
      "Gi0/1",
      "Gi0/0"
     ]
    ]
   }
  }
 }
}
)LABMAP";

int failures = 0;

void check(bool ok, const QString &what) {
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

void settle(int ms) {
    waitFor([] { return false; }, ms);
}

QVariant js(QWebEngineView *v, const QString &code, int ms = 8000) {
    auto st = std::make_shared<std::pair<bool, QVariant>>(false, QVariant());
    v->page()->runJavaScript(code, [st](const QVariant &r) { *st = {true, r}; });
    waitFor([st] { return st->first; }, ms);
    return st->second;
}

bool waitReady(ViewerWindow &w, int ms = 20000) {
    bool got = false, ok = false;
    auto c = QObject::connect(&w, &ViewerWindow::pageReady, [&](bool r) { got = true; ok = r; });
    waitFor([&] { return got; }, ms);
    QObject::disconnect(c);
    return got && ok;
}

struct ExportResult {
    bool done = false, ok = false;
    QString path, error;
};

ExportResult waitExport(ViewerWindow &w, const std::function<void()> &start, int ms = 15000) {
    ExportResult r;
    auto c = QObject::connect(&w, &ViewerWindow::exportFinished, [&](const QString &p, bool ok, const QString &e) {
        r = {true, ok, p, e};
    });
    start();
    waitFor([&] { return r.done; }, ms);
    QObject::disconnect(c);
    return r;
}

// A real click, at the element's centre, delivered to the widget WebEngine
// renders into. A click is a user gesture, which is what separates it from
// element.click() called by a script.
bool clickElement(QWebEngineView *v, const QString &id) {
    const QVariantList r = js(v, QStringLiteral(
        "(function(){var e=document.getElementById('%1'); if(!e) return null;"
        "var b=e.getBoundingClientRect(); return [b.left+b.width/2, b.top+b.height/2];})()").arg(id)).toList();
    QWidget *target = v->focusProxy();
    if (r.size() != 2 || !target) return false;
    const QPointF pos(r[0].toDouble(), r[1].toDouble());
    const QPointF global = target->mapToGlobal(pos);
    QMouseEvent press(QEvent::MouseButtonPress, pos, global, Qt::LeftButton, Qt::LeftButton, Qt::NoModifier);
    QMouseEvent release(QEvent::MouseButtonRelease, pos, global, Qt::LeftButton, Qt::NoButton, Qt::NoModifier);
    QCoreApplication::sendEvent(target, &press);
    QCoreApplication::sendEvent(target, &release);
    return true;
}

QJsonObject status(ViewerWindow &w) {
    return QJsonObject::fromVariantMap(js(w.view(), QStringLiteral("window.omegamapsStatus()")).toMap());
}

QString cssVar(QWebEngineView *v, const QString &name) {
    return js(v, QStringLiteral("getComputedStyle(document.documentElement).getPropertyValue('%1').trim()")
                     .arg(name)).toString();
}

void grab(QWidget &w, const QString &path) {
    settle(600);  // the theme change repaints asynchronously
    check(w.grab().save(path), QStringLiteral("grab saved: %1").arg(path));
}

QByteArray readAll(const QString &path) {
    QFile f(path);
    return f.open(QIODevice::ReadOnly) ? f.readAll() : QByteArray();
}

}  // namespace

int main(int argc, char **argv) {
    QCoreApplication::setAttribute(Qt::AA_ShareOpenGLContexts);
    QApplication app(argc, argv);
    // Its own settings, so a probe run never moves the real viewer's window
    // or export directory.
    QApplication::setOrganizationName(QStringLiteral("omegamaps-probe"));
    QApplication::setApplicationName(QStringLiteral("viewer_probe"));
    QSettings().clear();

    QTemporaryDir tmp;
    const QDir dir(tmp.path());
    const QString prefix = argc > 2 ? QString::fromLocal8Bit(argv[2]) : QStringLiteral("/tmp/omv");
    QString mapPath = argc > 1 ? QString::fromLocal8Bit(argv[1]) : QString();
    if (mapPath.isEmpty()) {
        mapPath = dir.filePath(QStringLiteral("lab-map.json"));
        QFile f(mapPath);
        if (!f.open(QIODevice::WriteOnly)) check(false, QStringLiteral("could not open %1").arg(f.fileName()));
        f.write(kLabMap);
    }
    const QString layouts = dir.filePath(QStringLiteral("layouts"));
    const QString exports = dir.filePath(QStringLiteral("exports"));
    QDir().mkpath(exports);
    const QJsonObject source = QJsonDocument::fromJson(readAll(mapPath)).object();

    ThemeManager::instance().setTheme(ThemeId::Light);

    auto win = std::make_unique<ViewerWindow>();
    ViewerWindow &w = *win;
    w.setLayoutDir(layouts);
    w.resize(1280, 820);
    w.show();

    // Every export answers its dialog with a path in exports/, as named.
    w.setSaveChooser([&](const QString &suggested) {
        return QDir(exports).filePath(QFileInfo(suggested).fileName());
    });

    // --- the map ------------------------------------------------------------
    QString err;
    check(w.openMap(mapPath, &err), QStringLiteral("map opened %1").arg(err));
    check(waitReady(w), QStringLiteral("page reports ready"));
    QJsonObject st = status(w);
    const int nodes = st.value(QStringLiteral("nodes")).toInt();
    check(nodes > 0 && nodes == w.server().nodeCount(),
          QStringLiteral("page draws every node the server loaded: %1 of %2").arg(nodes).arg(w.server().nodeCount()));
    check(st.value(QStringLiteral("edges")).toInt() > 0,
          QStringLiteral("and links: %1").arg(st.value(QStringLiteral("edges")).toInt()));
    check(st.value(QStringLiteral("layout_store")).toBool(), QStringLiteral("layouts go to the application's store"));
    check(st.value(QStringLiteral("banner")).toString().isEmpty(),
          QStringLiteral("no error banner: %1").arg(st.value(QStringLiteral("banner")).toString()));
    const QString bar = w.statusText();
    const QJsonObject inv = st.value(QStringLiteral("stats")).toObject();
    check(bar.startsWith(QStringLiteral("%1 devices").arg(inv.value(QStringLiteral("devices")).toInt())) &&
              inv.value(QStringLiteral("devices")).toInt() == source.size(),
          QStringLiteral("status bar counts the map, not the filtered view: %1").arg(bar));
    check(w.windowTitle().contains(QFileInfo(mapPath).fileName()),
          QStringLiteral("window title names the map: %1").arg(w.windowTitle()));
    check(js(w.view(), QStringLiteral("location.search")).toString().isEmpty(),
          QStringLiteral("token and theme are out of the address after load"));

    // --- theme ----------------------------------------------------------------
    const Tokens &light = tokensFor(ThemeId::Light);
    check(js(w.view(), QStringLiteral("document.documentElement.dataset.theme")).toString() == QLatin1String("light"),
          QStringLiteral("page opens in the light palette"));
    check(cssVar(w.view(), QStringLiteral("--bg")) == light.bgPrimary.name(),
          QStringLiteral("page background is the theme's: %1").arg(cssVar(w.view(), QStringLiteral("--bg"))));
    grab(w, prefix + QStringLiteral("-light.png"));

    ThemeManager::instance().setTheme(ThemeId::Dark);
    const Tokens &dark = tokensFor(ThemeId::Dark);
    settle(300);
    check(js(w.view(), QStringLiteral("document.documentElement.dataset.theme")).toString() == QLatin1String("dark"),
          QStringLiteral("a theme change reaches the page without a reload"));
    check(cssVar(w.view(), QStringLiteral("--bg")) == dark.bgPrimary.name(),
          QStringLiteral("dark background: %1").arg(cssVar(w.view(), QStringLiteral("--bg"))));
    check(js(w.view(), QStringLiteral("window.omegamapsTheme.cy.edge")).toString() == dark.accentDim.name(),
          QStringLiteral("graph links in the theme's accent"));
    grab(w, prefix + QStringLiteral("-dark.png"));
    ThemeManager::instance().setTheme(ThemeId::Cyber);
    grab(w, prefix + QStringLiteral("-cyber.png"));
    ThemeManager::instance().setTheme(ThemeId::Light);
    settle(300);

    // --- exports from the menu --------------------------------------------------
    const QString base = QFileInfo(mapPath).completeBaseName();
    ExportResult r = waitExport(w, [&] { w.exportPng(); });
    QImage png(r.path);
    check(r.ok && r.path.endsWith(base + QStringLiteral(".png")) && !png.isNull() && png.width() > 100,
          QStringLiteral("PNG export: %1 %2x%3 %4").arg(QFileInfo(r.path).fileName()).arg(png.width()).arg(png.height()).arg(r.error));

    r = waitExport(w, [&] { w.exportJson(); });
    const QJsonObject exported = QJsonDocument::fromJson(readAll(r.path)).object();
    const QStringList gotKeys = exported.keys(), wantKeys = source.keys();
    check(r.ok && !exported.isEmpty() && QSet<QString>(gotKeys.begin(), gotKeys.end()) ==
                                            QSet<QString>(wantKeys.begin(), wantKeys.end()),
          QStringLiteral("JSON export is the map: %1 devices %2").arg(exported.size()).arg(r.error));

    r = waitExport(w, [&] { w.exportDrawio(); });
    const QByteArray xml = readAll(r.path);
    const int visible = status(w).value(QStringLiteral("visible")).toInt();
    check(r.ok && xml.startsWith("<?xml") && xml.contains("<mxfile") && xml.count("vertex=\"1\"") == visible,
          QStringLiteral("draw.io export: %1 vertices for %2 visible nodes %3")
              .arg(xml.count("vertex=\"1\"")).arg(visible).arg(r.error));

    // --- the page's own Export button: a download, behind a dialog -------------
    // The chooser waits the way a modal dialog does, with the page's blob URL
    // already revoked, and the file exists: the download must replace it at
    // the path chosen rather than write "name (1).json" beside it.
    const QString over = QDir(exports).filePath(QStringLiteral("over.json"));
    {
        QFile f(over);
        if (!f.open(QIODevice::WriteOnly)) check(false, QStringLiteral("could not open %1").arg(f.fileName()));
        f.write("old");
    }
    w.setSaveChooser([&](const QString &) {
        settle(300);
        return over;
    });
    r = waitExport(w, [&] { clickElement(w.view(), QStringLiteral("btnJSON")); });
    const QByteArray got = readAll(over);
    check(r.done && r.ok, QStringLiteral("a click on the page's JSON button downloads: %1").arg(r.done ? r.error : QStringLiteral("nothing happened")));
    check(got != "old" && QJsonDocument::fromJson(got).isObject(),
          QStringLiteral("and replaces the file chosen: %1 bytes").arg(got.size()));
    const QStringList strays = QDir(exports).entryList({QStringLiteral("over*")}, QDir::Files);
    check(strays == QStringList{QStringLiteral("over.json")}, QStringLiteral("no renamed copy beside it: %1").arg(strays.join(QLatin1String(", "))));

    w.setSaveChooser([](const QString &) { return QString(); });
    r = waitExport(w, [&] { w.exportPng(); });
    check(r.done && !r.ok && r.error == QLatin1String("cancelled"), QStringLiteral("a cancelled dialog writes nothing"));
    w.setSaveChooser([&](const QString &s) { return QDir(exports).filePath(QFileInfo(s).fileName()); });

    // --- the page stays on its server -------------------------------------------
    js(w.view(), QStringLiteral("location.href = 'http://example.com/'; true"));
    settle(700);
    check(w.view()->url().host() == QLatin1String("127.0.0.1") &&
              status(w).value(QStringLiteral("state")).toString() == QLatin1String("ready"),
          QStringLiteral("navigation away is refused: %1").arg(w.view()->url().host()));

    // --- a file that is not a map ---------------------------------------------
    const QString junk = dir.filePath(QStringLiteral("notes.json"));
    {
        QFile f(junk);
        if (!f.open(QIODevice::WriteOnly)) check(false, QStringLiteral("could not open %1").arg(f.fileName()));
        f.write("[1, 2, 3]");
    }
    const QString before = w.mapPath();
    err.clear();
    const bool junkOpened = w.openMap(junk, &err);
    check(!junkOpened && !err.isEmpty(), QStringLiteral("a non-map is refused: %1").arg(err));
    check(w.mapPath() == before, QStringLiteral("and the map shown stays loaded"));

    // --- Reload re-reads the file -----------------------------------------------
    {
        QJsonObject grown = source;
        QJsonObject node{{QStringLiteral("node_details"),
                          QJsonObject{{QStringLiteral("ip"), QStringLiteral("172.16.9.1")},
                                      {QStringLiteral("platform"), QStringLiteral("cisco_ios")}}},
                         {QStringLiteral("peers"), QJsonObject()}};
        grown.insert(QStringLiteral("lab-new-1"), node);
        QFile f(mapPath);
        if (!f.open(QIODevice::WriteOnly | QIODevice::Truncate)) check(false, QStringLiteral("could not open %1").arg(f.fileName()));
        f.write(QJsonDocument(grown).toJson());
    }
    w.reload();
    check(waitReady(w), QStringLiteral("reload ready"));
    check(status(w).value(QStringLiteral("nodes")).toInt() == nodes + 1,
          QStringLiteral("reload shows the file as it is now: %1 nodes").arg(status(w).value(QStringLiteral("nodes")).toInt()));

    // --- a saved layout comes back in the next window -----------------------------
    js(w.view(), QStringLiteral("var s=document.getElementById('layoutSelect'); s.value='circle';"
                                "s.dispatchEvent(new Event('change')); true"));
    settle(300);
    js(w.view(), QStringLiteral("document.getElementById('btnSaveLayout').click(); true"));
    waitFor([&] { return status(w).value(QStringLiteral("layout_status")).toString() != QLatin1String("saving\u2026"); }, 5000);
    check(status(w).value(QStringLiteral("layout_status")).toString() == QLatin1String("saved"),
          QStringLiteral("Save: %1").arg(status(w).value(QStringLiteral("layout_status")).toString()));
    const QString layoutFile = w.server().layoutPath();
    check(QFileInfo(layoutFile).isFile() && layoutFile.startsWith(layouts),
          QStringLiteral("layout written to the store: %1").arg(QFileInfo(layoutFile).fileName()));
    check(QFileInfo(layoutFile).permissions() == (QFile::ReadOwner | QFile::WriteOwner | QFile::ReadUser | QFile::WriteUser),
          QStringLiteral("and readable by its owner only"));
    const int oldPort = w.server().port();

    win.reset();  // the server stops with the window
    settle(200);
    auto win2 = std::make_unique<ViewerWindow>();
    win2->setLayoutDir(layouts);
    win2->resize(1280, 820);
    win2->show();
    err.clear();
    const bool reopened = win2->openMap(mapPath, &err);
    check(reopened, QStringLiteral("second window opened the map %1").arg(err));
    check(waitReady(*win2), QStringLiteral("second window ready"));
    check(win2->server().port() != oldPort, QStringLiteral("on a new port: %1, was %2").arg(win2->server().port()).arg(oldPort));
    check(status(*win2).value(QStringLiteral("layout_status")).toString() == QLatin1String("restored"),
          QStringLiteral("the saved layout is restored: %1").arg(status(*win2).value(QStringLiteral("layout_status")).toString()));
    check(js(win2->view(), QStringLiteral("document.getElementById('layoutSelect').value")).toString() == QLatin1String("circle"),
          QStringLiteral("with the arrangement it was saved in"));
    js(win2->view(), QStringLiteral("document.getElementById('btnClearLayout').click(); true"));
    waitFor([&] { return !QFileInfo::exists(layoutFile); }, 5000);
    check(!QFileInfo::exists(layoutFile), QStringLiteral("Clear removes it"));

    // --- the application finds the viewer ------------------------------------------
    const QString appDir = QCoreApplication::applicationDirPath() + QStringLiteral("/../app");
    const QString found = findViewer(appDir);
    check(!found.isEmpty(), QStringLiteral("the application's launcher finds omegamaps-viewer: %1").arg(found));

    // A viewer the user located: checked, saved, and ahead of the built-in
    // places; $OMEGAMAPS_VIEWER ahead of it; a saved path that has gone
    // falls through to the built-in places again.
    auto writeScript = [&](const QString &path, const QByteArray &body) {
        QDir().mkpath(QFileInfo(path).absolutePath());
        QFile f(path);
        if (!f.open(QIODevice::WriteOnly)) check(false, QStringLiteral("could not open %1").arg(f.fileName()));
        f.write("#!/bin/sh\n" + body + "\n");
        f.close();
        f.setPermissions(f.permissions() | QFileDevice::ExeOwner);
    };
    QString why;
    const bool realIsViewer = looksLikeViewer(found, &why);
    check(realIsViewer, QStringLiteral("the real viewer answers --version as itself %1").arg(why));
    const QString impostor = dir.filePath(QStringLiteral("elsewhere/omegamaps"));
    writeScript(impostor, "echo 'omegamaps 1.0'");
    why.clear();
    const bool impostorTaken = looksLikeViewer(impostor, &why);
    check(!impostorTaken && why.contains(QLatin1String("omegamaps 1.0")),
          QStringLiteral("another program is refused, saying what it answered: %1").arg(why));

    const QString bundle = dir.filePath(QStringLiteral("elsewhere/omegamaps-viewer.app"));
    writeScript(bundle + QStringLiteral("/Contents/MacOS/omegamaps-viewer"), "exit 0");
    check(viewerExecutableFor(bundle) == QFileInfo(bundle + QStringLiteral("/Contents/MacOS/omegamaps-viewer")).absoluteFilePath(),
          QStringLiteral("an .app bundle picked in the dialog becomes the program inside it"));
    check(viewerExecutableFor(dir.filePath(QStringLiteral("notes.json"))).isEmpty(),
          QStringLiteral("a file that is not a program is not taken"));

    const QString located = dir.filePath(QStringLiteral("elsewhere/omegamaps-viewer"));
    writeScript(located, "exit 0");
    saveViewerPath(located);
    check(findViewer(appDir) == QFileInfo(located).absoluteFilePath(),
          QStringLiteral("a saved viewer comes ahead of the one beside the application"));
    qputenv("OMEGAMAPS_VIEWER", found.toLocal8Bit());
    check(findViewer(appDir) == found, QStringLiteral("and $OMEGAMAPS_VIEWER ahead of the saved one"));
    qunsetenv("OMEGAMAPS_VIEWER");
    QFile::remove(located);
    check(findViewer(appDir) == found, QStringLiteral("a saved viewer that has gone falls back to the built-in places"));

    // How it is started. The macOS rule runs here as a parameter: a program
    // in a bundle goes through open -n -a, so LaunchServices brings it to the
    // front; anything else, and every other platform, is a detached exec.
    const QStringList vargs{QStringLiteral("--theme"), QStringLiteral("dark"), QStringLiteral("/maps/m.json")};
    const QString inBundle = QStringLiteral("/b/app/omegamaps-viewer.app/Contents/MacOS/omegamaps-viewer");
    const ViewerCommand mac = viewerCommand(inBundle, vargs, true);
    check(mac.program == QLatin1String("/usr/bin/open") && mac.waitForIt &&
              mac.args == QStringList({QStringLiteral("-n"), QStringLiteral("-a"),
                                       QStringLiteral("/b/app/omegamaps-viewer.app"), QStringLiteral("--args")}) + vargs,
          QStringLiteral("macOS: a bundled viewer starts through open: %1 %2").arg(mac.program, mac.args.join(QLatin1Char(' '))));
    const ViewerCommand macBare = viewerCommand(QStringLiteral("/b/omegamaps-viewer"), vargs, true);
    check(macBare.program == QLatin1String("/b/omegamaps-viewer") && !macBare.waitForIt && macBare.args == vargs,
          QStringLiteral("macOS: a bare executable is started directly"));
    const ViewerCommand other = viewerCommand(inBundle, vargs, false);
    check(other.program == inBundle && !other.waitForIt, QStringLiteral("elsewhere: always started directly"));

    // --- Help > About ------------------------------------------------------------
    {
        QAction *aboutAct = nullptr;
        for (QAction *a : win2->findChildren<QAction *>())
            if (a->menuRole() == QAction::AboutRole) aboutAct = a;
        check(aboutAct != nullptr, QStringLiteral("the viewer has Help > About"));
        if (aboutAct) {
            aboutAct->trigger();
            QCoreApplication::processEvents();
            AboutDialog *d = nullptr;
            for (QWidget *tw : QApplication::topLevelWidgets())
                if (auto *a = qobject_cast<AboutDialog *>(tw); a && a->isVisible()) d = a;
            check(d && !d->splash()->pixmap().isNull(), QStringLiteral("and it opens the About box, splash and all"));
            if (d) d->close();
        }
    }

    // --- a file dialog that never appears ----------------------------------------
    // show(true) stands for the platform dialog, show(false) for Qt's.
    {
        QStringList calls;
        const QString got = pickWithFallback([&](bool native) {
            calls << (native ? QStringLiteral("native") : QStringLiteral("qt"));
            return native ? QString() : QStringLiteral("/maps/m.json");
        });
        check(got == QLatin1String("/maps/m.json") && calls == QStringList({QStringLiteral("native"), QStringLiteral("qt")}),
              QStringLiteral("a platform dialog that returns at once is asked again as Qt's: %1").arg(calls.join(QLatin1Char(','))));
        check(QCoreApplication::testAttribute(Qt::AA_DontUseNativeDialogs), QStringLiteral("and Qt's is used from then on"));
        calls.clear();
        pickWithFallback([&](bool native) { calls << (native ? QStringLiteral("native") : QStringLiteral("qt")); return QString(); });
        check(calls == QStringList{QStringLiteral("qt")}, QStringLiteral("without trying the platform's again: %1").arg(calls.join(QLatin1Char(','))));
        QCoreApplication::setAttribute(Qt::AA_DontUseNativeDialogs, false);

        calls.clear();
        const QString cancelled = pickWithFallback([&](bool native) {
            calls << (native ? QStringLiteral("native") : QStringLiteral("qt"));
            QThread::msleep(400);  // a person, cancelling
            return QString();
        });
        check(cancelled.isEmpty() && calls == QStringList{QStringLiteral("native")} &&
                  !QCoreApplication::testAttribute(Qt::AA_DontUseNativeDialogs),
              QStringLiteral("a cancel that took a person's time is a cancel"));
        calls.clear();
        const QString picked = pickWithFallback([&](bool native) {
            calls << (native ? QStringLiteral("native") : QStringLiteral("qt"));
            return QStringLiteral("/maps/fast.json");
        });
        check(picked == QLatin1String("/maps/fast.json") && calls.size() == 1, QStringLiteral("a quick answer with a file is an answer"));
    }

    // --- the path prompt ---------------------------------------------------------
    {
        const QString home = QDir::homePath();
        check(PathPrompt::resolve(QStringLiteral("  \"~/maps/x.json\"  ")) == home + QStringLiteral("/maps/x.json"),
              QStringLiteral("a pasted path loses its quotes and gets ~ expanded"));
        check(PathPrompt::resolve(QStringLiteral("file:///tmp/a%20b.json")) == QLatin1String("/tmp/a b.json"),
              QStringLiteral("a file:// URL becomes a path"));
        check(PathPrompt::resolve(QStringLiteral("rel/m.json")) == QDir::cleanPath(QDir::current().absoluteFilePath(QStringLiteral("rel/m.json"))),
              QStringLiteral("a relative path is made absolute"));
        check(PathPrompt::resolve(QStringLiteral("   ")).isEmpty(), QStringLiteral("blank is nothing"));

        PathPrompt p(nullptr, QStringLiteral("Open map"), QStringLiteral("map"), QString());
        p.setPath(mapPath);
        check(p.acceptable() && p.hint().contains(QLatin1String("modified")),
              QStringLiteral("an existing map is accepted, and described: %1").arg(p.hint()));
        p.setPath(dir.filePath(QStringLiteral("nope.json")));
        check(!p.acceptable() && p.hint() == QLatin1String("No such file"), QStringLiteral("a missing file is refused: %1").arg(p.hint()));
        p.setPath(dir.path());
        check(!p.acceptable() && p.hint() == QLatin1String("Not a file"), QStringLiteral("a directory is refused: %1").arg(p.hint()));
        p.setPath(mapPath);
        p.show();
        grab(p, prefix + QStringLiteral("-prompt.png"));
        p.hide();
        p.setCheck([](const QString &path) { return viewerExecutableFor(path).isEmpty() ? QStringLiteral("no") : QString(); });
        p.setPath(bundle);
        check(p.acceptable(), QStringLiteral("with the Locate check, an .app bundle is accepted"));
    }

    std::printf("%s\n", failures ? "CHECKS FAILED" : "all checks passed");
    win2.reset();
    return failures;
}
