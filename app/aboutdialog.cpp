// app/aboutdialog.cpp

#include "aboutdialog.h"

#include <QClipboard>
#include <QDialogButtonBox>
#include <QEvent>
#include <QGuiApplication>
#include <QLabel>
#include <QMouseEvent>
#include <QPointer>
#include <QPushButton>
#include <QScreen>
#include <QSysInfo>
#include <QVBoxLayout>

#include "runbridge.h"
#include "theme.h"

namespace omegamaps {

namespace {

// The splash's width on screen, in logical pixels: half the image, so a 2x
// display shows every pixel of it and a 1x display a smooth reduction.
constexpr int kSplashWidth = 784;

QPointer<AboutDialog> g_open;

class ClickToAbout : public QObject {
public:
    using QObject::QObject;

protected:
    bool eventFilter(QObject *watched, QEvent *e) override {
        if (e->type() == QEvent::MouseButtonRelease &&
            static_cast<QMouseEvent *>(e)->button() == Qt::LeftButton) {
            AboutDialog::showFor(static_cast<QWidget *>(watched)->window());
            return true;
        }
        return QObject::eventFilter(watched, e);
    }
};

}  // namespace

AboutDialog::AboutDialog(QWidget *parent) : QDialog(parent) {
    setWindowTitle(tr("About omegamaps"));
    setAttribute(Qt::WA_DeleteOnClose);

    auto *col = new QVBoxLayout(this);
    col->setContentsMargins(0, 0, 0, 16);
    col->setSpacing(10);

    // Scaled once, smoothly, to the pixels the screen will show; a pixmap
    // left for QLabel to shrink is drawn without smoothing.
    initResources();
    const QPixmap src(QStringLiteral(":/omegamaps/splash.png"));
    const qreal dpr = parent ? parent->devicePixelRatioF()
                             : (QGuiApplication::primaryScreen() ? QGuiApplication::primaryScreen()->devicePixelRatio() : 1.0);
    m_splash = new QLabel(this);
    if (!src.isNull()) {
        QPixmap pm = src.scaledToWidth(qRound(kSplashWidth * dpr), Qt::SmoothTransformation);
        pm.setDevicePixelRatio(dpr);
        m_splash->setPixmap(pm);
    }
    m_splash->setFixedWidth(kSplashWidth);
    m_splash->setAlignment(Qt::AlignCenter);
    col->addWidget(m_splash);

    auto *text = new QVBoxLayout;
    text->setContentsMargins(24, 4, 24, 0);
    text->setSpacing(6);

    auto *title = new QLabel(QStringLiteral("<b>omegamaps</b>&nbsp;&nbsp;%1").arg(RunBridge::libraryVersion().toHtmlEscaped()), this);
    title->setTextInteractionFlags(Qt::TextSelectableByMouse);
    text->addWidget(title);

    auto *what = new QLabel(tr("Network discovery and topology mapping: SSH and SNMP crawls, "
                               "CDP and LLDP neighbors, maps you can view, export and share."),
                            this);
    what->setWordWrap(true);
    text->addWidget(what);

    m_details = tr("omegamaps %1\nQt %2 (built with %3)\n%4, %5")
                    .arg(RunBridge::libraryVersion(), QString::fromLatin1(qVersion()), QStringLiteral(QT_VERSION_STR),
                         QSysInfo::prettyProductName(), QSysInfo::currentCpuArchitecture());
    // On screen without the first line, which the title already says; the
    // copied text keeps it, since it goes where there is no title.
    auto *details = new QLabel(m_details.section(QLatin1Char('\n'), 1), this);
    details->setProperty("tone", QStringLiteral("muted"));
    details->setTextInteractionFlags(Qt::TextSelectableByMouse);
    text->addWidget(details);

    auto *lineage = new QLabel(tr("The crawl engine, vault and map format come from PathfinderSSH; the SNMP "
                                  "collectors from Secure Cartography. Built the Omega way: Go, C, C++ and Qt."),
                               this);
    lineage->setWordWrap(true);
    lineage->setProperty("tone", QStringLiteral("secondary"));
    text->addWidget(lineage);

    auto *link = new QLabel(QStringLiteral("<a href=\"https://github.com/scottpeterman/omegamaps\">"
                                           "github.com/scottpeterman/omegamaps</a>"),
                            this);
    link->setOpenExternalLinks(true);
    text->addWidget(link);

    auto *buttons = new QDialogButtonBox(this);
    QPushButton *copy = buttons->addButton(tr("Copy details"), QDialogButtonBox::ActionRole);
    QPushButton *close = buttons->addButton(QDialogButtonBox::Close);
    close->setDefault(true);
    setStyleProperty(close, "primary", QStringLiteral("true"));
    connect(copy, &QPushButton::clicked, this, [this] { QGuiApplication::clipboard()->setText(m_details); });
    connect(buttons, &QDialogButtonBox::rejected, this, &QDialog::close);
    text->addSpacing(4);
    text->addWidget(buttons);

    col->addLayout(text);
    setFixedWidth(kSplashWidth);
}

AboutDialog *AboutDialog::showFor(QWidget *parent) {
    if (!g_open) g_open = new AboutDialog(parent);
    g_open->show();
    g_open->raise();
    g_open->activateWindow();
    return g_open;
}

void AboutDialog::makeTrigger(QWidget *w) {
    w->setCursor(Qt::PointingHandCursor);
    w->setToolTip(tr("About omegamaps"));
    w->installEventFilter(new ClickToAbout(w));
}

}  // namespace omegamaps
