// tests/icons_probe.cpp
//
// The preview's icon choice, both sets, with no window:
//
//   - every Cisco image in the resources decodes, which is also the check
//     that Qt's jpeg image-format plugin is present -- without it the preview
//     draws empty circles and says nothing
//   - platform strings resolve to the icon the map viewer picks for them
//     (TopologyViewer._resolveCiscoIconFile in internal/mapweb/assets/
//     viewer.js); the table below is the one that was checked against it
//   - the set switches, falls back, and spells its keys as the viewer does
//
//   QT_QPA_PLATFORM=offscreen ./build/tests/icons_probe
//
// Exit status is the number of failed checks.

#include <QDirIterator>
#include <QGuiApplication>
#include <QImage>

#include <cstdio>

#include "icons.h"

using namespace omegamaps;

namespace {

int failures = 0;

void check(bool ok, const QString &what) {
    std::printf("%s  %s\n", ok ? "ok   " : "FAIL ", qPrintable(what));
    if (!ok) ++failures;
}

struct Case {
    const char *platform;
    const char *name;
    const char *cisco;  // file in the Cisco set
};

// Display platforms as the crawler now writes them (internal/normalize/
// describe.go), a few neighbour-reported ones, and the older fingerprint
// tokens a map from before may still carry.
const Case kCases[] = {
    {"Cisco Nexus9000 C9504 NX-OS", "core-1", "Nexus_7000.jpg"},
    {"Arista DCS-7280SR3-48YC8 EOS", "spine-1", "mulitswitch_device.jpg"},
    {"Arista DCS-7050SX3-48YC8 EOS", "leaf-1", "virtual_layer_switch.jpg"},
    {"Juniper QFX5100-48S-6Q JUNOS", "leaf-3", "virtual_layer_switch.jpg"},
    {"Juniper MX960 JUNOS", "edge-1", "carrier_routing_system.jpg"},
    {"Cisco ISR4451-X/K9 IOS-XE", "wan-rtr-1", "router.jpg"},
    {"Cisco ASA5516 ASA", "fw-1", "pix_firewall.jpg"},
    // Specific before generic: C9300 is a switch even though IOS-XE alone
    // would say router. The table's file order is what decides it.
    {"Cisco C9300-48P IOS-XE", "access-1", "layer_3_switch.jpg"},
    {"Cisco WS-C2960X-48FPD-L IOS", "access-2", "workgroup_switch.jpg"},
    {"cisco AIR-AP2802I-B-K9", "ap-101", "accesspoint.jpg"},
    {"Cisco IP Phone 8851", "sep0c11", "ip_phone.jpg"},
    {"Linux 5.15 Ubuntu", "srv-db-1", "sun_workstation.jpg"},
    {"Cisco AIR-CT5520-K9", "wlc-1", "wlan_controller.jpg"},
    {"Cisco IOSv", "eng-rtr-1", "router.jpg"},
    {"Cisco IOSvL2", "sw-1", "layer_3_switch.jpg"},
    {"Arista vEOS-lab EOS", "spine", "virtual_layer_switch.jpg"},
    {"arista_eos", "eng-spine-1", "virtual_layer_switch.jpg"},
    {"cisco_ios", "eng-rtr-1", "layer_3_switch.jpg"},
    {"", "mystery", "generic_processor.jpg"},
};

}  // namespace

int main(int argc, char **argv) {
    QGuiApplication app(argc, argv);
    IconLibrary &lib = IconLibrary::instance();

    // --- the images -------------------------------------------------------
    int images = 0;
    QDirIterator it(QStringLiteral(":/omegamaps/cisco"));
    while (it.hasNext()) {
        const QString path = it.next();
        const QImage img(path);
        check(!img.isNull(), QStringLiteral("decodes: %1").arg(path));
        ++images;
    }
    check(images == 22, QStringLiteral("22 Cisco images in the resources (%1)").arg(images));

    // --- the Cisco set ----------------------------------------------------
    lib.setIconSet(IconSet::Cisco);
    check(lib.iconSet() == IconSet::Cisco && lib.usingCiscoImages(), QStringLiteral("the Cisco set is active"));
    for (const Case &c : kCases) {
        const QString got = lib.keyFor(QString::fromUtf8(c.platform), QString::fromUtf8(c.name));
        const QString want = QStringLiteral("cisco/") + QString::fromUtf8(c.cisco);
        check(got == want, QStringLiteral("%1 / %2 -> %3").arg(QString::fromUtf8(c.platform), QString::fromUtf8(c.name), got));
    }
    const QPixmap pm = lib.pixmap(lib.keyFor(QStringLiteral("Cisco IOSv"), QStringLiteral("r1")), 64);
    check(!pm.isNull() && pm.width() == 64 && pm.height() == 64, QStringLiteral("a Cisco icon renders at the asked size"));
    QImage pmImg = pm.toImage();
    bool painted = false;
    for (int y = 0; y < pmImg.height() && !painted; ++y)
        for (int x = 0; x < pmImg.width() && !painted; ++x)
            painted = qAlpha(pmImg.pixel(x, y)) > 0;
    check(painted, QStringLiteral("and is not blank"));

    // --- switching --------------------------------------------------------
    const int gen = lib.generation();
    lib.setIconSet(IconSet::Line);
    check(lib.generation() != gen, QStringLiteral("a set change bumps the generation"));
    check(lib.keyFor(QStringLiteral("Cisco IOSv"), QStringLiteral("r1")) == QLatin1String("router"),
          QStringLiteral("the Line set hands out line keys"));
    check(lib.keyFor(QString(), QStringLiteral("x")) == QLatin1String("undiscovered"),
          QStringLiteral("the Line set keeps its undiscovered icon"));
    const int gen2 = lib.generation();
    lib.setIconSet(IconSet::Line);
    check(lib.generation() == gen2, QStringLiteral("setting the same set again changes nothing"));

    check(iconSetKey(IconSet::Cisco) == QLatin1String("cisco") && iconSetKey(IconSet::Line) == QLatin1String("line"),
          QStringLiteral("keys are spelled as the viewer spells them"));
    check(iconSetFromKey(QStringLiteral("LINE")) == IconSet::Line && iconSetFromKey(QStringLiteral("bogus")) == IconSet::Cisco,
          QStringLiteral("anything unrecognised is the Cisco default"));

    std::printf("%s\n", failures ? "CHECKS FAILED" : "all checks passed");
    return failures;
}
