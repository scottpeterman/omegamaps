// app/icons.h
//
// Device icons for the preview, in two sets, picked by the same rules the
// browser viewer uses so a device looks the same in the application and in
// the map viewer:
//
//   Cisco  the 22 Cisco topology-icon images carried over from
//          securecartography2, chosen through platform_icon_map.json. A port
//          of TopologyViewer._resolveCiscoIconFile in
//          internal/mapweb/assets/viewer.js.
//   Line   the eight embedded line drawings, chosen through platform_map.json.
//          A port of TopologyViewer._resolveIconKey.
//
// Both tables are built into the resources straight from
// internal/mapweb/assets, not copied. A change to how icons are chosen is
// made in both places.

#ifndef OMEGAMAPS_APP_ICONS_H
#define OMEGAMAPS_APP_ICONS_H

#include <QColor>
#include <QHash>
#include <QPixmap>
#include <QString>
#include <QStringList>
#include <QVector>

namespace omegamaps {

enum class IconSet { Cisco, Line };

// "cisco" / "line", the spelling QSettings, the viewer's --icons and the
// page's ?icons= all use. Anything unrecognised is Cisco, the default.
QString iconSetKey(IconSet set);
IconSet iconSetFromKey(const QString &key);

class IconLibrary {
public:
    static IconLibrary &instance();

    // The set the preview draws with. Cisco falls back to Line when its table
    // could not be read, so a broken resource costs the look, not the icons.
    IconSet iconSet() const;
    void setIconSet(IconSet set);
    // True while the Cisco images are what keyFor() hands out; the preview
    // draws them in a white circle rather than on the tile.
    bool usingCiscoImages() const { return iconSet() == IconSet::Cisco; }
    // Bumped by every setIconSet that changes anything, so a cached key can
    // tell it is stale without being told.
    int generation() const { return m_generation; }

    // "router", "layer-3-switch", ... in the Line set; "cisco/<file>.jpg" in
    // the Cisco set. "undiscovered" for no platform in the Line set.
    QString keyFor(const QString &platform, const QString &name) const;

    // The icon rendered at px x px device pixels, aspect kept, cached.
    QPixmap pixmap(const QString &key, int px) const;

    // The vendor ring colour mapview draws around a device.
    QColor vendorColor(const QString &platform, const QString &name) const;

private:
    IconLibrary();
    void loadLineTable();
    void loadCiscoTable();
    QString lineKeyFor(const QString &platform, const QString &name) const;
    QString ciscoFileFor(const QString &platform, const QString &name) const;
    QString ciscoDefault(const QString &kind) const;

    struct Fallback {
        QStringList platformPatterns;
        QStringList namePatterns;
        QString key;  // Line: icon key. Cisco: the defaults entry it names.
    };

    // Line set (platform_map.json)
    QVector<QPair<QString, QString>> m_patterns;  // longest pattern first
    QVector<Fallback> m_fallbacks;                // file order

    // Cisco set (platform_icon_map.json)
    QVector<QPair<QString, QString>> m_ciscoPatterns;  // lowercased, file order
    QVector<Fallback> m_ciscoFallbacks;                // file order, lowercased
    QHash<QString, QString> m_ciscoDefaults;           // "default_router" -> file

    IconSet m_set = IconSet::Cisco;
    int m_generation = 0;
    mutable QHash<QString, QPixmap> m_cache;
};

}  // namespace omegamaps

#endif
