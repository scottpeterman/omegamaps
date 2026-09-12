// app/discoverylog.h
//
// What the crawl did and decided, as a timestamped log in the sc2 style.
//
// Two sources, so nothing is said twice: results come from rows (a device
// becoming reached or failed -- including devices a cancel ended, which no
// event describes), and everything else from the run's decisions: fallbacks,
// retries, resolutions, rejections, devices left undialed.
//
// Not-dialed decisions are kept but hidden by default. A crawl through
// switches that report their attached hosts produces one per excluded host,
// and shown inline they bury the results. The toggle in the title
// bar shows them, and the end of the run says how many there were and why.
//
// Colours are applied when lines are rendered, not stored in them, so a theme
// change or a filter change re-renders from the entries.

#ifndef OMEGAMAPS_APP_DISCOVERYLOG_H
#define OMEGAMAPS_APP_DISCOVERYLOG_H

#include <QDateTime>
#include <QHash>
#include <QSet>
#include <QVector>

#include "panel.h"
#include "runtypes.h"

class QPlainTextEdit;
class QPushButton;

namespace omegamaps {

class DiscoveryLog : public Panel {
    Q_OBJECT
public:
    enum class Level { Info, Success, Warning, Error, Detail, Heading };

    explicit DiscoveryLog(QWidget *parent = nullptr);

    void reset();
    void addRows(const QVector<RunRow> &rows);
    void addDecisions(const QVector<RunDecision> &decisions);
    void noteProgress(const RunProgress &p);
    void noteFinished(const RunProgress &p);
    void info(const QString &text);
    void error(const QString &text);

    int entryCount() const { return m_entries.size(); }
    int visibleCount() const;
    QString plainText() const;

    // True when the newest line is in view, which it should be unless the
    // reader scrolled away.
    bool showsNewest() const;

protected:
    bool eventFilter(QObject *watched, QEvent *event) override;

public:

    // Identities with a reached/failed line, one entry per line. Keyed on the
    // claim identity, not the name: two devices can report the same name.
    QStringList resultIdentities() const;

    // Distinct devices the log has a not-dialed decision for.
    int notDialedDevices() const;

private:
    struct Entry {
        QDateTime at;
        Level level;
        QString text;
        bool notDialed = false;
        QString result;  // identity, on a reached/failed line from a row
    };

    void append(const QDateTime &at, Level level, const QString &text, bool notDialed = false,
                const QString &result = QString());
    bool visible(const Entry &e) const;
    QString render(const Entry &e) const;
    void rerender();
    void updateToggle();
    void scrollAfterAppend(int keepValue);

    QPlainTextEdit *m_view = nullptr;
    bool m_follow = true;  // set by the reader's scrolling, nothing else
    QPushButton *m_notDialedBtn = nullptr;
    QVector<Entry> m_entries;
    QHash<QString, RowState> m_lastState;             // by identity
    QHash<QString, QSet<QString>> m_notDialedReasons; // reason -> identities
    int m_lastDepth = -1;
    QSet<QString> m_notDialedIds;
};

}  // namespace omegamaps

#endif
