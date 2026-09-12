// app/icons.h
//
// Device icons for the preview: the same eight drawings mapview uses, picked
// by the same rules, so a device looks the same in the application and in
// the browser viewer.
//
// The rules are a port of TopologyViewer._resolveIconKey in
// internal/mapweb/assets/viewer.js, reading the same platform_map.json (built
// into the resources straight from internal/mapweb/assets, not copied). A
// change to how icons are chosen is made in both places.

#ifndef OMEGAMAPS_APP_ICONS_H
#define OMEGAMAPS_APP_ICONS_H

#include <QColor>
#include <QHash>
#include <QPixmap>
#include <QString>
#include <QStringList>
#include <QVector>

namespace omegamaps {

class IconLibrary {
public:
    static IconLibrary &instance();

    // "router", "layer-3-switch", ... ; "undiscovered" for no platform.
    QString keyFor(const QString &platform, const QString &name) const;

    // The icon rendered at px x px device pixels, cached.
    QPixmap pixmap(const QString &key, int px) const;

    // The vendor ring colour mapview draws around a device.
    QColor vendorColor(const QString &platform, const QString &name) const;

private:
    IconLibrary();

    struct Fallback {
        QStringList platformPatterns;
        QStringList namePatterns;
        QString key;
    };
    QVector<QPair<QString, QString>> m_patterns;  // longest pattern first
    QVector<Fallback> m_fallbacks;                // file order
    mutable QHash<QString, QPixmap> m_cache;
};

}  // namespace omegamaps

#endif
