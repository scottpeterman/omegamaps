// tests/replay_probe.cpp
//
// Plays a recorded crawl through the application's own window and checks
// that what the views show agrees with the run model they were fed from:
//
//   - every dialed device is on the map, and nothing else is
//   - the progress panel's final counts are the run's final counts
//   - the log has a result line for every device that finished, and none
//     twice
//   - with a map.json, links were drawn and every map device was matched
//
// Then saves a grab of the window per theme. Runs offscreen:
//
//   QT_QPA_PLATFORM=offscreen ./build/tests/replay_probe \
//       run.jsonl map.json /tmp/omegamaps [speed]
//
// writes /tmp/omegamaps-light.png, -dark.png and -cyber.png, and at a speed
// above zero -running.png from partway through. Exit status is the number of
// failed checks.

#include <QApplication>
#include <QElapsedTimer>
#include <QLabel>
#include <QMouseEvent>
#include <QRegularExpression>
#include <QStyleFactory>
#include <QTimer>

#include <cstdio>

#include "aboutdialog.h"
#include "discoverylog.h"
#include "progresspanel.h"
#include "mainwindow.h"
#include "runbridge.h"
#include "theme.h"
#include "topologypreview.h"

using namespace omegamaps;

namespace {
int failures = 0;

void check(bool ok, const QString &what) {
    std::printf("%s  %s\n", ok ? "ok  " : "FAIL", qPrintable(what));
    if (!ok) ++failures;
}
}  // namespace

int main(int argc, char **argv) {
    QApplication app(argc, argv);
    QApplication::setStyle(QStyleFactory::create(QStringLiteral("Fusion")));
    if (argc < 4) {
        std::fprintf(stderr, "usage: %s <recording> <map.json|-> <grab-prefix> [speed]\n", argv[0]);
        return 2;
    }
    const QString events = QString::fromLocal8Bit(argv[1]);
    const QString mapPath = QString::fromLocal8Bit(argv[2]) == QLatin1String("-")
                                ? QString() : QString::fromLocal8Bit(argv[2]);
    const QString prefix = QString::fromLocal8Bit(argv[3]);
    const double speed = argc > 4 ? QString::fromLocal8Bit(argv[4]).toDouble() : 0.0;

    ThemeManager::instance().setTheme(ThemeId::Light);
    MainWindow w;
    // The smallest window the application allows: the height a laptop gives
    // it, and the one where the right column has least room.
    w.resize(w.minimumSize());
    w.show();

    // Track what the views were given, independently of the views.
    QHash<QString, RunRow> rows;
    int progressUpdates = 0;
    QObject::connect(w.bridge(), &RunBridge::rowsChanged, [&](const QVector<RunRow> &batch) {
        for (const RunRow &r : batch) rows.insert(r.identity, r);
    });
    // Mid-run: the first time the crawl is several depths in with devices in
    // flight, grab the window as it looks while a crawl is running. Only a
    // paced replay gets here; an instant one is finished by its first pull.
    int sawRunning = 0;
    bool grabbedRunning = false;
    QObject::connect(w.bridge(), &RunBridge::progressChanged, [&](const RunProgress &p) {
        ++progressUpdates;
        if (!p.finished && !p.running.isEmpty()) ++sawRunning;
        if (!grabbedRunning && !p.finished && p.depth >= 4 && p.running.size() >= 3) {
            grabbedRunning = true;
            app.processEvents();
            w.grab().save(prefix + QStringLiteral("-running.png"));
        }
    });

    // Connected before the open: nothing may assume a run cannot finish
    // before its opener has returned.
    QObject::connect(w.bridge(), &RunBridge::finished, &app, [&] {
        // Let the preview's deferred layout run before looking.
        QTimer::singleShot(400, &app, &QApplication::quit);
    });
    QElapsedTimer clock;
    clock.start();
    if (!w.openReplay(events, mapPath, speed)) {
        std::printf("FAIL  could not open %s\n", qPrintable(events));
        return 1;
    }
    QTimer::singleShot(10 * 60 * 1000, &app, [&] {
        std::printf("FAIL  replay did not finish\n");
        app.exit(1);
    });
    // Sample the progress panel through the run, after any pending layout
    // has been applied. It must never be squeezed below its natural height,
    // and it must never shrink mid-run: both were what the crushing and
    // re-expanding looked like.
    int samples = 0, crushed = 0, shrinks = 0, lastHeight = -1;
    QTimer sampler;
    sampler.setInterval(40);
    QObject::connect(&sampler, &QTimer::timeout, [&] {
        if (w.bridge()->isFinished()) return;
        QCoreApplication::sendPostedEvents(nullptr, QEvent::LayoutRequest);
        ProgressPanel *pp = w.progressPanel();
        const int h = pp->height(), hint = pp->sizeHint().height();
        ++samples;
        if (h < hint - 2) ++crushed;
        if (lastHeight >= 0 && h < lastHeight - 2) ++shrinks;
        lastHeight = h;
    });
    sampler.start();
    app.exec();
    sampler.stop();
    std::printf("replay finished in %lld ms, %d progress updates\n",
                static_cast<long long>(clock.elapsed()), progressUpdates);

    if (speed > 0) {
        check(sawRunning > 0, QStringLiteral("devices were seen in flight: %1 updates").arg(sawRunning));
        check(grabbedRunning, QStringLiteral("mid-run grab saved: %1-running.png").arg(prefix));
    }

    if (speed > 0) {
        check(samples > 10 && crushed == 0,
              QStringLiteral("progress panel never squeezed in the smallest window: %1 of %2 samples")
                  .arg(crushed).arg(samples));
        check(shrinks == 0, QStringLiteral("progress panel never shrank mid-run: %1 times").arg(shrinks));
    }
    {
        // When the column cannot fit, it scrolls; nothing in it is squeezed.
        QCoreApplication::sendPostedEvents(nullptr, QEvent::LayoutRequest);
        const int logH = w.log()->height(), logMin = w.log()->minimumSizeHint().height();
        const int ppH = w.progressPanel()->height(), ppHint = w.progressPanel()->sizeHint().height();
        check(logH >= logMin - 2 && ppH >= ppHint - 2,
              QStringLiteral("after the run nothing is squeezed: log %1/%2, progress %3/%4")
                  .arg(logH).arg(logMin).arg(ppH).arg(ppHint));
    }

    const RunProgress &p = w.bridge()->progress();
    const RunCounts &c = p.counts;
    check(p.finished, QStringLiteral("run finished"));
    check(c.queued == 0 && c.running == 0, QStringLiteral("nothing left in flight"));

    int dialed = 0, reached = 0, failed = 0, notDialed = 0;
    for (const RunRow &r : std::as_const(rows)) {
        if (r.dialed()) ++dialed;
        if (r.state == RowState::Reached) ++reached;
        if (r.state == RowState::Failed) ++failed;
        if (r.state == RowState::NotDialed) ++notDialed;
    }
    check(reached == c.reached && failed == c.failed && notDialed == c.notDialed,
          QStringLiteral("rows agree with counts: %1/%2 reached, %3/%4 failed, %5/%6 not dialed")
              .arg(reached).arg(c.reached).arg(failed).arg(c.failed).arg(notDialed).arg(c.notDialed));
    check(w.preview()->deviceCount() == dialed,
          QStringLiteral("map shows every dialed device and only those: %1 drawn, %2 dialed")
              .arg(w.preview()->deviceCount()).arg(dialed));

    // One result line per finished device, counted by identity: two devices
    // can report the same name, and on a name they would look like one
    // device logged twice.
    const QString text = w.log()->plainText();
    QHash<QString, int> results;
    for (const QString &id : w.log()->resultIdentities()) results[id]++;
    int twice = 0;
    for (int n : std::as_const(results)) twice += n > 1;
    check(results.size() == c.reached + c.failed,
          QStringLiteral("log has a result for every finished device: %1 devices, %2 finished")
              .arg(results.size()).arg(c.reached + c.failed));
    check(twice == 0, QStringLiteral("no device logged twice (%1 were)").arg(twice));

    check(w.log()->notDialedDevices() == c.notDialed,
          QStringLiteral("log has a not-dialed decision for every not-dialed device: %1 of %2")
              .arg(w.log()->notDialedDevices()).arg(c.notDialed));

    // Names shared by more than one device: reported, not failed. It is a
    // property of the network (or of the naming), and the map draws both.
    QHash<QString, QStringList> byName;
    for (const RunRow &r : std::as_const(rows))
        if (r.dialed()) byName[r.display] << r.identity;
    for (auto it = byName.constBegin(); it != byName.constEnd(); ++it)
        if (it->size() > 1)
            std::printf("note  %s is the name of %lld devices: %s\n", qPrintable(it.key()),
                        static_cast<long long>(it->size()), qPrintable(it->join(QStringLiteral(", "))));

    check(!text.contains(QRegularExpression(QStringLiteral("^\\[[0-9:]+\\] *$"),
                                            QRegularExpression::MultilineOption)),
          QStringLiteral("no empty log lines"));

    if (!mapPath.isEmpty()) {
        check(w.lastLinkCount() > 0,
              QStringLiteral("links drawn from map.json: %1").arg(w.lastLinkCount()));
        check(!text.contains(QStringLiteral("map devices not in this run")),
              QStringLiteral("every map device matched a row"));
    }

    // Nothing scrolled the log, so it has followed the run to its last line.
    check(w.log()->showsNewest(), QStringLiteral("log followed the run to its newest line"));

    const struct { ThemeId id; const char *name; } themes[] = {
        {ThemeId::Light, "light"}, {ThemeId::Dark, "dark"}, {ThemeId::Cyber, "cyber"}};
    for (const auto &t : themes) {
        ThemeManager::instance().setTheme(t.id);
        app.processEvents();
        const QString out = QStringLiteral("%1-%2.png").arg(prefix, QLatin1String(t.name));
        // A theme change re-renders the log; it must still show its end.
        check(w.log()->showsNewest(),
              QStringLiteral("log shows its newest line after switching to %1")
                  .arg(QLatin1String(t.name)));
        check(w.grab().save(out), QStringLiteral("grab saved: %1").arg(out));
    }

    // The wordmark is the About box: a click opens it, a second click
    // raises the same one rather than opening another.
    {
        ThemeManager::instance().setTheme(ThemeId::Light);
        auto *mark = w.findChild<QLabel *>(QStringLiteral("wordmark"));
        auto click = [&] {
            const QPointF at(mark->width() / 2.0, mark->height() / 2.0);
            QMouseEvent press(QEvent::MouseButtonPress, at, mark->mapToGlobal(at), Qt::LeftButton, Qt::LeftButton, Qt::NoModifier);
            QMouseEvent release(QEvent::MouseButtonRelease, at, mark->mapToGlobal(at), Qt::LeftButton, Qt::NoButton, Qt::NoModifier);
            QCoreApplication::sendEvent(mark, &press);
            QCoreApplication::sendEvent(mark, &release);
            app.processEvents();
        };
        auto abouts = [] {
            QList<AboutDialog *> out;
            for (QWidget *tw : QApplication::topLevelWidgets())
                if (auto *d = qobject_cast<AboutDialog *>(tw); d && d->isVisible()) out << d;
            return out;
        };
        check(mark != nullptr, QStringLiteral("the header has its wordmark"));
        if (mark) {
            click();
            const QList<AboutDialog *> open = abouts();
            check(open.size() == 1, QStringLiteral("a click on the wordmark opens the About box"));
            if (open.size() == 1) {
                AboutDialog *d = open.first();
                const QPixmap pm = d->splash()->pixmap();
                check(!pm.isNull() && d->splash()->width() == 784,
                      QStringLiteral("with the splash, %1 wide").arg(d->splash()->width()));
                check(d->details().contains(RunBridge::libraryVersion()) && d->details().contains(QString::fromLatin1(qVersion())),
                      QStringLiteral("and the version and Qt it runs on: %1").arg(d->details().replace(QLatin1Char('\n'), QLatin1String(" | "))));
                click();
                check(abouts().size() == 1, QStringLiteral("a second click raises the same one"));
                const QString out = prefix + QStringLiteral("-about.png");
                check(d->grab().save(out), QStringLiteral("grab saved: %1").arg(out));
                d->close();
                app.processEvents();
            }
        }
    }

    std::printf("%s\n", failures == 0 ? "all checks passed" : "CHECKS FAILED");
    return failures;
}
