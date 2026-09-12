// viewer/mapserver.cpp

#include "mapserver.h"

#include <QJsonDocument>

#include <omegamaps/mapview.h>

namespace omegamaps {

namespace {

QString lastError() {
    char *e = omegamaps_last_error();
    const QString s = QString::fromUtf8(e);
    omegamaps_free(e);
    return s;
}

}  // namespace

MapServer::~MapServer() { close(); }

bool MapServer::load(const QString &mapPath, QString *err, const QString &layoutDir) {
    const QByteArray path = mapPath.toUtf8();
    if (m_handle <= 0) {
        const QByteArray dir = layoutDir.toUtf8();
        const long long h = omegamaps_mapview_open(path.constData(), layoutDir.isEmpty() ? nullptr : dir.constData());
        if (h < 0) {
            if (err) *err = lastError();
            return false;
        }
        m_handle = h;
    } else if (omegamaps_mapview_load(m_handle, path.constData()) != 0) {
        if (err) *err = lastError();
        return false;
    }
    return refresh(err);
}

bool MapServer::refresh(QString *err) {
    char *info = omegamaps_mapview_info(m_handle);
    if (!info) {
        if (err) *err = lastError();
        return false;
    }
    m_info = QJsonDocument::fromJson(QByteArray(info)).object();
    omegamaps_free(info);
    return true;
}

void MapServer::close() {
    if (m_handle > 0) omegamaps_mapview_close(m_handle);
    m_handle = -1;
    m_info = QJsonObject();
}

}  // namespace omegamaps
