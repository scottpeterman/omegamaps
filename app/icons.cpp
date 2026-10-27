// app/icons.cpp

#include "icons.h"

#include "theme.h"

#include <QFile>
#include <QImage>
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

QStringList strings(const QJsonValue &v, bool lower = false) {
    QStringList out;
    for (const QJsonValue &x : v.toArray()) out << (lower ? x.toString().toLower() : x.toString());
    return out;
}

// The keys of one object in the order the FILE has them. QJsonObject iterates
// in key order; viewer.js, reading the same file, takes the first matching
// fallback rule in file order, so the order is recovered from the raw text.
QStringList keysInFileOrder(const QByteArray &raw, const char *section, const QJsonObject &obj) {
    const QString text = QString::fromUtf8(raw);
    const int start = text.indexOf(QStringLiteral("\"%1\"").arg(QLatin1String(section)));
    QVector<QPair<int, QString>> ordered;
    for (auto it = obj.begin(); it != obj.end(); ++it) {
        const QRegularExpression keyAt(
            QStringLiteral("\"%1\"\\s*:\\s*\\{").arg(QRegularExpression::escape(it.key())));
        const QRegularExpressionMatch m = keyAt.match(text, std::max(start, 0));
        ordered.append({m.hasMatch() ? int(m.capturedStart()) : INT_MAX, it.key()});
    }
    std::sort(ordered.begin(), ordered.end());
    QStringList out;
    for (const auto &[pos, key] : ordered) out << key;
    return out;
}

// The keys of a flat string->string object, in the order the text has them.
// The Cisco table's tier 1 is first-match in file order (see viewer.js), and
// QJsonObject has already sorted that order away.
QStringList flatKeysInFileOrder(const QByteArray &raw, const char *section) {
    const QString text = QString::fromUtf8(raw);
    const int at = text.indexOf(QStringLiteral("\"%1\"").arg(QLatin1String(section)));
    if (at < 0) return {};
    const int open = text.indexOf(QLatin1Char('{'), at);
    const int close = text.indexOf(QLatin1Char('}'), open);
    if (open < 0 || close < 0) return {};
    static const QRegularExpression key(QStringLiteral("\"((?:[^\"\\\\]|\\\\.)*)\"\\s*:"));
    QStringList out;
    auto it = key.globalMatch(text.mid(open + 1, close - open - 1));
    while (it.hasNext()) {
        const QString k = it.next().captured(1);
        // Through the JSON parser, so an escaped key reads as JSON reads it.
        out << QJsonDocument::fromJson(QStringLiteral("[\"%1\"]").arg(k).toUtf8()).array().at(0).toString();
    }
    return out;
}

bool anyContains(const QString &hay, std::initializer_list<const char *> words) {
    for (const char *w : words)
        if (hay.contains(QLatin1String(w))) return true;
    return false;
}

}  // namespace

QString iconSetKey(IconSet set) {
    return set == IconSet::Line ? QStringLiteral("line") : QStringLiteral("cisco");
}

IconSet iconSetFromKey(const QString &key) {
    return key.compare(QLatin1String("line"), Qt::CaseInsensitive) == 0 ? IconSet::Line : IconSet::Cisco;
}

IconLibrary &IconLibrary::instance() {
    static IconLibrary lib;
    return lib;
}

IconLibrary::IconLibrary() {
    initResources();
    loadLineTable();
    loadCiscoTable();
}

IconSet IconLibrary::iconSet() const {
    return (m_set == IconSet::Cisco && !m_ciscoDefaults.isEmpty()) ? IconSet::Cisco : IconSet::Line;
}

void IconLibrary::setIconSet(IconSet set) {
    if (set == m_set) return;
    m_set = set;
    ++m_generation;
}

void IconLibrary::loadCiscoTable() {
    QFile f(QStringLiteral(":/omegamaps/platform_icon_map.json"));
    if (!f.open(QIODevice::ReadOnly)) return;  // the Line set takes over
    const QByteArray raw = f.readAll();
    const QJsonObject root = QJsonDocument::fromJson(raw).object();

    const QJsonObject defaults = root.value(QLatin1String("defaults")).toObject();
    for (auto it = defaults.begin(); it != defaults.end(); ++it)
        m_ciscoDefaults.insert(it.key(), it.value().toString());

    // File order, first match wins: the table is written specific-first
    // (C9300) and generic-last (IOS-XE), and that order is its meaning.
    const QJsonObject pats = root.value(QLatin1String("platform_patterns")).toObject();
    for (const QString &k : flatKeysInFileOrder(raw, "platform_patterns")) {
        if (k.startsWith(QLatin1Char('_')) || !pats.value(k).isString()) continue;
        m_ciscoPatterns.append({k.toLower(), pats.value(k).toString()});
    }

    const QJsonObject fb = root.value(QLatin1String("fallback_patterns")).toObject();
    for (const QString &category : keysInFileOrder(raw, "fallback_patterns", fb)) {
        const QJsonObject c = fb.value(category).toObject();
        QString key = c.value(QLatin1String("icon")).toString();
        if (key.isEmpty()) key = QStringLiteral("default_") + category;
        m_ciscoFallbacks.append({strings(c.value(QLatin1String("platform_patterns")), true),
                                 strings(c.value(QLatin1String("name_patterns")), true), key});
    }
}

QString IconLibrary::ciscoDefault(const QString &kind) const {
    const QString f = m_ciscoDefaults.value(QStringLiteral("default_") + kind);
    return f.isEmpty() ? m_ciscoDefaults.value(QStringLiteral("default_unknown")) : f;
}

QString IconLibrary::ciscoFileFor(const QString &platform, const QString &name) const {
    if (platform.isEmpty() || platform == QLatin1String("Undiscovered")) return ciscoDefault(QStringLiteral("unknown"));
    const QString p = platform.toLower();
    const QString n = name.toLower();

    for (const auto &[pattern, file] : m_ciscoPatterns)
        if (p.contains(pattern)) return file;

    for (const Fallback &fb : m_ciscoFallbacks) {
        const QString file = m_ciscoDefaults.value(fb.key, ciscoDefault(QStringLiteral("unknown")));
        if (anyIn(fb.platformPatterns, p)) return file;
        if (!n.isEmpty() && anyIn(fb.namePatterns, n)) return file;
    }

    if (anyContains(p, {"switch", "nexus", "catalyst", "c9", "ws-c", "dcs", "qfx", "ex"}))
        return ciscoDefault(QStringLiteral("switch"));
    if (anyContains(p, {"router", "isr", "asr", "csr", "mx"})) return ciscoDefault(QStringLiteral("router"));
    if (anyContains(p, {"firewall", "asa", "ftd", "palo", "srx"})) return ciscoDefault(QStringLiteral("firewall"));
    if (anyContains(p, {"wireless", "ap", "wlc", "aironet"})) return ciscoDefault(QStringLiteral("wireless"));
    if (anyContains(p, {"phone", "sep"})) return ciscoDefault(QStringLiteral("phone"));
    if (anyContains(p, {"ata", "vg"})) return ciscoDefault(QStringLiteral("ata"));
    if (anyContains(p, {"linux", "debian", "ubuntu", "centos", "server"})) return ciscoDefault(QStringLiteral("endpoint"));
    return ciscoDefault(QStringLiteral("unknown"));
}

void IconLibrary::loadLineTable() {
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
    if (iconSet() == IconSet::Cisco) return QStringLiteral("cisco/") + ciscoFileFor(platform, name);
    return lineKeyFor(platform, name);
}

QString IconLibrary::lineKeyFor(const QString &platform, const QString &name) const {
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

    if (key.startsWith(QLatin1String("cisco/"))) {
        // Raster and not square: scaled to fit, centred, aspect kept.
        const QImage img(QStringLiteral(":/omegamaps/") + key);
        QPixmap pm(px, px);
        pm.fill(Qt::transparent);
        if (!img.isNull()) {
            const QImage scaled = img.scaled(px, px, Qt::KeepAspectRatio, Qt::SmoothTransformation);
            QPainter painter(&pm);
            painter.drawImage((px - scaled.width()) / 2, (px - scaled.height()) / 2, scaled);
        }
        m_cache.insert(cacheKey, pm);
        return pm;
    }

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
