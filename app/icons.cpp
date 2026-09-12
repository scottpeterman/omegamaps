// app/icons.cpp

#include "icons.h"

#include "theme.h"

#include <QFile>
#include <QJsonDocument>
#include <QJsonObject>
#include <QJsonArray>
#include <QPainter>
#include <QRegularExpression>
#include <QSvgRenderer>

#include <algorithm>
#include <climits>

namespace omegamaps {

namespace {

// viewer.js _shapeToIconKey: the last dotted part of a drawio shape name.
QString shapeToKey(const QString &shape) {
    static const QHash<QString, QString> map = {
        {QStringLiteral("router"), QStringLiteral("router")},
        {QStringLiteral("layer_3_switch"), QStringLiteral("layer-3-switch")},
        {QStringLiteral("workgroup_switch"), QStringLiteral("workgroup-switch")},
        {QStringLiteral("multilayer_switch"), QStringLiteral("multilayer-switch")},
        {QStringLiteral("multilayer_remote_switch"), QStringLiteral("multilayer-switch")},
        {QStringLiteral("firewall"), QStringLiteral("firewall")},
        {QStringLiteral("asa_5500"), QStringLiteral("firewall")},
        {QStringLiteral("pix_firewall"), QStringLiteral("firewall")},
        {QStringLiteral("access_point"), QStringLiteral("access-point")},
        {QStringLiteral("wireless_transport"), QStringLiteral("access-point")},
        {QStringLiteral("generic_server"), QStringLiteral("server")},
        {QStringLiteral("pc"), QStringLiteral("server")},
        {QStringLiteral("workstation"), QStringLiteral("server")},
    };
    QString s = shape;
    s.remove(QStringLiteral("shape="));
    return map.value(s.section(QLatin1Char('.'), -1));
}

bool anyIn(const QStringList &pats, const QString &hay) {
    for (const QString &p : pats)
        if (hay.contains(p)) return true;
    return false;
}

// viewer.js _detectDeviceRole, mapped through _roleToIconKey.
QString roleKey(const QString &platform, const QString &name) {
    const QString p = platform.toLower();
    const QString n = name.toLower();
    static const QStringList fwP = {"asa", "firepower", "ftd", "fxos", "pan-os", "pa-", "panos",
                                    "fortigate", "fortios", "srx", "screenos", "checkpoint", "gaia"};
    static const QStringList fwN = {"fw", "firewall", "palo", "forti", "asa"};
    if (anyIn(fwP, p) || anyIn(fwN, n)) return QStringLiteral("firewall");

    static const QStringList rtP = {"isr", "asr", "ncs", "crs", "c8000", "7600", "7200", "7500",
                                    "mx-", "mx9", "mx4", "mx2", "mx1", "mx8", "mx10",
                                    "vmx", "ptx", "acx", "7500r", "7280r"};
    static const QStringList rtN = {"rtr", "-rt-", "-rt.", "router", "gw-", "gw.", "gateway",
                                    "wan-", "wan.", "border", "br-", "br.", "pe-", "pe.", "-pe-",
                                    "ce-", "ce.", "mx-", "mx."};
    if (anyIn(rtP, p) || anyIn(rtN, n)) return QStringLiteral("router");

    static const QStringList l2P = {"2960", "3560", "3750", "c1000", "cbs", "ex2200", "ex2300",
                                    "ex3300", "ws-c29", "ws-c35", "ws-c37", "ie-", "ie2000",
                                    "ie3000", "ie4000", "sf", "sg", "c1200", "c1300"};
    static const QStringList l2N = {"access", "acc-", "acc.", "closet", "idf", "mdf",
                                    "edge-sw", "tor-", "tor.", "leaf-", "leaf."};
    if (anyIn(l2P, p) || anyIn(l2N, n)) return QStringLiteral("workgroup-switch");

    return QStringLiteral("layer-3-switch");
}

QStringList strings(const QJsonValue &v) {
    QStringList out;
    for (const QJsonValue &x : v.toArray()) out << x.toString();
    return out;
}

}  // namespace

IconLibrary &IconLibrary::instance() {
    static IconLibrary lib;
    return lib;
}

IconLibrary::IconLibrary() {
    initResources();
    QFile f(QStringLiteral(":/omegamaps/platform_map.json"));
    if (!f.open(QIODevice::ReadOnly)) return;  // tier 3 alone still picks icons
    const QJsonObject root = QJsonDocument::fromJson(f.readAll()).object();

    const QJsonObject pats = root.value(QLatin1String("platform_patterns")).toObject();
    for (auto it = pats.begin(); it != pats.end(); ++it) {
        if (it.key().startsWith(QLatin1String("_comment"))) continue;
        m_patterns.append({it.key(), it.value().toString()});
    }
    // Longest first, so "C9300" wins over "C9". Stable on ties, like JS sort.
    std::stable_sort(m_patterns.begin(), m_patterns.end(),
                     [](const auto &a, const auto &b) { return a.first.size() > b.first.size(); });

    // QJsonObject iterates in key order, not file order; viewer.js takes the
    // first rule that matches in FILE order, so the order is read from the
    // raw document instead.
    const QJsonObject fb = root.value(QLatin1String("fallback_patterns")).toObject();
    f.seek(0);
    const QByteArray raw = f.readAll();
    const int start = raw.indexOf("\"fallback_patterns\"");
    QVector<QPair<int, QString>> ordered;
    for (auto it = fb.begin(); it != fb.end(); ++it) {
        const QRegularExpression keyAt(
            QStringLiteral("\"%1\"\\s*:\\s*\\{").arg(QRegularExpression::escape(it.key())));
        const QRegularExpressionMatch m = keyAt.match(QString::fromUtf8(raw), start);
        ordered.append({m.hasMatch() ? int(m.capturedStart()) : INT_MAX, it.key()});
    }
    std::sort(ordered.begin(), ordered.end());
    for (const auto &[pos, key] : ordered) {
        const QJsonObject c = fb.value(key).toObject();
        m_fallbacks.append({strings(c.value(QLatin1String("platform_patterns"))),
                            strings(c.value(QLatin1String("name_patterns"))),
                            shapeToKey(c.value(QLatin1String("shape")).toString())});
    }
}

QString IconLibrary::keyFor(const QString &platform, const QString &name) const {
    if (platform.isEmpty() || platform == QLatin1String("Undiscovered"))
        return QStringLiteral("undiscovered");

    // Tier 1: case-sensitive substring on the platform string.
    for (const auto &[pattern, shape] : m_patterns) {
        if (platform.contains(pattern)) {
            const QString k = shapeToKey(shape);
            if (!k.isEmpty()) return k;
        }
    }
    // Tier 2: looser rules on the platform and the hostname.
    const QString p = platform.toLower();
    const QString n = name.toLower();
    for (const Fallback &fb : m_fallbacks) {
        if ((anyIn(fb.platformPatterns, p) || anyIn(fb.namePatterns, n)) && !fb.key.isEmpty())
            return fb.key;
    }
    // Tier 3: role from the strings alone.
    return roleKey(platform, name);
}

QPixmap IconLibrary::pixmap(const QString &key, int px) const {
    const QString cacheKey = key + QLatin1Char('@') + QString::number(px);
    auto it = m_cache.constFind(cacheKey);
    if (it != m_cache.constEnd()) return *it;

    QSvgRenderer svg(QStringLiteral(":/omegamaps/icons/%1.svg").arg(key));
    if (!svg.isValid()) svg.load(QStringLiteral(":/omegamaps/icons/layer-3-switch.svg"));
    QPixmap pm(px, px);
    pm.fill(Qt::transparent);
    QPainter painter(&pm);
    painter.setRenderHint(QPainter::Antialiasing);
    svg.render(&painter);
    painter.end();
    m_cache.insert(cacheKey, pm);
    return pm;
}

QColor IconLibrary::vendorColor(const QString &platform, const QString &name) const {
    // viewer.js _detectVendor / _vendorColors.
    const QString p = platform.toLower();
    const QString n = name.toLower();
    static const QVector<QPair<QStringList, QColor>> checks = {
        {{"junos", "juniper", "mx", "qfx", "ex2", "ex3", "ex4", "srx", "ptx", "acx"}, QColor("#F58536")},
        {{"arista", "eos", "veos", "dcs-", "ccs-"}, QColor("#2D8659")},
        {{"palo", "pan-", "pa-"}, QColor("#FA582D")},
        {{"forti", "fortigate", "fortios"}, QColor("#EE3124")},
        {{"cisco", "ios", "nx-os", "nexus", "catalyst", "c9", "ws-c", "isr", "asr", "asa"}, QColor("#049fd9")},
    };
    for (const auto &[pats, color] : checks) {
        for (const QString &pat : pats)
            if (p.contains(pat) || n.contains(pat)) return color;
    }
    return QColor("#4a9eff");
}

}  // namespace omegamaps
