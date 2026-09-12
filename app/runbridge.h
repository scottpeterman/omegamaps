// app/runbridge.h
//
// One run, seen from the Qt thread. Owns the handle from the C surface,
// watches its notifier, and turns each wake into one pull: progress first,
// then the rows and decisions that changed since the last pull (see the
// DELIVERY MODEL in include/omegamaps/omegamaps.h for why that order).
//
// Everything this emits is emitted on the thread that owns the object. The
// Go side never calls in; it only makes the notifier readable.

#ifndef OMEGAMAPS_APP_RUNBRIDGE_H
#define OMEGAMAPS_APP_RUNBRIDGE_H

#include <QObject>
#include <QString>
#include <QTimer>

#include "runtypes.h"

class QSocketNotifier;

namespace omegamaps {

class RunBridge : public QObject {
    Q_OBJECT
public:
    explicit RunBridge(QObject *parent = nullptr);
    ~RunBridge() override;

    // Plays a recorded stream (crawl -events). speed divides the recorded
    // gaps; 0 delivers everything at once. Closes any run already open.
    bool openReplay(const QString &path, double speed, QString *err);

    // Starts a live crawl. request is the JSON omegamaps_crawl_open takes;
    // vault is an unlocked Vault's handle(), or 0. Closes any run already open.
    // Everything after this -- the signals, cancel, close -- is the same as
    // for a replay.
    bool openCrawl(const QByteArray &request, long long vault, QString *err);

    // Field-level problems with a crawl request, empty when it is good. For a
    // form marking bad fields; openCrawl reports the same things as one error.
    static QVector<QPair<QString, QString>> validateCrawl(const QByteArray &request,
                                                          QString *err = nullptr);

    // Where the run's output went and how it ended. Meaningful once finished().
    RunResult result() const;

    void cancel();
    void close();

    bool isOpen() const { return m_handle > 0; }
    bool isFinished() const { return m_finished; }
    const RunProgress &progress() const { return m_progress; }

    // The library version, for the window title and bug reports.
    static QString libraryVersion();

signals:
    // A new run has started; views reset.
    void started();
    void progressChanged(const omegamaps::RunProgress &p);
    void rowsChanged(const QVector<omegamaps::RunRow> &rows);
    void decisionsAdded(const QVector<omegamaps::RunDecision> &decisions);
    // After the last rows and decisions of the run have been emitted.
    void finished(const omegamaps::RunProgress &p);

private:
    bool adopt(long long h, QString *err);
    void pull();

    long long m_handle = -1;
    QSocketNotifier *m_notifier = nullptr;
    QTimer m_tick;  // phase times move without events; see the header
    quint64 m_rowSeq = 0;
    quint64 m_decisionSeq = 0;
    bool m_finished = false;
    RunProgress m_progress;
};

}  // namespace omegamaps

#endif
