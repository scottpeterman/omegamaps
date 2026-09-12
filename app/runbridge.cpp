// app/runbridge.cpp

#include "runbridge.h"

#include <QSocketNotifier>
#include <QJsonObject>
#include <QJsonDocument>
#include <QJsonArray>

#include <omegamaps/omegamaps.h>

#ifdef _WIN32
#include <winsock2.h>
#else
#include <unistd.h>
#endif

namespace omegamaps {

namespace {

// Takes ownership of a string from the library.
QByteArray take(char *raw) {
    QByteArray b(raw ? raw : "");
    omegamaps_free(raw);
    return b;
}

QString lastError() { return QString::fromUtf8(take(omegamaps_last_error())); }

// Empties the notifier. Its contents are wakeups, never data.
void drainNotify(long long handle) {
    if (handle < 0) return;
    char scratch[256];
#ifdef _WIN32
    while (::recv(static_cast<SOCKET>(handle), scratch, sizeof(scratch), 0) > 0) {
    }
#else
    while (::read(static_cast<int>(handle), scratch, sizeof(scratch)) > 0) {
    }
#endif
}

}  // namespace

RunBridge::RunBridge(QObject *parent) : QObject(parent) {
    m_tick.setInterval(500);
    connect(&m_tick, &QTimer::timeout, this, &RunBridge::pull);
}

RunBridge::~RunBridge() { close(); }

QString RunBridge::libraryVersion() { return QString::fromUtf8(take(omegamaps_version())); }

bool RunBridge::openReplay(const QString &path, double speed, QString *err) {
    close();
    const QByteArray p = path.toUtf8();
    const long long h = omegamaps_replay_open(p.constData(), speed);
    if (h <= 0) {
        if (err) *err = lastError();
        return false;
    }
    return adopt(h, err);
}

bool RunBridge::openCrawl(const QByteArray &request, long long vault, QString *err) {
    close();
    const long long h = omegamaps_crawl_open(vault, request.constData());
    if (h <= 0) {
        if (err) *err = lastError();
        return false;
    }
    return adopt(h, err);
}

QVector<QPair<QString, QString>> RunBridge::validateCrawl(const QByteArray &request, QString *err) {
    QVector<QPair<QString, QString>> out;
    char *raw = omegamaps_crawl_validate(request.constData());
    if (!raw) {
        if (err) *err = lastError();
        out.append({QStringLiteral("request"), err ? *err : QStringLiteral("unreadable request")});
        return out;
    }
    const QJsonArray arr = QJsonDocument::fromJson(take(raw)).array();
    for (const QJsonValue &v : arr) {
        const QJsonObject o = v.toObject();
        out.append({o.value(QLatin1String("field")).toString(),
                    o.value(QLatin1String("message")).toString()});
    }
    return out;
}

RunResult RunBridge::result() const {
    RunResult r;
    if (m_handle <= 0) return r;
    parseResult(take(omegamaps_run_result(m_handle)), &r);
    return r;
}

// Takes over a run handle the library just opened: watch its notifier and
// start pulling. Shared by every way a run can start.
bool RunBridge::adopt(long long h, QString *err) {
    const long long fd = omegamaps_notify_handle(h);
    if (fd < 0) {
        if (err) *err = lastError();
        omegamaps_close(h);
        return false;
    }

    m_handle = h;
    m_rowSeq = 0;
    m_decisionSeq = 0;
    m_finished = false;
    m_progress = RunProgress{};
    emit started();

    m_notifier = new QSocketNotifier(static_cast<qintptr>(fd), QSocketNotifier::Read, this);
    connect(m_notifier, &QSocketNotifier::activated, this, &RunBridge::pull);
    m_tick.start();
    // Anything delivered before the notifier existed is waiting already, so
    // pull once without a wake -- but from the event loop, not from here. An
    // instant replay can be complete by now, and a pull made inside the open
    // would emit finished() before the caller got back control to connect to
    // it: the probe hung on exactly that, one run in twelve.
    QMetaObject::invokeMethod(this, &RunBridge::pull, Qt::QueuedConnection);
    return true;
}

void RunBridge::cancel() {
    if (m_handle > 0) omegamaps_cancel(m_handle);
}

void RunBridge::close() {
    m_tick.stop();
    if (m_notifier) {
        m_notifier->setEnabled(false);
        delete m_notifier;
        m_notifier = nullptr;
    }
    if (m_handle > 0) {
        omegamaps_close(m_handle);
        m_handle = -1;
    }
}

void RunBridge::pull() {
    const long long h = m_handle;
    if (h <= 0 || m_finished) return;
    drainNotify(omegamaps_notify_handle(h));

    RunProgress p;
    if (!parseProgress(take(omegamaps_progress(h)), &p)) return;

    // A slot may close this run or open another while a signal is being
    // delivered; after each emit, stop if the handle is no longer ours.
    const auto stillOurs = [this, h] { return m_handle == h; };

    QVector<RunRow> rows;
    quint64 next = m_rowSeq;
    if (parseRows(take(omegamaps_rows_since(h, m_rowSeq)), &next, &rows)) {
        m_rowSeq = next;
        if (!rows.isEmpty()) emit rowsChanged(rows);
        if (!stillOurs()) return;
    }

    QVector<RunDecision> decisions;
    next = m_decisionSeq;
    if (parseDecisions(take(omegamaps_decisions_since(h, m_decisionSeq)), &next, &decisions)) {
        m_decisionSeq = next;
        if (!decisions.isEmpty()) emit decisionsAdded(decisions);
        if (!stillOurs()) return;
    }

    m_progress = p;
    emit progressChanged(p);
    if (!stillOurs()) return;

    if (p.finished) {
        m_finished = true;
        m_tick.stop();
        if (m_notifier) m_notifier->setEnabled(false);
        emit finished(p);
    }
}

}  // namespace omegamaps
