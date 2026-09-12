// viewer/mapserver.h
//
// The map viewer's server (include/omegamaps/mapview.h) as a C++ object: one
// loopback server, one map loaded at a time, closed with the object.

#ifndef OMEGAMAPS_VIEWER_MAPSERVER_H
#define OMEGAMAPS_VIEWER_MAPSERVER_H

#include <QJsonObject>
#include <QString>
#include <QUrl>

namespace omegamaps {

class MapServer {
public:
    MapServer() = default;
    ~MapServer();
    MapServer(const MapServer &) = delete;
    MapServer &operator=(const MapServer &) = delete;

    // Starts the server with mapPath loaded, or loads mapPath into the
    // running one. On failure the previous map, if any, stays loaded.
    // layoutDir empty means the library's default (~/.omegamaps/layouts).
    bool load(const QString &mapPath, QString *err, const QString &layoutDir = QString());
    void close();

    bool isOpen() const { return m_handle > 0; }

    // The page address with its token: the credential for this server.
    QUrl url() const { return QUrl(m_info.value(QStringLiteral("url")).toString()); }
    int port() const { return url().port(); }
    QString mapPath() const { return m_info.value(QStringLiteral("path")).toString(); }
    QString mapName() const { return m_info.value(QStringLiteral("name")).toString(); }
    int nodeCount() const { return m_info.value(QStringLiteral("nodes")).toInt(); }
    QString layoutPath() const { return m_info.value(QStringLiteral("layout_path")).toString(); }

private:
    bool refresh(QString *err);

    long long m_handle = -1;
    QJsonObject m_info;
};

}  // namespace omegamaps

#endif
