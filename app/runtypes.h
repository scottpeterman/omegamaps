// app/runtypes.h
//
// The run model as the Qt side sees it: parsed from the JSON the C surface
// returns (include/omegamaps/omegamaps.h documents every field), plus the
// finished map read from map.json.

#ifndef OMEGAMAPS_APP_RUNTYPES_H
#define OMEGAMAPS_APP_RUNTYPES_H

#include <QByteArray>
#include <QDateTime>
#include <QHash>
#include <QList>
#include <QPair>
#include <QString>
#include <QVector>

namespace omegamaps {

enum class RowState { Queued, Running, Reached, Failed, NotDialed };

struct RunRow {
    quint64 seq = 0;
    QString identity;  // the crawl's claim key; never changes
    QString name;
    QString display;   // what to show
    int depth = 0;
    QString platform;
    RowState state = RowState::Queued;
    QString via;       // identity of the reporting device; empty for a seed
    QString detail;
    QString descr;
    QString method;
    QString credential;
    int attempts = 0;
    int neighbors = 0;
    int fresh = 0;     // "new" on the wire
    QString phase;
    qint64 phaseMs = 0;
    qint64 durationMs = 0;

    bool dialed() const { return state != RowState::NotDialed; }
};

struct DepthCounts {
    int depth = 0;
    int total = 0;
    int queued = 0;
    int running = 0;
    int reached = 0;
    int failed = 0;
    int notDialed = 0;
};

struct RunCounts {
    int queued = 0;
    int running = 0;
    int reached = 0;
    int failed = 0;
    int notDialed = 0;
    int newHostKeys = 0;
    int attempts = 0;
    int rejections = 0;
};

struct RunProgress {
    quint64 seq = 0;
    int depth = 0;
    qint64 elapsedMs = 0;
    bool finished = false;
    RunCounts counts;
    QVector<DepthCounts> depths;
    QVector<RunRow> running;  // longest in its phase first
};

struct RunDecision {
    quint64 seq = 0;
    QDateTime at;
    QString kind;
    QString identity;
    QString name;
    QString via;
    QString detail;
    QString text;
};

// Parsers for the three pulls. A malformed document yields an empty result
// and false; the caller keeps its previous state.
bool parseProgress(const QByteArray &json, RunProgress *out);
bool parseRows(const QByteArray &json, quint64 *nextSeq, QVector<RunRow> *out);
bool parseDecisions(const QByteArray &json, quint64 *nextSeq, QVector<RunDecision> *out);

QString stateName(RowState s);

// Where a run's output went and how it ended (omegamaps_run_result).
struct RunResult {
    QString kind;   // "replay" or "crawl"; empty before a run
    QString state;  // running, done, cancelled, failed
    QString error;
    QString mapPath;
    QString eventsPath;
    QString logPath;
    int devices = 0;
    int nodes = 0;
};

bool parseResult(const QByteArray &json, RunResult *out);

// map.json: the Secure Cartography / Pathfinder topology format.
struct MapLink {
    QString local;
    QString remote;
};

struct MapPeer {
    QString ip;
    QString platform;
    QVector<MapLink> links;
};

struct MapNode {
    QString ip;
    QString platform;
    QHash<QString, MapPeer> peers;
};

struct TopologyMap {
    QHash<QString, MapNode> nodes;
    bool isEmpty() const { return nodes.isEmpty(); }
};

// Reads map.json. On failure returns false with a reason in *err.
bool loadMap(const QString &path, TopologyMap *out, QString *err);

// "4:26", "1:02:03", "850ms", "3.2s": durations the way the panels show them.
QString formatElapsed(qint64 ms);
QString formatShort(qint64 ms);

}  // namespace omegamaps

#endif
