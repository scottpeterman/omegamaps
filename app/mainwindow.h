// app/mainwindow.h
//
// The window: sc2's header bar over the run's three views. The layout here
// is the replay viewer's -- progress and log on the left, the map on the
// right. When the crawl form lands (sc2's connection, credentials, options
// and output panels), these three move into its right-hand column unchanged.

#ifndef OMEGAMAPS_APP_MAINWINDOW_H
#define OMEGAMAPS_APP_MAINWINDOW_H

#include <QMainWindow>

#include <memory>

#include "runtypes.h"

class QComboBox;
class QLabel;
class QPushButton;
class QSplitter;

namespace omegamaps {

class ConnectionPanel;
class CrawlForm;
class CredentialsPanel;
class DiscoveryLog;
class OptionsPanel;
class OutputPanel;
class RunPanel;
class Vault;
class ProgressPanel;
class RunBridge;
class TopologyPreview;

class MainWindow : public QMainWindow {
    Q_OBJECT
public:
    explicit MainWindow(QWidget *parent = nullptr);
    ~MainWindow() override;

    // Plays a recording. mapPath may be empty: then a map.json beside the
    // recording is used if there is one.
    bool openReplay(const QString &eventsPath, const QString &mapPath, double speed);

    // Starts a live crawl: request is the JSON omegamaps_crawl_open takes, and
    // vault an unlocked Vault's handle(). The map loads from wherever the run
    // wrote it; afterwards the recording can be replayed at any speed.
    bool openCrawl(const QByteArray &request, long long vault);

    CrawlForm *crawlForm() const { return m_form; }
    ConnectionPanel *connectionPanel() const { return m_connection; }
    CredentialsPanel *credentialsPanel() const { return m_credentials; }
    OptionsPanel *optionsPanel() const { return m_options; }
    OutputPanel *outputPanel() const { return m_output; }
    RunPanel *runPanel() const { return m_runPanel; }
    Vault *vault() const { return m_vault.get(); }

    // Switches to the vault at path, remembered for next time. Not while a
    // crawl runs: it holds the current one.
    void setVaultPath(const QString &path);
    static QString defaultVaultPath();

    // Unlocks from the OS keyring or OMEGAMAPS_VAULT_PASSWORD without asking,
    // for startup. False when the vault stays locked.
    bool tryQuietUnlock();

    // How the last run ended; kind is empty before one has.
    const RunResult &lastResult() const { return m_lastResult; }

    RunBridge *bridge() const { return m_bridge; }
    ProgressPanel *progressPanel() const { return m_progress; }
    TopologyPreview *preview() const { return m_preview; }
    DiscoveryLog *log() const { return m_log; }

    // Links drawn from map.json at the end of the last run, -1 before then.
    int lastLinkCount() const { return m_lastLinks; }

    // Opens mapPath in the map viewer (omegamaps-viewer), in the current
    // theme. When the viewer cannot be found, offers to locate it; any other
    // failure is said in a dialog.
    bool openInViewer(const QString &mapPath);

    // Takes picked (a program, or on macOS an .app bundle) as the map viewer
    // after checking it is one, and saves it. Empty picked asks with a file
    // dialog. False, having said why, when it is not the viewer.
    bool locateViewer(const QString &picked = QString());

private:
    void buildHeader(QWidget *central);
    void chooseRecording();
    void chooseMapToView();
    void restartAtSpeed();
    void onFinished(const RunProgress &p);
    double selectedSpeed() const;
    void selectSpeed(double speed);

    RunBridge *m_bridge = nullptr;
    ProgressPanel *m_progress = nullptr;
    TopologyPreview *m_preview = nullptr;
    DiscoveryLog *m_log = nullptr;
    QSplitter *m_split = nullptr;

    QLabel *m_subtitle = nullptr;
    QComboBox *m_speed = nullptr;
    QComboBox *m_theme = nullptr;
    QPushButton *m_stop = nullptr;

    QString m_eventsPath;
    QString m_mapPath;
    std::unique_ptr<Vault> m_vault;
    ConnectionPanel *m_connection = nullptr;
    CredentialsPanel *m_credentials = nullptr;
    OptionsPanel *m_options = nullptr;
    OutputPanel *m_output = nullptr;
    RunPanel *m_runPanel = nullptr;
    CrawlForm *m_form = nullptr;
    QString m_startLine;
    QString m_sourceLabel;
    bool m_crawling = false;
    RunResult m_lastResult;
    int m_lastLinks = -1;
};

}  // namespace omegamaps

#endif
