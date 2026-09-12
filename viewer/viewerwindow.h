// viewer/viewerwindow.h
//
// The map viewer: a native window around internal/mapweb's page.
//
// The page is the browser viewer, unchanged in what it draws and exports; it
// is served from this process over loopback (MapServer) and shown in a
// QWebEngineView. What the window adds is what a browser tab cannot give it:
// the application's own palette and chrome, native menus and save dialogs,
// and a lifetime that is the window's. When the window closes, the server
// stops and the page's token dies with it.
//
// Exports take two routes and both end in a native save dialog:
//   - the File menu asks the page for the data (omegamapsExport) and writes
//     it with QSaveFile. No browser download is involved, so nothing depends
//     on Chromium's rules for script-started downloads.
//   - the page's own Export buttons still start a download; the profile's
//     downloadRequested puts the dialog in front of it.
//
// The profile is off the record. Saved layouts go to the server's layout
// store (~/.omegamaps/layouts), so the page needs no persistent browser
// storage, and nothing about a map is left in a browser cache on disk.

#ifndef OMEGAMAPS_VIEWER_VIEWERWINDOW_H
#define OMEGAMAPS_VIEWER_VIEWERWINDOW_H

#include <QJsonObject>
#include <QMainWindow>
#include <QTimer>

#include <functional>

#include "mapserver.h"
#include "theme.h"

class QAction;
class QActionGroup;
class QLabel;
class QWebEngineDownloadRequest;
class QWebEngineProfile;
class QWebEngineView;

namespace omegamaps {

class ViewerWindow : public QMainWindow {
    Q_OBJECT
public:
    explicit ViewerWindow(QWidget *parent = nullptr);
    ~ViewerWindow() override;

    // Loads a map and shows it. On failure the window keeps whatever it was
    // showing and *err says why.
    bool openMap(const QString &path, QString *err = nullptr);

    // Where saved layouts go; empty (the default) is ~/.omegamaps/layouts.
    // Takes effect at the first openMap.
    void setLayoutDir(const QString &dir) { m_layoutDir = dir; }

    QString mapPath() const { return m_server.mapPath(); }
    const MapServer &server() const { return m_server; }
    QWebEngineView *view() const { return m_view; }

    // What the page last reported (omegamapsStatus): state, name, nodes,
    // edges, visible, layout_store, layout_status, banner. Empty before the
    // first page is ready.
    QJsonObject pageStatus() const { return m_status; }

    // The status bar's standing line: the map's counts and path.
    QString statusText() const;

    // Where an export goes, given the suggested path; "" cancels. The default
    // is a native save dialog. Replaceable so a probe can answer it.
    using SaveChooser = std::function<QString(const QString &suggested)>;
    void setSaveChooser(SaveChooser chooser) { m_chooser = std::move(chooser); }

    // The page's colours come from the application's theme tokens.
    static QJsonObject pageTheme(const Tokens &t);

    // Directory the next export dialog opens in.
    QString exportDir() const;

public slots:
    void chooseMap();
    void reload();
    void exportPng() { exportAs(QStringLiteral("png")); }
    void exportJson() { exportAs(QStringLiteral("json")); }
    void exportDrawio() { exportAs(QStringLiteral("drawio")); }
    void exportAs(const QString &kind);

signals:
    // The page has loaded a map and reported on it (ok), or could not.
    void pageReady(bool ok);
    // An export finished: written to path, or not, with the reason.
    void exportFinished(const QString &path, bool ok, const QString &error);

protected:
    void closeEvent(QCloseEvent *e) override;

private:
    void buildMenus();
    void showPage();
    void pollStatus();
    void applyTheme();
    void onDownload(QWebEngineDownloadRequest *d);
    QString choosePath(const QString &suggested);
    void rememberExportDir(const QString &path);
    void updateTitle();
    void clickPage(const char *elementId);

    MapServer m_server;
    QString m_layoutDir;
    QWebEngineProfile *m_profile = nullptr;
    QWebEngineView *m_view = nullptr;
    QLabel *m_statusLabel = nullptr;
    QActionGroup *m_themeGroup = nullptr;
    QList<QAction *> m_mapActions;  // enabled only while a map is shown

    QTimer m_poll;
    int m_pollsLeft = 0;
    QJsonObject m_status;
    SaveChooser m_chooser;
};

}  // namespace omegamaps

#endif
