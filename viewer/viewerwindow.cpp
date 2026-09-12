// viewer/viewerwindow.cpp

#include "viewerwindow.h"

#include "aboutdialog.h"
#include "filedialogs.h"
#include "pathprompt.h"

#include <QAction>
#include <QActionGroup>
#include <QCloseEvent>
#include <QDesktopServices>
#include <QDir>
#include <QFileInfo>
#include <QJsonDocument>
#include <QKeySequence>
#include <QLabel>
#include <QMenuBar>
#include <QPointer>
#include <QSaveFile>
#include <QSettings>
#include <QStandardPaths>
#include <QStatusBar>
#include <QUrlQuery>
#include <QWebEngineDownloadRequest>
#include <QWebEnginePage>
#include <QWebEngineProfile>
#include <QWebEngineSettings>
#include <QWebEngineView>

namespace omegamaps {

namespace {

// The page may go to its own server and nowhere else. Its downloads are
// blob: URLs and never navigate; anything else -- a link that somehow
// appears, a script that sets location -- is refused, and a clicked web link
// goes to the desktop's browser instead of replacing the map.
class ViewerPage : public QWebEnginePage {
public:
    ViewerPage(QWebEngineProfile *profile, QObject *parent) : QWebEnginePage(profile, parent) {}
    void setAllowedPort(int port) { m_port = port; }

protected:
    bool acceptNavigationRequest(const QUrl &url, NavigationType type, bool isMainFrame) override {
        Q_UNUSED(isMainFrame);
        const QString scheme = url.scheme();
        if (scheme == QLatin1String("blob") || scheme == QLatin1String("data")) return true;
        if (scheme == QLatin1String("http") && url.host() == QLatin1String("127.0.0.1") && url.port() == m_port)
            return true;
        if (type == NavigationTypeLinkClicked && (scheme == QLatin1String("http") || scheme == QLatin1String("https")))
            QDesktopServices::openUrl(url);
        return false;
    }

    // No pop-ups: the viewer is one window.
    QWebEnginePage *createWindow(WebWindowType) override { return nullptr; }

private:
    int m_port = -1;
};

QString hex(const QColor &c) { return c.name(QColor::HexRgb); }

QString filterFor(const QString &suffix) {
    if (suffix == QLatin1String("png")) return QObject::tr("PNG image (*.png)");
    if (suffix == QLatin1String("drawio")) return QObject::tr("draw.io diagram (*.drawio)");
    if (suffix == QLatin1String("json")) return QObject::tr("Map JSON (*.json)");
    return QObject::tr("All files (*)");
}

const char *kStatusJs = "window.omegamapsStatus ? window.omegamapsStatus() : null";

}  // namespace

ViewerWindow::ViewerWindow(QWidget *parent) : QMainWindow(parent) {
    setWindowTitle(tr("omegamaps map viewer"));
    resize(1280, 820);

    // Off the record: see the header.
    m_profile = new QWebEngineProfile(this);
    connect(m_profile, &QWebEngineProfile::downloadRequested, this, &ViewerWindow::onDownload);

    m_view = new QWebEngineView(this);
    m_view->setPage(new ViewerPage(m_profile, m_view));
    // The browser's own menu (Back, Reload, View source) has nothing to offer
    // a map; right-click in the page still reaches the page's scripts.
    m_view->setContextMenuPolicy(Qt::NoContextMenu);
    m_view->settings()->setAttribute(QWebEngineSettings::FocusOnNavigationEnabled, true);
    setCentralWidget(m_view);
    connect(m_view, &QWebEngineView::loadFinished, this, [this](bool ok) {
        if (!ok) {
            m_statusLabel->setText(tr("The map page did not load"));
            emit pageReady(false);
            return;
        }
        m_pollsLeft = 300;  // 15 s at 50 ms; a map that big is not a map
        pollStatus();
    });

    m_statusLabel = new QLabel(this);
    m_statusLabel->setProperty("tone", QStringLiteral("secondary"));
    statusBar()->addWidget(m_statusLabel, 1);
    statusBar()->setSizeGripEnabled(true);

    m_poll.setSingleShot(true);
    m_poll.setInterval(50);
    connect(&m_poll, &QTimer::timeout, this, &ViewerWindow::pollStatus);

    buildMenus();
    applyTheme();
    connect(&ThemeManager::instance(), &ThemeManager::changed, this, &ViewerWindow::applyTheme);

    const QByteArray geo = QSettings().value(QStringLiteral("geometry")).toByteArray();
    if (!geo.isEmpty()) restoreGeometry(geo);
    updateTitle();
}

ViewerWindow::~ViewerWindow() {
    // Pages before their profile, or WebEngine warns that the profile is
    // released with a page still alive. The page is the view's child.
    delete m_view;
    m_view = nullptr;
}

void ViewerWindow::buildMenus() {
    QMenu *file = menuBar()->addMenu(tr("&File"));
    QAction *open = file->addAction(tr("&Open Map\u2026"), this, &ViewerWindow::chooseMap);
    open->setShortcut(QKeySequence::Open);
    QAction *reloadAct = file->addAction(tr("&Reload"), this, &ViewerWindow::reload);
    reloadAct->setShortcuts({QKeySequence::Refresh, QKeySequence(Qt::CTRL | Qt::Key_R)});
    file->addSeparator();
    QMenu *exp = file->addMenu(tr("&Export"));
    QAction *png = exp->addAction(tr("&PNG Image\u2026"), this, &ViewerWindow::exportPng);
    png->setShortcut(QKeySequence(Qt::CTRL | Qt::SHIFT | Qt::Key_E));
    QAction *json = exp->addAction(tr("Map &JSON\u2026"), this, &ViewerWindow::exportJson);
    QAction *drawio = exp->addAction(tr("&draw.io Diagram\u2026"), this, &ViewerWindow::exportDrawio);
    file->addSeparator();
    QAction *close = file->addAction(tr("&Close"), this, &QWidget::close);
    close->setShortcut(QKeySequence::Close);

    QMenu *view = menuBar()->addMenu(tr("&View"));
    QAction *fit = view->addAction(tr("&Fit"), this, [this] { clickPage("btnFit"); });
    fit->setShortcut(QKeySequence(Qt::CTRL | Qt::Key_0));
    QAction *zin = view->addAction(tr("Zoom &In"), this, [this] { clickPage("btnZoomIn"); });
    zin->setShortcuts({QKeySequence::ZoomIn, QKeySequence(Qt::CTRL | Qt::Key_Equal)});
    QAction *zout = view->addAction(tr("Zoom &Out"), this, [this] { clickPage("btnZoomOut"); });
    zout->setShortcut(QKeySequence::ZoomOut);
    view->addSeparator();

    QMenu *themes = view->addMenu(tr("&Theme"));
    m_themeGroup = new QActionGroup(this);
    for (ThemeId id : {ThemeId::Cyber, ThemeId::Dark, ThemeId::Light}) {
        QAction *a = themes->addAction(tokensFor(id).name);
        a->setCheckable(true);
        a->setData(themeKey(id));
        m_themeGroup->addAction(a);
        connect(a, &QAction::triggered, this, [id] { ThemeManager::instance().setTheme(id); });
    }

    QMenu *help = menuBar()->addMenu(tr("&Help"));
    QAction *about = help->addAction(tr("&About omegamaps"), this, [this] { AboutDialog::showFor(this); });
    about->setMenuRole(QAction::AboutRole);  // macOS: the application menu, where About belongs

    m_mapActions = {reloadAct, png, json, drawio, fit, zin, zout};
    for (QAction *a : m_mapActions) a->setEnabled(false);
}

// ---------------------------------------------------------------------------
// Theme

QJsonObject ViewerWindow::pageTheme(const Tokens &t) {
    // Page CSS variables (internal/mapweb/assets/index.html) from the tokens
    // the rest of the application paints with, and the graph colours the way
    // the topology preview uses them: links in the accent, labels on panel
    // chips, failed or undiscovered in danger.
    QJsonObject vars{
        {QStringLiteral("--bg"), hex(t.bgPrimary)},
        {QStringLiteral("--panel"), hex(t.bgSecondary)},
        {QStringLiteral("--panel-2"), hex(t.bgTertiary)},
        {QStringLiteral("--line"), hex(t.borderSecondary)},
        {QStringLiteral("--fg"), hex(t.textPrimary)},
        {QStringLiteral("--fg-dim"), hex(t.textSecondary)},
        {QStringLiteral("--accent"), hex(t.accent)},
        {QStringLiteral("--on-accent"), hex(t.textOnAccent)},
        {QStringLiteral("--green"), hex(t.success)},
        {QStringLiteral("--red"), hex(t.danger)},
        {QStringLiteral("--amber"), hex(t.warning)},
    };
    QJsonObject cy{
        {QStringLiteral("text"), hex(t.textPrimary)},
        {QStringLiteral("textBg"), hex(t.bgSecondary)},
        {QStringLiteral("edge"), hex(t.accentDim)},
        {QStringLiteral("select"), hex(t.accent)},
        {QStringLiteral("undiscovered"), hex(t.danger)},
        {QStringLiteral("bg"), hex(t.bgPrimary)},
    };
    return QJsonObject{{QStringLiteral("dark"), t.dark}, {QStringLiteral("vars"), vars}, {QStringLiteral("cy"), cy}};
}

void ViewerWindow::applyTheme() {
    const Tokens &t = ThemeManager::instance().tokens();
    // Before and between pages the view is this colour, not a white flash.
    m_view->page()->setBackgroundColor(t.bgPrimary);
    const QString key = themeKey(ThemeManager::instance().id());
    for (QAction *a : m_themeGroup->actions()) a->setChecked(a->data().toString() == key);

    if (!m_server.isOpen()) return;
    const QByteArray json = QJsonDocument(pageTheme(t)).toJson(QJsonDocument::Compact);
    m_view->page()->runJavaScript(
        QStringLiteral("window.omegamapsSetTheme && window.omegamapsSetTheme(%1)").arg(QString::fromUtf8(json)));
}

// ---------------------------------------------------------------------------
// Map and page

bool ViewerWindow::openMap(const QString &path, QString *err) {
    QString why;
    if (!m_server.load(path, &why, m_layoutDir)) {
        if (err) *err = why;
        statusBar()->showMessage(tr("Could not open %1: %2").arg(QFileInfo(path).fileName(), why), 8000);
        return false;
    }
    static_cast<ViewerPage *>(m_view->page())->setAllowedPort(m_server.port());
    showPage();
    return true;
}

void ViewerWindow::showPage() {
    m_status = QJsonObject();
    for (QAction *a : m_mapActions) a->setEnabled(false);
    m_statusLabel->setText(tr("Loading %1\u2026").arg(m_server.mapName()));
    updateTitle();

    // The theme goes in the URL so the page's first paint is already in it
    // (index.html applies it from <head>); the page rewrites the address
    // afterwards and keeps both for a reload.
    QUrl u = m_server.url();
    const QByteArray theme = QJsonDocument(pageTheme(ThemeManager::instance().tokens())).toJson(QJsonDocument::Compact);
    u.setQuery(u.query(QUrl::FullyEncoded) + QStringLiteral("&theme=") +
                   QString::fromLatin1(QUrl::toPercentEncoding(QString::fromUtf8(theme))),
               QUrl::StrictMode);
    m_view->setUrl(u);
}

void ViewerWindow::reload() {
    if (!m_server.isOpen()) return;
    // Re-read the file: a re-crawl may have rewritten it since it was opened.
    const QString path = m_server.mapPath();
    QString err;
    if (!m_server.load(path, &err)) {
        statusBar()->showMessage(tr("Reload failed; still showing the previous copy: %1").arg(err), 8000);
        return;
    }
    showPage();
}

void ViewerWindow::chooseMap() {
    const QString last = m_server.isOpen() ? m_server.mapPath() : QSettings().value(QStringLiteral("lastMap")).toString();
    const QString path = PathPrompt::getPath(this, tr("Open map"), tr("A map written by a crawl (map.json):"), last,
                                             tr("Map JSON (*.json);;All files (*)"));
    if (path.isEmpty()) return;
    QSettings().setValue(QStringLiteral("lastMap"), path);
    openMap(path);
}

void ViewerWindow::pollStatus() {
    QPointer<ViewerWindow> self(this);
    m_view->page()->runJavaScript(QString::fromLatin1(kStatusJs), [self](const QVariant &v) {
        if (!self) return;
        const QJsonObject st = QJsonObject::fromVariantMap(v.toMap());
        const QString state = st.value(QStringLiteral("state")).toString();
        if (state.isEmpty() || state == QLatin1String("loading")) {
            if (--self->m_pollsLeft > 0) {
                self->m_poll.start();
            } else {
                self->m_statusLabel->setText(tr("The map page did not finish loading"));
                emit self->pageReady(false);
            }
            return;
        }
        self->m_status = st;
        const bool ok = state == QLatin1String("ready");
        for (QAction *a : self->m_mapActions) a->setEnabled(ok);
        // Reload stays available after an error: it is the way out of one.
        if (!self->m_mapActions.isEmpty()) self->m_mapActions.first()->setEnabled(true);
        if (ok) {
            // The page's own inventory, so the status bar and the sidebar
            // never disagree about what a device is.
            const QJsonObject n = st.value(QStringLiteral("stats")).toObject();
            self->m_statusLabel->setText(tr("%1 devices  \u00B7  %2 leaves  \u00B7  %3 links  \u00B7  %4")
                                             .arg(n.value(QStringLiteral("devices")).toInt())
                                             .arg(n.value(QStringLiteral("leaves")).toInt())
                                             .arg(n.value(QStringLiteral("links")).toInt())
                                             .arg(QDir::toNativeSeparators(self->m_server.mapPath())));
        } else {
            self->m_statusLabel->setText(st.value(QStringLiteral("banner")).toString());
        }
        self->updateTitle();
        emit self->pageReady(ok);
    });
}

QString ViewerWindow::statusText() const { return m_statusLabel->text(); }

void ViewerWindow::clickPage(const char *elementId) {
    m_view->page()->runJavaScript(
        QStringLiteral("(function(){var b=document.getElementById('%1'); if (b) b.click();})()")
            .arg(QLatin1String(elementId)));
}

void ViewerWindow::updateTitle() {
    setWindowTitle(m_server.isOpen() ? tr("%1 \u2014 omegamaps map viewer").arg(m_server.mapName())
                                     : tr("omegamaps map viewer"));
    setWindowFilePath(m_server.mapPath());
}

// ---------------------------------------------------------------------------
// Exports

QString ViewerWindow::exportDir() const {
    const QString remembered = QSettings().value(QStringLiteral("exportDir")).toString();
    if (!remembered.isEmpty() && QFileInfo(remembered).isDir()) return remembered;
    if (m_server.isOpen()) return QFileInfo(m_server.mapPath()).absolutePath();
    return QStandardPaths::writableLocation(QStandardPaths::DocumentsLocation);
}

void ViewerWindow::rememberExportDir(const QString &path) {
    QSettings().setValue(QStringLiteral("exportDir"), QFileInfo(path).absolutePath());
}

QString ViewerWindow::choosePath(const QString &suggested) {
    const QString suffix = QFileInfo(suggested).suffix();
    QString path = m_chooser ? m_chooser(suggested)
                             : pickSaveFile(this, tr("Export map"), suggested, filterFor(suffix));
    // Not every platform's dialog appends the filter's suffix.
    if (!path.isEmpty() && QFileInfo(path).suffix().isEmpty() && !suffix.isEmpty())
        path += QLatin1Char('.') + suffix;
    return path;
}

void ViewerWindow::exportAs(const QString &kind) {
    if (!m_server.isOpen()) return;
    const QString js = QStringLiteral(
        "(function(){try{return window.omegamapsExport('%1');}"
        "catch(e){return {error: String(e && e.message || e)};}})()").arg(kind);
    QPointer<ViewerWindow> self(this);
    m_view->page()->runJavaScript(js, [self, kind](const QVariant &v) {
        if (!self) return;
        const QVariantMap m = v.toMap();
        QString err = m.value(QStringLiteral("error")).toString();
        if (err.isEmpty() && m.value(QStringLiteral("data")).toString().isEmpty())
            err = tr("the map page is not ready");
        if (!err.isEmpty()) {
            self->statusBar()->showMessage(tr("Export failed: %1").arg(err), 8000);
            emit self->exportFinished(QString(), false, err);
            return;
        }

        const QString target =
            self->choosePath(QDir(self->exportDir()).filePath(m.value(QStringLiteral("name")).toString()));
        if (target.isEmpty()) {
            emit self->exportFinished(QString(), false, tr("cancelled"));
            return;
        }
        const QString data = m.value(QStringLiteral("data")).toString();
        const QByteArray bytes = m.value(QStringLiteral("encoding")).toString() == QLatin1String("base64")
                                     ? QByteArray::fromBase64(data.toLatin1())
                                     : data.toUtf8();
        QSaveFile f(target);
        if (!f.open(QIODevice::WriteOnly) || f.write(bytes) != bytes.size() || !f.commit()) {
            const QString why = f.errorString();
            self->statusBar()->showMessage(tr("Could not write %1: %2").arg(target, why), 8000);
            emit self->exportFinished(target, false, why);
            return;
        }
        self->rememberExportDir(target);
        self->statusBar()->showMessage(tr("Exported %1").arg(QDir::toNativeSeparators(target)), 6000);
        emit self->exportFinished(target, true, QString());
    });
}

// The page's own Export buttons: a browser download, with a native save
// dialog in front of it. The page names its files generically; the map's
// name is more use in a directory of exports.
void ViewerWindow::onDownload(QWebEngineDownloadRequest *d) {
    QString name = d->downloadFileName();
    const QString base = QFileInfo(m_server.mapPath()).completeBaseName();
    if (name == QLatin1String("topology.png")) name = base + QStringLiteral(".png");
    else if (name == QLatin1String("map.json")) name = base + QStringLiteral(".json");

    // The dialog is modal and the request waits for it; it has to be
    // answered before this slot returns, or WebEngine cancels it.
    const QString target = choosePath(QDir(exportDir()).filePath(name));
    if (target.isEmpty()) {
        d->cancel();
        emit exportFinished(QString(), false, tr("cancelled"));
        return;
    }
    const QFileInfo fi(target);
    d->setDownloadDirectory(fi.absolutePath());
    d->setDownloadFileName(fi.fileName());

    QPointer<ViewerWindow> self(this);
    connect(d, &QWebEngineDownloadRequest::isFinishedChanged, this, [self, d, target] {
        if (!self || !d->isFinished()) return;
        const bool ok = d->state() == QWebEngineDownloadRequest::DownloadCompleted;
        const QString why = ok ? QString() : d->interruptReasonString();
        if (ok) {
            self->rememberExportDir(target);
            self->statusBar()->showMessage(tr("Exported %1").arg(QDir::toNativeSeparators(target)), 6000);
        } else {
            self->statusBar()->showMessage(tr("Export failed: %1").arg(why), 8000);
        }
        emit self->exportFinished(target, ok, why);
    });
    d->accept();
}

void ViewerWindow::closeEvent(QCloseEvent *e) {
    QSettings().setValue(QStringLiteral("geometry"), saveGeometry());
    QMainWindow::closeEvent(e);
}

}  // namespace omegamaps
