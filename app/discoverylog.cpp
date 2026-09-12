// app/discoverylog.cpp

#include "discoverylog.h"

#include <QEvent>
#include <QFontDatabase>
#include <QHBoxLayout>
#include <QPlainTextEdit>
#include <QPushButton>
#include <QScrollBar>
#include <QTextCursor>
#include <QTextBlock>
#include <QTimer>
#include <QVBoxLayout>

#include <algorithm>

#include "theme.h"

namespace omegamaps {

namespace {
// Beyond this the oldest entries go; the run model still has everything.
constexpr int kMaxEntries = 20000;
}

DiscoveryLog::DiscoveryLog(QWidget *parent) : Panel(QStringLiteral("Discovery log"), parent) {
    m_notDialedBtn = new QPushButton(this);
    m_notDialedBtn->setCheckable(true);
    m_notDialedBtn->setCursor(Qt::PointingHandCursor);
    m_notDialedBtn->setToolTip(tr("Show each device the crawl deliberately did not dial"));
    barLayout()->addWidget(m_notDialedBtn);
    connect(m_notDialedBtn, &QPushButton::toggled, this, [this] { rerender(); });

    m_view = new QPlainTextEdit(this);
    m_view->setProperty("role", QStringLiteral("log"));
    m_view->setReadOnly(true);
    m_view->setLineWrapMode(QPlainTextEdit::NoWrap);
    m_view->setMaximumBlockCount(kMaxEntries);
    QFont mono = QFontDatabase::systemFont(QFontDatabase::FixedFont);
    mono.setPointSize(9);
    m_view->setFont(mono);
    m_view->setMinimumHeight(100);
    bodyLayout()->setContentsMargins(10, 10, 10, 10);
    bodyLayout()->addWidget(m_view, 1);

    // Only the reader's own scrolling decides whether the log follows the
    // end: actionTriggered fires for wheel, drag and the arrows, and never for
    // a programmatic scroll. sliderPosition is where the action is taking the
    // bar; value() still holds where it was.
    // A resize keeps the top line where it was, so a log following the end
    // stops showing it whenever the column around it re-lays out -- and the
    // run column does, every time the progress panel gains a depth. Following
    // is re-applied after any resize, not only after an append.
    m_view->viewport()->installEventFilter(this);

    QScrollBar *vbar = m_view->verticalScrollBar();
    connect(vbar, &QScrollBar::actionTriggered, this, [this, vbar](int) {
        m_follow = vbar->sliderPosition() >= vbar->maximum() - 4;
    });

    connect(&ThemeManager::instance(), &ThemeManager::changed, this, [this] { rerender(); });
    updateToggle();
}

void DiscoveryLog::reset() {
    m_entries.clear();
    m_lastState.clear();
    m_notDialedReasons.clear();
    m_lastDepth = -1;
    m_notDialedIds.clear();
    m_view->clear();
    m_follow = true;
    updateToggle();
}

void DiscoveryLog::updateToggle() {
    // Devices, not lines: a server behind two switches is reported twice and
    // listed twice, but it is one device, and the count should match the
    // progress panel's.
    m_notDialedBtn->setText(tr("Not dialed (%1)").arg(m_notDialedIds.size()));
}

bool DiscoveryLog::visible(const Entry &e) const {
    return !e.notDialed || m_notDialedBtn->isChecked();
}

int DiscoveryLog::visibleCount() const {
    return int(std::count_if(m_entries.begin(), m_entries.end(),
                             [this](const Entry &e) { return visible(e); }));
}

namespace {
QString markFor(DiscoveryLog::Level level) {
    switch (level) {
    case DiscoveryLog::Level::Success: return QStringLiteral("\u2713 ");
    case DiscoveryLog::Level::Error: return QStringLiteral("\u2717 ");
    case DiscoveryLog::Level::Warning: return QStringLiteral("! ");
    case DiscoveryLog::Level::Detail: return QStringLiteral("\u00B7 ");
    default: return QString();
    }
}
}  // namespace

QString DiscoveryLog::plainText() const {
    QStringList lines;
    for (const Entry &e : m_entries) {
        if (!visible(e)) continue;
        lines << QStringLiteral("[%1] %2%3").arg(e.at.toString(QStringLiteral("HH:mm:ss")),
                                                 markFor(e.level), e.text);
    }
    return lines.join(QLatin1Char('\n'));
}

int DiscoveryLog::notDialedDevices() const { return m_notDialedIds.size(); }

QStringList DiscoveryLog::resultIdentities() const {
    QStringList out;
    for (const Entry &e : m_entries)
        if (!e.result.isEmpty()) out << e.result;
    return out;
}

QString DiscoveryLog::render(const Entry &e) const {
    const Tokens &t = ThemeManager::instance().tokens();
    QColor color = t.textPrimary;
    const QString mark = markFor(e.level);
    switch (e.level) {
    case Level::Success: color = t.success; break;
    case Level::Error: color = t.danger; break;
    case Level::Warning: color = t.warning; break;
    case Level::Detail: color = t.textMuted; break;
    case Level::Heading: color = t.accent; break;
    case Level::Info: color = t.textSecondary; break;
    }
    const QString stamp = QStringLiteral("<span style=\"color:%1\">[%2]</span> ")
                              .arg(t.textMuted.name(), e.at.toString(QStringLiteral("HH:mm:ss")));
    QString body = (mark + e.text).toHtmlEscaped();
    if (e.level == Level::Heading) body = QStringLiteral("<b>%1</b>").arg(body);
    return stamp + QStringLiteral("<span style=\"color:%1\">%2</span>").arg(color.name(), body);
}

void DiscoveryLog::append(const QDateTime &at, Level level, const QString &text, bool notDialed,
                          const QString &result) {
    if (text.trimmed().isEmpty()) return;  // sc2 logged bare timestamps; never here
    Entry e{at.isValid() ? at : QDateTime::currentDateTime(), level, text, notDialed, result};
    m_entries.append(e);
    if (m_entries.size() > kMaxEntries) m_entries.remove(0, m_entries.size() - kMaxEntries);
    if (!visible(e)) return;

    const int keepValue = m_view->verticalScrollBar()->value();
    m_view->appendHtml(render(e));
    scrollAfterAppend(keepValue);
}

void DiscoveryLog::rerender() {
    const int keepValue = m_view->verticalScrollBar()->value();
    m_view->setUpdatesEnabled(false);
    m_view->clear();
    // One edit block through a cursor, one block per entry: appendHtml per
    // line is what makes a large re-render take seconds, and joining lines
    // into one block would defeat the view's block limit.
    QTextCursor cur(m_view->document());
    cur.beginEditBlock();
    bool first = true;
    for (const Entry &e : std::as_const(m_entries)) {
        if (!visible(e)) continue;
        if (!first) cur.insertBlock();
        cur.insertHtml(render(e));
        first = false;
    }
    cur.endEditBlock();
    m_view->setUpdatesEnabled(true);
    scrollAfterAppend(keepValue);
}

// Follows the end unless the reader has scrolled away from it. Whether to
// follow is a flag the reader's own scrolling sets (see the constructor), not
// something inferred from the scroll bar on each append: after an insert the
// bar's maximum is stale until the document is laid out, so "am I at the
// bottom?" asked at that moment answers no, and a log that asks it stops
// following in the middle of a busy depth -- which is what the first version
// did. Moving the cursor rather than setting the bar, for the same reason.
void DiscoveryLog::scrollAfterAppend(int keepValue) {
    if (m_follow) {
        m_view->moveCursor(QTextCursor::End);
        m_view->ensureCursorVisible();
        // With wrapping off, the end of the last line can be far right;
        // following the log means the newest line, read from its start.
        m_view->horizontalScrollBar()->setValue(0);
    } else {
        m_view->verticalScrollBar()->setValue(keepValue);
    }
}

// Asked of what is on screen, not of the scroll bar: QPlainTextEdit's range
// is in blocks and is only exact once the last page has been laid out, so
// after a re-render the bar can report a maximum 25 lines past a view that is
// already showing the end.
bool DiscoveryLog::eventFilter(QObject *watched, QEvent *event) {
    if (watched == m_view->viewport() && event->type() == QEvent::Resize && m_follow) {
        // After the resize has been applied, not during it.
        QTimer::singleShot(0, this, [this] {
            if (m_follow) scrollAfterAppend(m_view->verticalScrollBar()->value());
        });
    }
    return Panel::eventFilter(watched, event);
}

bool DiscoveryLog::showsNewest() const {
    const QWidget *vp = m_view->viewport();
    const QTextCursor bottom = m_view->cursorForPosition(QPoint(4, vp->height() - 4));
    return bottom.blockNumber() >= m_view->document()->lastBlock().blockNumber();
}

void DiscoveryLog::info(const QString &text) {
    append(QDateTime::currentDateTime(), Level::Info, text);
}

void DiscoveryLog::error(const QString &text) {
    append(QDateTime::currentDateTime(), Level::Error, text);
}

void DiscoveryLog::addRows(const QVector<RunRow> &rows) {
    const QDateTime now = QDateTime::currentDateTime();
    for (const RunRow &r : rows) {
        const auto prev = m_lastState.constFind(r.identity);
        const bool changed = prev == m_lastState.constEnd() || *prev != r.state;
        m_lastState.insert(r.identity, r.state);
        if (!changed) continue;

        if (r.state == RowState::Reached) {
            QString text = r.display;
            if (!r.method.isEmpty()) text += QStringLiteral(" via ") + r.method;
            text += QStringLiteral(" (%1 neighbors, %2)")
                        .arg(r.neighbors)
                        .arg(formatShort(r.durationMs));
            if (r.attempts > 1) text += tr(" after %1 credentials").arg(r.attempts);
            append(now, Level::Success, text, false, r.identity);
        } else if (r.state == RowState::Failed) {
            append(now, Level::Error, QStringLiteral("%1 failed: %2").arg(r.display, r.detail), false,
                   r.identity);
        }
    }
}

void DiscoveryLog::addDecisions(const QVector<RunDecision> &decisions) {
    for (const RunDecision &d : decisions) {
        // Failures are logged from rows, which also see the ones no event
        // describes.
        if (d.kind == QLatin1String("failed")) continue;

        if (d.kind == QLatin1String("not-dialed")) {
            m_notDialedReasons[d.detail.isEmpty() ? tr("no reason given") : d.detail]
                .insert(d.identity);
            m_notDialedIds.insert(d.identity);
            append(d.at, Level::Detail, d.text, true);
            updateToggle();
            continue;
        }

        Level level = Level::Info;
        if (d.kind == QLatin1String("fallback") || d.kind == QLatin1String("auth-reject") ||
            d.kind == QLatin1String("retry-addr"))
            level = Level::Warning;
        else if (d.kind == QLatin1String("cred-parked") || d.kind == QLatin1String("collect-err"))
            level = Level::Error;
        else if (d.kind == QLatin1String("resolved") || d.kind == QLatin1String("renamed") ||
                 d.kind == QLatin1String("host-key-new") || d.kind == QLatin1String("auth-ok"))
            level = Level::Detail;
        append(d.at, level, d.text);
    }
}

void DiscoveryLog::noteProgress(const RunProgress &p) {
    if (p.finished) return;
    // Depth batches in order; a pull can skip past a short one entirely. A
    // depth is announced once its counts exist -- the first pull can come
    // before the run has admitted anything, and "0 devices" would be wrong.
    for (int d = m_lastDepth + 1; d <= p.depth; ++d) {
        const DepthCounts *dc = nullptr;
        for (const DepthCounts &x : p.depths)
            if (x.depth == d) dc = &x;
        if (!dc || dc->total == 0) break;
        append(QDateTime::currentDateTime(), Level::Heading,
               tr("\u2500\u2500 depth %1: %2 devices to collect")
                   .arg(d)
                   .arg(dc->total - dc->notDialed));
        m_lastDepth = d;
    }
}

void DiscoveryLog::noteFinished(const RunProgress &p) {
    const RunCounts &c = p.counts;
    append(QDateTime::currentDateTime(), Level::Heading,
           tr("Complete in %1: %2 reached, %3 failed, %4 not dialed")
               .arg(formatElapsed(p.elapsedMs))
               .arg(c.reached)
               .arg(c.failed)
               .arg(c.notDialed));

    // Why devices were left alone, largest reason first, counting each
    // device once however many neighbours reported it.
    QVector<QPair<int, QString>> reasons;
    for (auto it = m_notDialedReasons.constBegin(); it != m_notDialedReasons.constEnd(); ++it)
        reasons.append({int(it->size()), it.key()});
    std::sort(reasons.begin(), reasons.end(),
              [](const auto &a, const auto &b) { return a.first > b.first; });
    for (const auto &[n, why] : reasons)
        append(QDateTime::currentDateTime(), Level::Info,
               tr("  not dialed: %1 \u00D7 %2").arg(n).arg(why));
}

}  // namespace omegamaps
