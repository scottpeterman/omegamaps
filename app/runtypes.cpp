// app/runtypes.cpp

#include "runtypes.h"

#include <QFile>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QJsonParseError>

namespace omegamaps {

namespace {

RowState parseState(const QString &s) {
    if (s == QLatin1String("running")) return RowState::Running;
    if (s == QLatin1String("reached")) return RowState::Reached;
    if (s == QLatin1String("failed")) return RowState::Failed;
    if (s == QLatin1String("not dialed")) return RowState::NotDialed;
    return RowState::Queued;
}

quint64 u64(const QJsonValue &v) { return static_cast<quint64>(v.toDouble()); }
qint64 i64(const QJsonValue &v) { return static_cast<qint64>(v.toDouble()); }

RunRow parseRow(const QJsonObject &o) {
    RunRow r;
    r.seq = u64(o.value(QLatin1String("seq")));
    r.identity = o.value(QLatin1String("identity")).toString();
    r.name = o.value(QLatin1String("name")).toString();
    r.display = o.value(QLatin1String("display")).toString();
    if (r.display.isEmpty()) r.display = r.name.isEmpty() ? r.identity : r.name;
    r.depth = o.value(QLatin1String("depth")).toInt();
    r.platform = o.value(QLatin1String("platform")).toString();
    r.state = parseState(o.value(QLatin1String("state")).toString());
    r.via = o.value(QLatin1String("via")).toString();
    r.detail = o.value(QLatin1String("detail")).toString();
    r.descr = o.value(QLatin1String("descr")).toString();
    r.method = o.value(QLatin1String("method")).toString();
    r.credential = o.value(QLatin1String("credential")).toString();
    r.attempts = o.value(QLatin1String("attempts")).toInt();
    r.neighbors = o.value(QLatin1String("neighbors")).toInt();
    r.fresh = o.value(QLatin1String("new")).toInt();
    r.phase = o.value(QLatin1String("phase")).toString();
    r.phaseMs = i64(o.value(QLatin1String("phase_ms")));
    r.durationMs = i64(o.value(QLatin1String("duration_ms")));
    return r;
}

bool object(const QByteArray &json, QJsonObject *out) {
    QJsonParseError err{};
    const QJsonDocument doc = QJsonDocument::fromJson(json, &err);
    if (err.error != QJsonParseError::NoError || !doc.isObject()) return false;
    *out = doc.object();
    return true;
}

}  // namespace

QString stateName(RowState s) {
    switch (s) {
    case RowState::Queued: return QStringLiteral("queued");
    case RowState::Running: return QStringLiteral("running");
    case RowState::Reached: return QStringLiteral("reached");
    case RowState::Failed: return QStringLiteral("failed");
    case RowState::NotDialed: return QStringLiteral("not dialed");
    }
    return QString();
}

bool parseProgress(const QByteArray &json, RunProgress *out) {
    QJsonObject o;
    if (!object(json, &o)) return false;
    RunProgress p;
    p.seq = u64(o.value(QLatin1String("seq")));
    p.depth = o.value(QLatin1String("depth")).toInt();
    p.elapsedMs = i64(o.value(QLatin1String("elapsed_ms")));
    p.finished = o.value(QLatin1String("finished")).toBool();

    const QJsonObject c = o.value(QLatin1String("counts")).toObject();
    p.counts.queued = c.value(QLatin1String("queued")).toInt();
    p.counts.running = c.value(QLatin1String("running")).toInt();
    p.counts.reached = c.value(QLatin1String("reached")).toInt();
    p.counts.failed = c.value(QLatin1String("failed")).toInt();
    p.counts.notDialed = c.value(QLatin1String("not_dialed")).toInt();
    p.counts.newHostKeys = c.value(QLatin1String("new_host_keys")).toInt();
    p.counts.attempts = c.value(QLatin1String("attempts")).toInt();
    p.counts.rejections = c.value(QLatin1String("rejections")).toInt();

    for (const QJsonValue &v : o.value(QLatin1String("depths")).toArray()) {
        const QJsonObject d = v.toObject();
        DepthCounts dc;
        dc.depth = d.value(QLatin1String("depth")).toInt();
        dc.total = d.value(QLatin1String("total")).toInt();
        dc.queued = d.value(QLatin1String("queued")).toInt();
        dc.running = d.value(QLatin1String("running")).toInt();
        dc.reached = d.value(QLatin1String("reached")).toInt();
        dc.failed = d.value(QLatin1String("failed")).toInt();
        dc.notDialed = d.value(QLatin1String("not_dialed")).toInt();
        p.depths.append(dc);
    }
    for (const QJsonValue &v : o.value(QLatin1String("running")).toArray())
        p.running.append(parseRow(v.toObject()));
    *out = p;
    return true;
}

bool parseRows(const QByteArray &json, quint64 *nextSeq, QVector<RunRow> *out) {
    QJsonObject o;
    if (!object(json, &o)) return false;
    *nextSeq = u64(o.value(QLatin1String("seq")));
    out->clear();
    for (const QJsonValue &v : o.value(QLatin1String("rows")).toArray())
        out->append(parseRow(v.toObject()));
    return true;
}

bool parseDecisions(const QByteArray &json, quint64 *nextSeq, QVector<RunDecision> *out) {
    QJsonObject o;
    if (!object(json, &o)) return false;
    *nextSeq = u64(o.value(QLatin1String("seq")));
    out->clear();
    for (const QJsonValue &v : o.value(QLatin1String("decisions")).toArray()) {
        const QJsonObject d = v.toObject();
        RunDecision rd;
        rd.seq = u64(d.value(QLatin1String("seq")));
        rd.at = QDateTime::fromMSecsSinceEpoch(i64(d.value(QLatin1String("at_ms"))));
        rd.kind = d.value(QLatin1String("kind")).toString();
        rd.identity = d.value(QLatin1String("identity")).toString();
        rd.name = d.value(QLatin1String("name")).toString();
        rd.via = d.value(QLatin1String("via")).toString();
        rd.detail = d.value(QLatin1String("detail")).toString();
        rd.text = d.value(QLatin1String("text")).toString();
        out->append(rd);
    }
    return true;
}

bool parseResult(const QByteArray &json, RunResult *out) {
    QJsonObject o;
    if (!object(json, &o)) return false;
    RunResult r;
    r.kind = o.value(QLatin1String("kind")).toString();
    r.state = o.value(QLatin1String("state")).toString();
    r.error = o.value(QLatin1String("error")).toString();
    r.mapPath = o.value(QLatin1String("map_path")).toString();
    r.eventsPath = o.value(QLatin1String("events_path")).toString();
    r.logPath = o.value(QLatin1String("log_path")).toString();
    r.devices = o.value(QLatin1String("devices")).toInt();
    r.nodes = o.value(QLatin1String("nodes")).toInt();
    *out = r;
    return true;
}

bool loadMap(const QString &path, TopologyMap *out, QString *err) {
    QFile f(path);
    if (!f.open(QIODevice::ReadOnly)) {
        if (err) *err = f.errorString();
        return false;
    }
    QJsonParseError pe{};
    const QJsonDocument doc = QJsonDocument::fromJson(f.readAll(), &pe);
    if (pe.error != QJsonParseError::NoError || !doc.isObject()) {
        if (err) *err = pe.error != QJsonParseError::NoError
                            ? pe.errorString()
                            : QStringLiteral("not a JSON object");
        return false;
    }

    TopologyMap m;
    const QJsonObject root = doc.object();
    for (auto it = root.begin(); it != root.end(); ++it) {
        const QJsonObject node = it.value().toObject();
        MapNode mn;
        const QJsonObject details = node.value(QLatin1String("node_details")).toObject();
        mn.ip = details.value(QLatin1String("ip")).toString();
        mn.platform = details.value(QLatin1String("platform")).toString();
        const QJsonObject peers = node.value(QLatin1String("peers")).toObject();
        for (auto pit = peers.begin(); pit != peers.end(); ++pit) {
            const QJsonObject po = pit.value().toObject();
            MapPeer peer;
            peer.ip = po.value(QLatin1String("ip")).toString();
            peer.platform = po.value(QLatin1String("platform")).toString();
            for (const QJsonValue &c : po.value(QLatin1String("connections")).toArray()) {
                const QJsonArray pair = c.toArray();
                if (pair.size() >= 2)
                    peer.links.append({pair.at(0).toString(), pair.at(1).toString()});
            }
            mn.peers.insert(pit.key(), peer);
        }
        m.nodes.insert(it.key(), mn);
    }
    *out = m;
    return true;
}

QString formatElapsed(qint64 ms) {
    const qint64 s = ms / 1000;
    const qint64 h = s / 3600, m = (s % 3600) / 60, sec = s % 60;
    if (h > 0)
        return QStringLiteral("%1:%2:%3").arg(h).arg(m, 2, 10, QLatin1Char('0'))
                                          .arg(sec, 2, 10, QLatin1Char('0'));
    return QStringLiteral("%1:%2").arg(m).arg(sec, 2, 10, QLatin1Char('0'));
}

QString formatShort(qint64 ms) {
    if (ms < 1000) return QStringLiteral("%1ms").arg(ms);
    if (ms < 60000) return QStringLiteral("%1s").arg(ms / 1000.0, 0, 'f', 1);
    return formatElapsed(ms);
}

}  // namespace omegamaps
