// tests/crawl_probe.cpp
//
// A real crawl, through the C surface and the application's own window,
// against the fake lab (tests/fakelab, internal/fakedev/lab.go). What
// replay_probe is for recordings, this is for live runs. It checks:
//
//   the vault    create, store SSH and SNMP credentials, list with no secret
//                in the output, meta-only edits that keep the secret, lock,
//                wrong and right passwords, the quiet unlock's answer, and
//                refusing an SNMP credential as the default
//   the request  validation names the bad fields
//   the crawl    5 reached, eng-leaf-1 failed on its credential, eng-host-9
//                left undialed by the exclude -- and no other rows, which is
//                what the host-key identity bug broke; the failed device kept
//                on the map as a leaf; the map, the recording
//                and the log written where the result says; the host keys in
//                the file the request named
//   the replay   the recording plays back to the same counts
//   the cancel   a stopped crawl finishes, says so, and still writes its map
//   the form     the same crawl started from the window's own form: the
//                credentials panel over the vault, bad fields marked and the
//                start refused, a locked vault refused, Test Single, Start,
//                the buttons following the run, and the fields remembered
//
// Run it with the lab's addresses on the host (fakelab -addrs prints the
// commands) and the right to bind port 22:
//
//   QT_QPA_PLATFORM=offscreen ./build/tests/crawl_probe /tmp/om-crawl
//
// writes /tmp/om-crawl-light.png. Exit status is the number of failed checks,
// or 77 when the lab cannot start -- a skip, not a failure.

#include <QApplication>
#include <QCoreApplication>
#include <QDir>
#include <QElapsedTimer>
#include <QFile>
#include <QFileInfo>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QPlainTextEdit>
#include <QProcess>
#include <QPushButton>
#include <QSettings>
#include <QSpinBox>
#include <QComboBox>
#include <QLabel>
#include <QLineEdit>
#include <QTableWidget>
#include <QStyleFactory>
#include <QTemporaryDir>
#include <QTimer>

#include <cstdio>
#include <functional>

#include <omegamaps/vault.h>

#include "crawlform.h"
#include "discoverylog.h"
#include "mainwindow.h"
#include "runbridge.h"
#include "theme.h"
#include "topologypreview.h"
#include "vault.h"
#include "viewerlaunch.h"

using namespace omegamaps;

namespace {

int failures = 0;

void check(bool ok, const QString &what) {
    std::printf("%s  %s\n", ok ? "ok  " : "FAIL", qPrintable(what));
    std::fflush(stdout);
    if (!ok) ++failures;
}

// Runs the event loop until done() or the timeout, and says which.
bool waitFor(const std::function<bool()> &done, int timeoutMs) {
    QElapsedTimer t;
    t.start();
    while (!done()) {
        if (t.elapsed() > timeoutMs) return false;
        QCoreApplication::processEvents(QEventLoop::AllEvents, 50);
    }
    return true;
}

QString rawList(const Vault &v) {
    char *out = nullptr;
    if (omegamaps_vault_list(v.handle(), &out) != OMEGAMAPS_VAULT_OK) return QString();
    const QString s = QString::fromUtf8(out);
    omegamaps_free(out);
    return s;
}

struct Crawled {
    bool finished = false;
    QHash<QString, RunRow> rows;
    RunProgress progress;
    RunResult result;
    int maxRunning = 0;
};

// Starts a run through the window and follows it to the end.
Crawled follow(MainWindow &w, const std::function<bool()> &start, int timeoutMs,
               const std::function<void(const RunProgress &)> &onProgress = {}) {
    Crawled c;
    QObject ctx;
    QObject::connect(w.bridge(), &RunBridge::rowsChanged, &ctx, [&](const QVector<RunRow> &batch) {
        for (const RunRow &r : batch) c.rows.insert(r.identity, r);
    });
    QObject::connect(w.bridge(), &RunBridge::progressChanged, &ctx, [&](const RunProgress &p) {
        c.maxRunning = std::max(c.maxRunning, p.counts.running);
        if (onProgress) onProgress(p);
    });
    QObject::connect(w.bridge(), &RunBridge::finished, &ctx, [&](const RunProgress &p) {
        c.progress = p;
        c.finished = true;
    });
    if (!start()) return c;
    waitFor([&] { return c.finished; }, timeoutMs);
    // The window loads the map in its own finished handler; let the preview's
    // deferred layout run too.
    waitFor([] { return false; }, 400);
    c.result = w.bridge()->result();
    return c;
}

QByteArray request(const QString &mapPath, const QString &knownHosts, const QString &seed) {
    QJsonObject o;
    o.insert(QStringLiteral("seeds"), QJsonArray{seed});
    o.insert(QStringLiteral("depth"), 3);
    o.insert(QStringLiteral("concurrency"), 5);
    o.insert(QStringLiteral("timeout_ms"), 10000);
    o.insert(QStringLiteral("methods"), QJsonArray{QStringLiteral("ssh")});
    o.insert(QStringLiteral("domains"), QJsonArray{QStringLiteral("lab.local")});
    o.insert(QStringLiteral("exclude"), QJsonArray{QStringLiteral("linux")});
    o.insert(QStringLiteral("host_keys"), QStringLiteral("tofu"));
    o.insert(QStringLiteral("known_hosts_path"), knownHosts);
    o.insert(QStringLiteral("map_path"), mapPath);
    return QJsonDocument(o).toJson(QJsonDocument::Compact);
}

}  // namespace

int main(int argc, char **argv) {
    QApplication app(argc, argv);
    QApplication::setStyle(QStyleFactory::create(QStringLiteral("Fusion")));
    const QString prefix = argc > 1 ? QString::fromLocal8Bit(argv[1]) : QStringLiteral("/tmp/om-crawl");
    const QString fakelabPath = argc > 2 ? QString::fromLocal8Bit(argv[2])
                                         : QCoreApplication::applicationDirPath() + QStringLiteral("/fakelab");

    // --- the lab -------------------------------------------------------------
    QProcess lab;
    lab.setProgram(fakelabPath);
    lab.setArguments({QStringLiteral("-latency"), QStringLiteral("150ms")});
    lab.start();
    if (!lab.waitForStarted(5000)) {
        std::printf("SKIP  could not run %s\n", qPrintable(fakelabPath));
        return 77;
    }
    QByteArray readyLine;
    waitFor([&] {
        if (lab.canReadLine()) readyLine = lab.readLine();
        return !readyLine.isEmpty() || lab.state() == QProcess::NotRunning;
    }, 15000);
    const QJsonObject ready = QJsonDocument::fromJson(readyLine).object();
    if (!ready.value(QLatin1String("ready")).toBool()) {
        lab.waitForFinished(2000);
        std::printf("SKIP  the fake lab did not start:\n%s", lab.readAllStandardError().constData());
        return 77;
    }
    const QString seed = ready.value(QLatin1String("seed")).toString();
    const QString labUser = ready.value(QLatin1String("user")).toString();
    const QString labPassword = ready.value(QLatin1String("password")).toString();
    std::printf("lab up: %d devices, seed %s\n",
                int(ready.value(QLatin1String("devices")).toArray().size()), qPrintable(seed));

    QTemporaryDir work;
    const QString dir = work.path();
    // The form remembers its fields in QSettings; keep the probe's out of the
    // user's own.
    QCoreApplication::setOrganizationName(QStringLiteral("omegamaps-probe"));
    QCoreApplication::setApplicationName(QStringLiteral("crawl_probe"));
    QSettings::setDefaultFormat(QSettings::IniFormat);
    QSettings::setPath(QSettings::IniFormat, QSettings::UserScope, dir + QStringLiteral("/settings"));

    // --- the vault -------------------------------------------------------------
    Vault vault(dir + QStringLiteral("/vault.json"));
    check(vault.isOpen() && !vault.exists(), QStringLiteral("vault handle open, no file yet"));
    check(vault.create(QStringLiteral("short")) == VaultError::WeakPassword,
          QStringLiteral("a short master password is refused"));
    check(vault.create(QStringLiteral("lab-master-pass")) == VaultError::Ok, QStringLiteral("vault created"));
    check(vault.create(QStringLiteral("lab-master-pass")) == VaultError::Exists,
          QStringLiteral("creating over an existing vault is refused"));

    CredentialInput ssh;
    ssh.name = QStringLiteral("lab");
    ssh.username = labUser;
    ssh.auth = AuthMethod::Password;
    ssh.password = labPassword;
    ssh.tags = {QStringLiteral("lab")};
    QString sshId;
    check(vault.store(ssh, &sshId) == VaultError::Ok && !sshId.isEmpty(), QStringLiteral("SSH credential stored"));

    CredentialInput snmp;
    snmp.name = QStringLiteral("lab-ro");
    snmp.auth = AuthMethod::SnmpV2c;
    snmp.password = QStringLiteral("lab-community-string");
    snmp.tags = {QStringLiteral("lab")};
    QString snmpId;
    check(vault.store(snmp, &snmpId) == VaultError::Ok, QStringLiteral("SNMP v2c credential stored"));

    QVector<CredentialMeta> metas;
    check(vault.list(&metas) == VaultError::Ok && metas.size() == 2, QStringLiteral("list has both"));
    const QString raw = rawList(vault);
    check(!raw.contains(labPassword) && !raw.contains(QLatin1String("lab-community-string")) &&
              raw.contains(QLatin1String("\"has_secret\":true")),
          QStringLiteral("list carries no secret, and says one is there"));
    CredentialMeta ro;
    check(vault.meta(QStringLiteral("lab-ro"), &ro) == VaultError::Ok && ro.isSnmp &&
              ro.authType == QLatin1String("snmp-v2c"),
          QStringLiteral("SNMP credential reads back as snmp-v2c"));
    check(vault.setDefault(snmpId) == VaultError::SnmpDefault,
          QStringLiteral("an SNMP credential is refused as the default"));

    CredentialMeta labMeta;
    vault.meta(QStringLiteral("lab"), &labMeta);
    labMeta.description = QStringLiteral("home lab IOSv login");
    check(vault.updateMetadata(labMeta) == VaultError::Ok, QStringLiteral("meta-only edit accepted"));
    vault.meta(QStringLiteral("lab"), &labMeta);
    check(labMeta.hasSecret && labMeta.description == QLatin1String("home lab IOSv login"),
          QStringLiteral("meta-only edit kept the password"));

    vault.lock();
    check(vault.list(&metas) == VaultError::Locked, QStringLiteral("locked vault refuses a list"));
    check(vault.unlock(QStringLiteral("not-the-password")) == VaultError::WrongPassword,
          QStringLiteral("wrong master password says so"));
    {
        // Nobody typed anything: the quiet path answers from the keyring or
        // the environment, and in a headless session neither has it.
        Vault second(dir + QStringLiteral("/vault.json"));
        const VaultError q = second.unlockQuiet();
        check(q == VaultError::NeedsPassword || isKeyringError(q),
              QStringLiteral("quiet unlock never reports a wrong password (%1)").arg(Vault::errorName(q)));
    }
    check(vault.unlock(QStringLiteral("lab-master-pass")) == VaultError::Ok, QStringLiteral("unlocked"));

    // --- the request -----------------------------------------------------------
    const QString knownHosts = dir + QStringLiteral("/known_hosts");
    const QString mapPath = dir + QStringLiteral("/maps/lab-map.json");
    check(RunBridge::validateCrawl(request(mapPath, knownHosts, seed)).isEmpty(),
          QStringLiteral("the lab request validates"));
    {
        QJsonObject bad = QJsonDocument::fromJson(request(QString(), knownHosts, seed)).object();
        bad.insert(QStringLiteral("seeds"), QJsonArray{});
        bad.insert(QStringLiteral("depth"), -1);
        QStringList fields;
        for (const auto &f : RunBridge::validateCrawl(QJsonDocument(bad).toJson())) fields << f.first;
        check(fields.contains(QLatin1String("seeds")) && fields.contains(QLatin1String("depth")) &&
                  fields.contains(QLatin1String("map_path")),
              QStringLiteral("validation names the bad fields: %1").arg(fields.join(QLatin1String(", "))));
    }

    // --- the crawl -------------------------------------------------------------
    ThemeManager::instance().setTheme(ThemeId::Light);
    MainWindow w;
    w.resize(1440, 900);
    w.show();

    QElapsedTimer clock;
    clock.start();
    Crawled c = follow(w, [&] { return w.openCrawl(request(mapPath, knownHosts, seed), vault.handle()); },
                       120000);
    std::printf("crawl finished in %lld ms\n", static_cast<long long>(clock.elapsed()));
    check(c.finished, QStringLiteral("crawl finished"));
    check(c.result.kind == QLatin1String("crawl") && c.result.state == QLatin1String("done"),
          QStringLiteral("result: %1 %2 %3").arg(c.result.kind, c.result.state, c.result.error));

    const RunCounts &n = c.progress.counts;
    check(n.reached == 5 && n.failed == 1 && n.notDialed == 1,
          QStringLiteral("5 reached, 1 failed, 1 not dialed: got %1, %2, %3")
              .arg(n.reached).arg(n.failed).arg(n.notDialed));
    check(c.rows.size() == 7,
          QStringLiteral("exactly 7 rows -- no device counted twice: %1").arg(c.rows.size()));
    for (const RunRow &r : std::as_const(c.rows)) {
        if (r.display == QLatin1String("eng-leaf-1"))
            check(r.state == RowState::Failed, QStringLiteral("eng-leaf-1 failed on its credential"));
        if (r.display == QLatin1String("eng-host-9"))
            check(r.state == RowState::NotDialed && r.detail.contains(QLatin1String("linux")),
                  QStringLiteral("eng-host-9 left undialed by the exclude"));
    }
    check(c.maxRunning > 0, QStringLiteral("devices were seen in flight"));
    check(w.preview()->deviceCount() == 6,
          QStringLiteral("the map shows the 6 dialed devices: %1").arg(w.preview()->deviceCount()));
    // Seven between drawn devices: the six among the reached ones, and the
    // failed eng-leaf-1 hanging off eng-spine-1.
    check(w.lastLinkCount() == 7, QStringLiteral("links drawn from map.json: %1, want 7").arg(w.lastLinkCount()));

    check(c.result.mapPath == mapPath && QFileInfo::exists(mapPath) && !QFileInfo::exists(mapPath + ".tmp"),
          QStringLiteral("map written where asked, no temporary left"));
    check(c.result.eventsPath.endsWith(QLatin1String("lab-map.events.jsonl")) &&
              QFileInfo(c.result.eventsPath).size() > 0,
          QStringLiteral("recording written beside the map: %1").arg(QFileInfo(c.result.eventsPath).fileName()));
    check(c.result.logPath.endsWith(QLatin1String("lab-map.log")) && QFileInfo(c.result.logPath).size() > 0,
          QStringLiteral("crawl log written beside the map"));
    {
        QFile kh(knownHosts);
        if (!kh.open(QIODevice::ReadOnly)) check(false, QStringLiteral("could not open %1").arg(kh.fileName()));
        const int keys = QString::fromUtf8(kh.readAll()).count(QLatin1Char('\n'));
        check(keys == 6, QStringLiteral("host keys went to the file the request named: %1").arg(keys));
    }
    check(w.runPanel()->status->text().startsWith(QLatin1String("Completed in")) &&
              w.runPanel()->status->text().contains(QLatin1String("5 reached, 1 failed")),
          QStringLiteral("status line: %1").arg(w.runPanel()->status->text()));
    check(w.log()->plainText().contains(QLatin1String("Recorded to lab-map.events.jsonl")),
          QStringLiteral("the log says where the recording is"));
    check(w.grab().save(prefix + QStringLiteral("-light.png")),
          QStringLiteral("grab saved: %1-light.png").arg(prefix));

    // --- the replay ------------------------------------------------------------
    Crawled r = follow(w, [&] { return w.openReplay(c.result.eventsPath, c.result.mapPath, 0); }, 30000);
    check(r.finished && r.progress.counts.reached == n.reached && r.progress.counts.failed == n.failed &&
              r.progress.counts.notDialed == n.notDialed && r.rows.size() == c.rows.size(),
          QStringLiteral("the recording replays to the same run"));

    // --- the cancel ------------------------------------------------------------
    const QString stoppedMap = dir + QStringLiteral("/maps/stopped-map.json");
    bool cancelled = false;
    Crawled s = follow(
        w, [&] { return w.openCrawl(request(stoppedMap, knownHosts, seed), vault.handle()); }, 60000,
        [&](const RunProgress &p) {
            if (!cancelled && p.counts.running > 0) {
                cancelled = true;
                w.bridge()->cancel();
            }
        });
    check(cancelled && s.finished, QStringLiteral("a crawl stopped in flight finishes"));
    check(s.result.state == QLatin1String("cancelled"),
          QStringLiteral("and says it was cancelled: %1").arg(s.result.state));
    check(s.progress.counts.running == 0 && s.progress.counts.queued == 0,
          QStringLiteral("with nothing left in flight"));
    check(QFileInfo::exists(stoppedMap), QStringLiteral("and still writes the map of what it reached"));
    check(w.runPanel()->status->text().startsWith(QLatin1String("Stopped after")),
          QStringLiteral("status line: %1").arg(w.runPanel()->status->text()));


    // --- the form --------------------------------------------------------------
    // Everything above started the crawl by calling openCrawl. This starts it
    // the way a user does: fields, the vault panel, the buttons.
    w.resize(1600, 980);
    w.setVaultPath(dir + QStringLiteral("/vault.json"));
    CredentialsPanel *credPanel = w.credentialsPanel();
    check(credPanel->status->text().startsWith(QLatin1String("Locked")),
          QStringLiteral("a new vault handle starts locked: %1").arg(credPanel->status->text()));
    check(w.vault()->unlock(QStringLiteral("lab-master-pass")) == VaultError::Ok, QStringLiteral("form's vault unlocked"));
    credPanel->refresh();
    check(credPanel->table->rowCount() == 2 && credPanel->unlockBtn->text() == QLatin1String("Lock"),
          QStringLiteral("credentials panel lists both: %1 rows").arg(credPanel->table->rowCount()));
    {
        QStringList types;
        for (int i = 0; i < credPanel->table->rowCount(); ++i) types << credPanel->table->item(i, 1)->text();
        check(types.contains(QLatin1String("SSH Password")) && types.contains(QLatin1String("SNMP V2C")),
              QStringLiteral("types shown: %1").arg(types.join(QLatin1String(", "))));
    }

    CrawlForm *form = w.crawlForm();
    ConnectionPanel *conn = w.connectionPanel();
    OptionsPanel *opts = w.optionsPanel();
    OutputPanel *out = w.outputPanel();
    RunPanel *runp = w.runPanel();
    int starts = 0;
    QObject::connect(form, &CrawlForm::startRequested, [&] { ++starts; });

    conn->seeds->setPlainText(QStringLiteral("not a host!!"));
    out->directory->setText(dir + QStringLiteral("/formmaps"));
    out->mapName->setText(QStringLiteral("lab-form"));
    runp->start->click();
    check(starts == 0 && form->fieldWidget(QStringLiteral("seeds"))->property("invalid").toString() == QLatin1String("true") &&
              runp->problems->isVisible(),
          QStringLiteral("a bad seed is marked and the start refused: %1").arg(runp->problems->text()));

    conn->seeds->setPlainText(seed);
    conn->domains->setText(QStringLiteral("lab.local"));
    conn->exclude->setText(QStringLiteral("linux"));
    opts->methods->setCurrentIndex(0);  // SSH only: the lab speaks no SNMP
    opts->knownHosts->setText(knownHosts);
    w.vault()->lock();
    runp->start->click();
    check(starts == 0 && credPanel->status->property("invalid").toString() == QLatin1String("true"),
          QStringLiteral("a locked vault refuses the start"));
    w.vault()->unlock(QStringLiteral("lab-master-pass"));
    credPanel->refresh();

    {
        const QJsonObject req = QJsonDocument::fromJson(form->request()).object();
        check(req.value(QLatin1String("depth")).toInt() == opts->depth->value() &&
                  req.value(QLatin1String("methods")).toArray() == QJsonArray{QStringLiteral("ssh")} &&
                  req.value(QLatin1String("exclude")).toArray() == QJsonArray{QStringLiteral("linux")} &&
                  req.value(QLatin1String("map_path")).toString() == dir + QStringLiteral("/formmaps/lab-form.json"),
              QStringLiteral("the request says what the fields say"));
    }

    Crawled single = follow(w, [&] { runp->testSingle->click(); return starts == 1; }, 60000);
    check(single.finished && single.progress.counts.reached == 1 && single.rows.value(seed).state == RowState::Reached &&
              QFileInfo::exists(dir + QStringLiteral("/formmaps/lab-form-test.json")),
          QStringLiteral("Test single collects the seed alone, into lab-form-test.json"));

    bool stopEnabledDuring = false;
    bool runningShown = false;
    bool viewMapDuring = false;
    Crawled f = follow(
        w, [&] { runp->start->click(); return starts == 2; }, 120000,
        [&](const RunProgress &p) {
            if (!p.finished && runp->stop->isEnabled() && !runp->start->isEnabled()) stopEnabledDuring = true;
            if (!p.finished && runp->status->text().startsWith(QLatin1String("Running"))) runningShown = true;
            if (!p.finished && runp->viewMap->isEnabled()) viewMapDuring = true;
        });
    check(f.finished && f.progress.counts.reached == 5 && f.progress.counts.failed == 1 &&
              f.progress.counts.notDialed == 1,
          QStringLiteral("Start runs the lab crawl: %1 reached, %2 failed, %3 not dialed")
              .arg(f.progress.counts.reached).arg(f.progress.counts.failed).arg(f.progress.counts.notDialed));
    check(QFileInfo::exists(dir + QStringLiteral("/formmaps/lab-form.json")) &&
              QFileInfo::exists(dir + QStringLiteral("/formmaps/lab-form.events.jsonl")),
          QStringLiteral("map and recording written where the form said"));
    check(stopEnabledDuring && runp->start->isEnabled() && !runp->stop->isEnabled(),
          QStringLiteral("Start and Stop follow the run"));
    check(runningShown, QStringLiteral("the status line says Running while it runs"));
    check(!viewMapDuring && runp->viewMap->isEnabled(),
          QStringLiteral("View map is off while the crawl runs and on once its map is in"));
    {
        // A stand-in viewer that records how it was started: the launch is
        // the application's half, and what it passes is the contract.
        const QString argsFile = dir + QStringLiteral("/viewer-args.txt");
        const QString stub = dir + QStringLiteral("/fake-viewer.sh");
        QFile sf(stub);
        if (!sf.open(QIODevice::WriteOnly)) check(false, QStringLiteral("could not open %1").arg(sf.fileName()));
        sf.write(QStringLiteral("#!/bin/sh\nprintf '%s\\n' \"$@\" > '%1'\n").arg(argsFile).toUtf8());
        sf.close();
        sf.setPermissions(sf.permissions() | QFileDevice::ExeOwner);
        qputenv("OMEGAMAPS_VIEWER", stub.toLocal8Bit());
        runp->viewMap->click();
        waitFor([&] { return QFileInfo(argsFile).size() > 0; }, 5000);
        QFile af(argsFile);
        if (!af.open(QIODevice::ReadOnly)) check(false, QStringLiteral("could not open %1").arg(af.fileName()));
        const QStringList args = QString::fromUtf8(af.readAll()).split(QLatin1Char('\n'), Qt::SkipEmptyParts);
        const QStringList want{QStringLiteral("--theme"), themeKey(ThemeManager::instance().id()),
                               QFileInfo(dir + QStringLiteral("/formmaps/lab-form.json")).absoluteFilePath()};
        check(args == want, QStringLiteral("View map starts the viewer on the run's map, in the theme: %1")
                                .arg(args.join(QLatin1Char(' '))));
        check(w.log()->plainText().contains(QStringLiteral("Opened lab-form.json in the map viewer")),
              QStringLiteral("and says so in the log, so a launch is never silent"));
        qunsetenv("OMEGAMAPS_VIEWER");

        // Locate: the real viewer, where the build put it, is checked and
        // saved. Only where it was built (it needs WebEngine).
        const QString real = QCoreApplication::applicationDirPath() + QStringLiteral("/../app/omegamaps-viewer");
        if (QFileInfo(real).isExecutable()) {
            // Checked first: a refusal inside locateViewer is a modal
            // dialog, which would hang the probe instead of failing it.
            QString why;
            const bool passes = looksLikeViewer(real, &why);
            check(passes, QStringLiteral("the built viewer passes the Locate check %1").arg(why));
            if (passes) {
                const bool took = w.locateViewer(real);
                check(took && QSettings().value(QLatin1String(kViewerPathKey)).toString() ==
                                  QFileInfo(real).absoluteFilePath(),
                      QStringLiteral("Locate takes the real viewer and remembers it"));
            }
            QSettings().remove(QLatin1String(kViewerPathKey));
        }
    }
    check(w.lastLinkCount() == 7, QStringLiteral("the form's crawl draws the same 7 links"));
    check(w.grab().save(prefix + QStringLiteral("-form.png")), QStringLiteral("grab saved: %1-form.png").arg(prefix));

    {
        // A second window starts from what the first one ran.
        MainWindow again;
        const QByteArray a = form->request(), b = again.crawlForm()->request();
        check(a == b, QStringLiteral("the fields are remembered for next time"));
    }

    // --- done ----------------------------------------------------------------
    lab.closeWriteChannel();
    if (!lab.waitForFinished(5000)) lab.kill();
    std::printf("%s\n", failures == 0 ? "all checks passed" : "CHECKS FAILED");
    return failures;
}
