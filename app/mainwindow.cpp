// app/mainwindow.cpp

#include "mainwindow.h"

#include <QComboBox>
#include <QCoreApplication>
#include <QDir>
#include <QFileInfo>
#include <QHBoxLayout>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QLabel>
#include <QMessageBox>
#include <QPushButton>
#include <QScrollArea>
#include <QSettings>
#include <QSplitter>
#include <QVBoxLayout>

#include "aboutdialog.h"
#include "crawlform.h"
#include "discoverylog.h"
#include "pathprompt.h"
#include "panel.h"
#include "progresspanel.h"
#include "runbridge.h"
#include "theme.h"
#include "topologypreview.h"
#include "vault.h"
#include "viewerlaunch.h"

namespace omegamaps {

MainWindow::MainWindow(QWidget *parent) : QMainWindow(parent) {
    setWindowTitle(QStringLiteral("omegamaps"));
    setMinimumSize(1280, 760);
    resize(1600, 980);

    auto *central = new QWidget(this);
    central->setObjectName(QStringLiteral("central"));
    setCentralWidget(central);
    auto *outer = new QVBoxLayout(central);
    outer->setContentsMargins(0, 0, 0, 0);
    outer->setSpacing(0);
    buildHeader(central);

    m_split = new QSplitter(Qt::Horizontal, central);
    m_split->setHandleWidth(6);
    m_split->setChildrenCollapsible(false);

    // The vault the credentials panel shows and a crawl is handed. One for
    // the life of the window; setVaultPath replaces it.
    m_vault = std::make_unique<Vault>(
        QSettings().value(QStringLiteral("vault/path"), defaultVaultPath()).toString());

    // Columns one and two: the form, scrolling when the screen is short.
    auto scrolled = [this](QWidget *content) {
        auto *area = new QScrollArea(m_split);
        area->setWidget(content);
        area->setWidgetResizable(true);
        area->setFrameShape(QFrame::NoFrame);
        area->setHorizontalScrollBarPolicy(Qt::ScrollBarAlwaysOff);
        area->setMinimumWidth(340);
        return area;
    };
    auto *formLeft = new QWidget;
    formLeft->setObjectName(QStringLiteral("central"));
    auto *leftCol = new QVBoxLayout(formLeft);
    leftCol->setContentsMargins(16, 16, 8, 16);
    leftCol->setSpacing(16);
    m_connection = new ConnectionPanel(formLeft);
    m_credentials = new CredentialsPanel(m_vault.get(), formLeft);
    leftCol->addWidget(m_connection);
    leftCol->addWidget(m_credentials, 1);

    auto *formMiddle = new QWidget;
    formMiddle->setObjectName(QStringLiteral("central"));
    auto *midCol = new QVBoxLayout(formMiddle);
    midCol->setContentsMargins(8, 16, 8, 16);
    midCol->setSpacing(16);
    m_options = new OptionsPanel(formMiddle);
    m_output = new OutputPanel(formMiddle);
    m_runPanel = new RunPanel(formMiddle);
    midCol->addWidget(m_options);
    midCol->addWidget(m_output);
    midCol->addWidget(m_runPanel);
    midCol->addStretch(1);

    // Column three: the run, as sc2 stacks it. It scrolls when the window is
    // shorter than the column's minimum, as the form's columns do: a progress
    // panel with seven depths and its waiting list, plus the smallest usable
    // preview and log, is taller than a laptop window, and a layout that
    // cannot fit its minimums shrinks everything below them -- which is what
    // crushed the progress panel's numbers. When there is room this is
    // invisible: no scroll bar, and the preview and log stretch as before.
    auto *right = new QWidget;
    right->setObjectName(QStringLiteral("central"));
    auto *rightCol = new QVBoxLayout(right);
    rightCol->setContentsMargins(8, 16, 16, 16);
    rightCol->setSpacing(16);
    m_progress = new ProgressPanel(right);
    m_preview = new TopologyPreview(right);
    m_log = new DiscoveryLog(right);
    // The progress panel gets its natural height, always; the preview and
    // the log are what give way in a short window. Left to the layout, the
    // shortfall came out of the progress panel and crushed its numbers.
    m_progress->setSizePolicy(QSizePolicy::Preferred, QSizePolicy::Fixed);
    rightCol->addWidget(m_progress);
    rightCol->addWidget(m_preview, 3);
    rightCol->addWidget(m_log, 2);
    right->setMinimumWidth(520);

    m_split->addWidget(scrolled(formLeft));
    m_split->addWidget(scrolled(formMiddle));
    QScrollArea *runArea = scrolled(right);
    runArea->setMinimumWidth(520);
    m_split->addWidget(runArea);
    m_split->setStretchFactor(0, 0);
    m_split->setStretchFactor(1, 0);
    m_split->setStretchFactor(2, 1);
    m_split->setSizes({380, 380, 840});

    m_form = new CrawlForm(m_vault.get(), m_connection, m_credentials, m_options, m_output,
                           m_runPanel, this);
    m_form->load();
    outer->addWidget(m_split, 1);

    m_bridge = new RunBridge(this);
    connect(m_bridge, &RunBridge::started, this, [this] {
        m_progress->reset(m_sourceLabel);
        m_preview->reset();
        m_preview->setFinished(false);
        m_log->reset();
        m_lastLinks = -1;
        m_stop->setEnabled(true);
        m_runPanel->viewMap->setEnabled(false);
        // Said here, between the reset and the first pull, so it heads the
        // log rather than landing after the first depth.
        if (!m_startLine.isEmpty()) m_log->info(m_startLine);
        if (!m_crawling && m_mapPath.isEmpty())
            m_log->info(tr("No map.json beside the recording; the preview keeps the reporting tree"));
    });
    connect(m_bridge, &RunBridge::rowsChanged, m_preview, &TopologyPreview::updateRows);
    connect(m_bridge, &RunBridge::rowsChanged, m_log, &DiscoveryLog::addRows);
    connect(m_bridge, &RunBridge::decisionsAdded, m_log, &DiscoveryLog::addDecisions);
    connect(m_bridge, &RunBridge::progressChanged, m_progress, &ProgressPanel::setProgress);
    connect(m_bridge, &RunBridge::progressChanged, m_log, &DiscoveryLog::noteProgress);
    connect(m_bridge, &RunBridge::finished, this, &MainWindow::onFinished);
    connect(m_bridge, &RunBridge::started, m_form, [this] { m_form->setRunning(true); });
    // The run panel's status line: what is happening now, and how the last
    // run ended.
    connect(m_bridge, &RunBridge::started, m_runPanel, [this] {
        if (m_crawling)
            m_runPanel->setStatus(tr("Running"), QStringLiteral("accent"));
        else
            m_runPanel->setStatus(tr("Replaying %1").arg(m_sourceLabel), QStringLiteral("muted"));
    });
    connect(m_bridge, &RunBridge::progressChanged, m_runPanel, [this](const RunProgress &p) {
        if (!m_crawling || p.finished) return;
        m_runPanel->setStatus(tr("Running  \u00B7  depth %1  \u00B7  %2")
                                  .arg(p.depth).arg(formatElapsed(p.elapsedMs)),
                              QStringLiteral("accent"));
    });
    connect(m_bridge, &RunBridge::finished, m_form, [this] { m_form->setRunning(false); });
    connect(m_form, &CrawlForm::startRequested, this,
            [this](const QByteArray &req) { openCrawl(req, m_vault->handle()); });
    connect(m_form, &CrawlForm::stopRequested, m_bridge, &RunBridge::cancel);
    connect(m_runPanel->viewMap, &QPushButton::clicked, this, [this] { openInViewer(m_mapPath); });
}

MainWindow::~MainWindow() { m_bridge->close(); }

QString MainWindow::defaultVaultPath() {
    // vaultcli.DefaultPath: the command-line tools' vault, so one vault serves
    // both.
    return QDir::home().filePath(QStringLiteral(".omegamaps/vault.json"));
}

void MainWindow::setVaultPath(const QString &path) {
    // The old vault locks as it goes; a crawl already running keeps using it
    // until it ends, so replacing it mid-crawl is refused by the caller.
    auto next = std::make_unique<Vault>(path);
    m_credentials->setVault(next.get());
    m_form->setVault(next.get());
    m_vault = std::move(next);
    QSettings().setValue(QStringLiteral("vault/path"), path);
}

bool MainWindow::tryQuietUnlock() {
    if (!m_vault->exists() || !m_vault->isLocked()) return false;
    const bool ok = m_vault->unlockQuiet() == VaultError::Ok;
    m_credentials->refresh();
    return ok;
}

void MainWindow::buildHeader(QWidget *central) {
    auto *header = new QFrame(central);
    header->setProperty("role", QStringLiteral("header"));
    header->setFixedHeight(56);
    auto *row = new QHBoxLayout(header);
    row->setContentsMargins(20, 0, 16, 0);
    row->setSpacing(12);

    auto *mark = new QLabel(header);
    mark->setProperty("role", QStringLiteral("panelMark"));
    mark->setFixedSize(6, 20);
    row->addWidget(mark);
    QLabel *wordmark = makeCaption(QStringLiteral("OMEGAMAPS"), "wordmark", 13, header);
    wordmark->setObjectName(QStringLiteral("wordmark"));
    row->addWidget(wordmark);
    // The wordmark is the About box, the way a logo is on a web page.
    AboutDialog::makeTrigger(mark);
    AboutDialog::makeTrigger(wordmark);

    m_subtitle = new QLabel(header);
    m_subtitle->setProperty("role", QStringLiteral("subtle"));
    row->addWidget(m_subtitle);
    row->addStretch(1);

    auto *open = new QPushButton(tr("Open recording\u2026"), header);
    open->setCursor(Qt::PointingHandCursor);
    open->setToolTip(tr("Play back a crawl recorded with crawl -events"));
    connect(open, &QPushButton::clicked, this, &MainWindow::chooseRecording);
    row->addWidget(open);

    auto *openMap = new QPushButton(tr("Open map\u2026"), header);
    openMap->setCursor(Qt::PointingHandCursor);
    openMap->setToolTip(tr("Open any map.json in the map viewer"));
    connect(openMap, &QPushButton::clicked, this, &MainWindow::chooseMapToView);
    row->addWidget(openMap);

    m_speed = new QComboBox(header);
    m_speed->addItem(tr("Real time"), 1.0);
    m_speed->addItem(QStringLiteral("10\u00D7"), 10.0);
    m_speed->addItem(QStringLiteral("50\u00D7"), 50.0);
    m_speed->addItem(tr("Instant"), 0.0);
    m_speed->setToolTip(tr("Replay speed; changing it restarts the replay"));
    m_speed->setCurrentIndex(1);
    connect(m_speed, qOverload<int>(&QComboBox::activated), this, &MainWindow::restartAtSpeed);
    row->addWidget(m_speed);

    m_stop = new QPushButton(tr("Stop"), header);
    m_stop->setCursor(Qt::PointingHandCursor);
    m_stop->setEnabled(false);
    connect(m_stop, &QPushButton::clicked, this, [this] { m_bridge->cancel(); });
    row->addWidget(m_stop);

    m_theme = new QComboBox(header);
    m_theme->addItem(tr("Cyber"), themeKey(ThemeId::Cyber));
    m_theme->addItem(tr("Dark"), themeKey(ThemeId::Dark));
    m_theme->addItem(tr("Light"), themeKey(ThemeId::Light));
    m_theme->setCurrentIndex(m_theme->findData(themeKey(ThemeManager::instance().id())));
    connect(m_theme, qOverload<int>(&QComboBox::currentIndexChanged), this, [this](int i) {
        const ThemeId id = themeFromKey(m_theme->itemData(i).toString());
        ThemeManager::instance().setTheme(id);
        QSettings().setValue(QStringLiteral("theme"), themeKey(id));
    });
    connect(&ThemeManager::instance(), &ThemeManager::changed, this, [this] {
        const int i = m_theme->findData(themeKey(ThemeManager::instance().id()));
        if (i >= 0 && i != m_theme->currentIndex()) m_theme->setCurrentIndex(i);
    });
    row->addWidget(m_theme);

    static_cast<QVBoxLayout *>(central->layout())->addWidget(header);
}

double MainWindow::selectedSpeed() const { return m_speed->currentData().toDouble(); }

void MainWindow::selectSpeed(double speed) {
    for (int i = 0; i < m_speed->count(); ++i) {
        if (qFuzzyCompare(m_speed->itemData(i).toDouble() + 1, speed + 1)) {
            m_speed->setCurrentIndex(i);
            return;
        }
    }
    // A speed from the command line that the list does not offer: add it,
    // rather than showing a speed that is not the one playing.
    m_speed->addItem(QStringLiteral("%1\u00D7").arg(speed), speed);
    m_speed->setCurrentIndex(m_speed->count() - 1);
}

bool MainWindow::openReplay(const QString &eventsPath, const QString &mapPath, double speed) {
    m_eventsPath = eventsPath;
    m_mapPath = mapPath;
    if (m_mapPath.isEmpty()) {
        const QString beside = QFileInfo(eventsPath).dir().filePath(QStringLiteral("map.json"));
        if (QFileInfo::exists(beside)) m_mapPath = beside;
    }
    selectSpeed(speed);
    m_crawling = false;
    m_sourceLabel = QFileInfo(eventsPath).fileName();

    const QString speedText = speed <= 0 ? tr("instant") : QStringLiteral("%1\u00D7").arg(speed);
    m_startLine = tr("Replaying %1 at %2; durations are as recorded")
                      .arg(QFileInfo(eventsPath).fileName(), speedText);

    QString err;
    if (!m_bridge->openReplay(eventsPath, speed, &err)) {
        m_log->reset();
        m_log->error(tr("Could not open %1: %2").arg(eventsPath, err));
        m_subtitle->clear();
        return false;
    }
    m_subtitle->setText(tr("replay  \u00B7  %1  \u00B7  %2")
                            .arg(QFileInfo(eventsPath).fileName(), speedText));
    return true;
}

bool MainWindow::openCrawl(const QByteArray &request, long long vault) {
    const QJsonObject req = QJsonDocument::fromJson(request).object();
    QStringList seeds;
    for (const QJsonValue &v : req.value(QLatin1String("seeds")).toArray()) seeds << v.toString();

    // The map path comes back from the run's result when it finishes; until
    // then there is nothing to load and nothing to replay.
    m_crawling = true;
    m_eventsPath.clear();
    m_mapPath.clear();
    m_sourceLabel = seeds.join(QStringLiteral(", "));
    m_startLine = tr("Crawling from %1").arg(m_sourceLabel);

    QString err;
    if (!m_bridge->openCrawl(request, vault, &err)) {
        m_crawling = false;
        m_form->setRunning(false);
        m_runPanel->setStatus(tr("Could not start: %1").arg(err), QStringLiteral("danger"));
        m_log->reset();
        m_log->error(tr("Could not start the crawl: %1").arg(err));
        m_subtitle->clear();
        return false;
    }
    m_subtitle->setText(tr("crawl  \u00B7  %1").arg(m_sourceLabel));
    return true;
}

void MainWindow::chooseRecording() {
    const QString last = m_eventsPath.isEmpty() ? QSettings().value(QStringLiteral("replay/lastRecording")).toString()
                                                : m_eventsPath;
    const QString path = PathPrompt::getPath(this, tr("Open recording"),
                                             tr("A crawl recording (the -events file a crawl writes):"), last,
                                             tr("Crawl recordings (*.jsonl *.json);;All files (*)"));
    if (path.isEmpty()) return;
    QSettings().setValue(QStringLiteral("replay/lastRecording"), path);
    openReplay(path, QString(), selectedSpeed());
}

void MainWindow::chooseMapToView() {
    QString last = QSettings().value(QStringLiteral("viewer/lastMap")).toString();
    if (last.isEmpty()) last = m_mapPath;
    const QString path = PathPrompt::getPath(this, tr("Open map"), tr("A map written by a crawl (map.json):"), last,
                                             tr("Map JSON (*.json);;All files (*)"));
    if (path.isEmpty()) return;
    QSettings().setValue(QStringLiteral("viewer/lastMap"), path);
    openInViewer(path);
}

bool MainWindow::openInViewer(const QString &mapPath) {
    if (mapPath.isEmpty() || !QFileInfo::exists(mapPath)) {
        QMessageBox::warning(this, tr("Map viewer"),
                             tr("There is no map file at %1").arg(QDir::toNativeSeparators(mapPath)));
        return false;
    }
    const QString theme = themeKey(ThemeManager::instance().id());
    QString err, used;
    bool notFound = false;
    // Said on success too: a launch that works but shows nothing (a window
    // behind this one) must not look the same as a click that did nothing.
    const auto opened = [&] {
        m_log->info(tr("Opened %1 in the map viewer (%2)")
                        .arg(QFileInfo(mapPath).fileName(), QDir::toNativeSeparators(used)));
        return true;
    };
    if (launchViewer(mapPath, theme, &err, &notFound, &used)) return opened();
    qInfo("omegamaps: map viewer: %s", qPrintable(err));
    if (!notFound) {
        QMessageBox::warning(this, tr("Map viewer"), err);
        return false;
    }

    QMessageBox box(QMessageBox::Warning, tr("Map viewer"), err, QMessageBox::Cancel, this);
    QPushButton *locate = box.addButton(tr("Locate\u2026"), QMessageBox::AcceptRole);
    box.setDefaultButton(locate);
    box.exec();
    if (box.clickedButton() != locate || !locateViewer()) return false;
    if (launchViewer(mapPath, theme, &err, nullptr, &used)) return opened();
    QMessageBox::warning(this, tr("Map viewer"), err);
    return false;
}

bool MainWindow::locateViewer(const QString &picked) {
    QString path = picked;
    if (path.isEmpty()) {
        const QString saved = QSettings().value(QLatin1String(kViewerPathKey)).toString();
#if defined(Q_OS_WIN)
        const QString filter = tr("Map viewer (omegamaps-viewer.exe);;Programs (*.exe)");
#else
        const QString filter = tr("All files (*)");
#endif
        path = PathPrompt::getPath(
            this, tr("Locate the map viewer"),
            tr("The omegamaps-viewer program (on macOS, omegamaps-viewer.app or the program inside it):"),
            saved.isEmpty() ? QCoreApplication::applicationDirPath() : saved, filter,
            [](const QString &p) {
                return viewerExecutableFor(p).isEmpty() ? tr("Not a program, or an .app with one inside")
                                                        : QString();
            });
        if (path.isEmpty()) return false;
    }
    const QString exe = viewerExecutableFor(path);
    QString why;
    if (exe.isEmpty()) {
        why = tr("%1 is not a program").arg(QDir::toNativeSeparators(path));
    } else if (looksLikeViewer(exe, &why)) {
        saveViewerPath(exe);
        m_log->info(tr("Map viewer: %1").arg(QDir::toNativeSeparators(exe)));
        return true;
    }
    QMessageBox::warning(this, tr("Map viewer"), why);
    return false;
}

void MainWindow::restartAtSpeed() {
    if (!m_eventsPath.isEmpty()) openReplay(m_eventsPath, m_mapPath, selectedSpeed());
}

void MainWindow::onFinished(const RunProgress &p) {
    m_stop->setEnabled(false);
    m_preview->setFinished(true);
    m_log->noteFinished(p);

    const RunResult r = m_bridge->result();
    m_lastResult = r;
    const QString counts = tr("%1 reached, %2 failed").arg(p.counts.reached).arg(p.counts.failed);
    const QString took = formatElapsed(p.elapsedMs);
    if (r.kind != QLatin1String("crawl"))
        m_runPanel->setStatus(tr("Replay finished  \u00B7  %1").arg(counts), QStringLiteral("muted"));
    else if (r.state == QLatin1String("failed"))
        m_runPanel->setStatus(tr("Failed after %1: %2").arg(took, r.error), QStringLiteral("danger"));
    else if (r.state == QLatin1String("cancelled"))
        m_runPanel->setStatus(tr("Stopped after %1  \u00B7  %2; the map holds what was reached")
                                  .arg(took, counts),
                              QStringLiteral("warning"));
    else
        m_runPanel->setStatus(tr("Completed in %1  \u00B7  %2").arg(took, counts), QStringLiteral("success"));
    if (r.kind == QLatin1String("crawl")) {
        // A finished crawl is a recording like any other: choosing a speed
        // now replays it.
        m_mapPath = r.mapPath;
        m_eventsPath = r.eventsPath;
        if (r.state == QLatin1String("failed")) {
            m_log->error(tr("The map could not be written: %1").arg(r.error));
            return;
        }
        if (r.state == QLatin1String("cancelled"))
            m_log->info(tr("Stopped; the map holds what was reached before the stop"));
        m_log->info(tr("Recorded to %1; crawl log in %2")
                        .arg(QFileInfo(r.eventsPath).fileName(), QFileInfo(r.logPath).fileName()));
    }
    if (m_mapPath.isEmpty()) return;

    TopologyMap map;
    QString err;
    if (!loadMap(m_mapPath, &map, &err)) {
        m_log->error(tr("Could not read %1: %2").arg(m_mapPath, err));
        return;
    }
    int unmatched = 0;
    m_lastLinks = m_preview->setFinalMap(map, &unmatched);
    m_runPanel->viewMap->setEnabled(true);
    QString line = tr("Map: %1 links between %2 devices from %3")
                       .arg(m_lastLinks)
                       .arg(m_preview->deviceCount())
                       .arg(QFileInfo(m_mapPath).fileName());
    if (unmatched > 0) line += tr(" (%1 map devices not in this run)").arg(unmatched);
    m_log->info(line);
}

}  // namespace omegamaps
