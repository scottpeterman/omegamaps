// app/theme.cpp

#include "theme.h"

#include <QApplication>
#include <QStyle>
#include <QVariant>
#include <QWidget>

// The resources live in a static library, where nothing references their
// initializer and the linker is free to drop it; Q_INIT_RESOURCE is the
// reference. It has to be outside any namespace.
static void initResourcesOnce() {
    static const bool done = [] {
        Q_INIT_RESOURCE(resources);
        return true;
    }();
    (void)done;
}

namespace omegamaps {

void initResources() { initResourcesOnce(); }

namespace {

// Values are sc2's ui/themes.py, token for token.
Tokens makeCyber() {
    Tokens t;
    t.name = QStringLiteral("Cyber");
    t.dark = true;
    t.bgPrimary = QColor("#0a0a0f");
    t.bgSecondary = QColor("#12121a");
    t.bgTertiary = QColor("#1a1a25");
    t.bgInput = QColor("#0d1a1a");
    t.bgHover = QColor("#0f2626");
    t.bgSelected = QColor("#0a3333");
    t.accent = QColor("#00ffff");
    t.accentDim = QColor("#00b3b3");
    t.danger = QColor("#ff0055");
    t.success = QColor("#00ff88");
    t.warning = QColor("#ffaa00");
    t.info = QColor("#00aaff");
    t.textPrimary = QColor("#e0f7ff");
    t.textSecondary = QColor("#88c8d4");
    t.textMuted = QColor("#5a8a94");
    t.textOnAccent = QColor("#0a0a0f");
    t.borderPrimary = QColor("#00ffff");
    t.borderSecondary = QColor("#2a5a5a");
    t.borderDim = QColor("#1a3a3a");
    t.scrollHandle = QColor("#1a3a3a");
    t.scrollHover = QColor("#00ffff");
    return t;
}

Tokens makeDark() {
    Tokens t;
    t.name = QStringLiteral("Dark");
    t.dark = true;
    t.bgPrimary = QColor("#000000");
    t.bgSecondary = QColor("#0a0a0a");
    t.bgTertiary = QColor("#141414");
    t.bgInput = QColor("#0f0d08");
    t.bgHover = QColor("#1a1608");
    t.bgSelected = QColor("#1f1a0a");
    t.accent = QColor("#d4af37");
    t.accentDim = QColor("#b8960c");
    t.danger = QColor("#dc2626");
    t.success = QColor("#22c55e");
    t.warning = QColor("#f59e0b");
    t.info = QColor("#3b82f6");
    t.textPrimary = QColor("#f5f5f5");
    t.textSecondary = QColor("#a3a3a3");
    t.textMuted = QColor("#666666");
    t.textOnAccent = QColor("#000000");
    t.borderPrimary = QColor("#d4af37");
    t.borderSecondary = QColor("#4a4020");
    t.borderDim = QColor("#2a2510");
    t.scrollHandle = QColor("#2a2510");
    t.scrollHover = QColor("#d4af37");
    return t;
}

Tokens makeLight() {
    Tokens t;
    t.name = QStringLiteral("Light");
    t.dark = false;
    t.bgPrimary = QColor("#ffffff");
    t.bgSecondary = QColor("#f8fafc");
    t.bgTertiary = QColor("#f1f5f9");
    t.bgInput = QColor("#f8f9fa");
    t.bgHover = QColor("#e8ecf0");
    t.bgSelected = QColor("#dbeafe");
    t.accent = QColor("#2563eb");
    t.accentDim = QColor("#1d4ed8");
    t.danger = QColor("#dc2626");
    t.success = QColor("#16a34a");
    t.warning = QColor("#d97706");
    t.info = QColor("#0284c7");
    t.textPrimary = QColor("#1e293b");
    t.textSecondary = QColor("#64748b");
    t.textMuted = QColor("#94a3b8");
    t.textOnAccent = QColor("#ffffff");
    t.borderPrimary = QColor("#2563eb");
    t.borderSecondary = QColor("#cbd5e1");
    t.borderDim = QColor("#e2e8f0");
    t.scrollHandle = QColor("#cbd5e1");
    t.scrollHover = QColor("#2563eb");
    return t;
}

QString hex(const QColor &c) { return c.name(QColor::HexRgb); }

}  // namespace

const Tokens &tokensFor(ThemeId id) {
    static const Tokens cyber = makeCyber();
    static const Tokens dark = makeDark();
    static const Tokens light = makeLight();
    switch (id) {
    case ThemeId::Cyber: return cyber;
    case ThemeId::Dark: return dark;
    case ThemeId::Light: break;
    }
    return light;
}

QString themeKey(ThemeId id) {
    switch (id) {
    case ThemeId::Cyber: return QStringLiteral("cyber");
    case ThemeId::Dark: return QStringLiteral("dark");
    case ThemeId::Light: break;
    }
    return QStringLiteral("light");
}

ThemeId themeFromKey(const QString &key) {
    const QString k = key.trimmed().toLower();
    if (k == QLatin1String("cyber")) return ThemeId::Cyber;
    if (k == QLatin1String("dark")) return ThemeId::Dark;
    return ThemeId::Light;
}

QColor toneColor(const Tokens &t, const QString &tone) {
    if (tone == QLatin1String("accent")) return t.accent;
    if (tone == QLatin1String("success")) return t.success;
    if (tone == QLatin1String("danger")) return t.danger;
    if (tone == QLatin1String("warning")) return t.warning;
    if (tone == QLatin1String("info")) return t.info;
    if (tone == QLatin1String("secondary")) return t.textSecondary;
    if (tone == QLatin1String("muted")) return t.textMuted;
    return t.textPrimary;
}

QString styleSheet(const Tokens &t) {
    // Button text on the accent gradient: sc2 uses the window colour on dark
    // themes (dark text on cyan or gold) and white on light ones.
    const QString onAccent = t.dark ? hex(t.bgPrimary) : QStringLiteral("#ffffff");

    QString s = QStringLiteral(R"(
QMainWindow, QWidget#central { background: %bgPrimary%; }
QWidget { color: %textPrimary%; }
QToolTip {
    background: %bgTertiary%; color: %textPrimary%;
    border: 1px solid %borderSecondary%; padding: 4px 6px;
}

QMenuBar {
    background: %bgSecondary%; color: %textPrimary%;
    border-bottom: 1px solid %borderDim%;
}
QMenuBar::item { background: transparent; padding: 4px 10px; }
QMenuBar::item:selected, QMenuBar::item:pressed { background: %bgHover%; color: %accent%; }
QMenu {
    background: %bgSecondary%; color: %textPrimary%;
    border: 1px solid %borderSecondary%; padding: 4px 0;
}
QMenu::item { padding: 5px 24px 5px 20px; }
QMenu::item:selected { background: %bgSelected%; color: %accent%; }
QMenu::item:disabled { color: %textMuted%; }
QMenu::separator { height: 1px; background: %borderDim%; margin: 4px 8px; }
QMenu::indicator { width: 12px; height: 12px; left: 4px; }
QStatusBar { background: %bgSecondary%; border-top: 1px solid %borderDim%; }
QStatusBar QLabel { color: %textSecondary%; background: transparent; }

QFrame[role="header"] {
    background: %bgSecondary%; border: none; border-bottom: 1px solid %borderDim%;
}
QLabel[role="wordmark"] { color: %textPrimary%; background: transparent; }
QLabel[role="subtle"] { color: %textMuted%; background: transparent; }

QFrame[role="panel"] {
    background: %bgSecondary%; border: 1px solid %borderDim%; border-radius: 8px;
}
QFrame[role="panelBar"] {
    background: transparent; border: none; border-bottom: 1px solid %borderDim%;
    border-top-left-radius: 8px; border-top-right-radius: 8px;
}
QLabel[role="panelTitle"] { color: %textPrimary%; background: transparent; }
QLabel[role="panelMark"] { background: %accent%; border-radius: 2px; }
QWidget[role="panelBody"] { background: transparent; }

QLabel[role="fieldLabel"] { color: %textSecondary%; background: transparent; }

QFrame[role="stat"] {
    background: %bgTertiary%; border: 1px solid %borderDim%; border-radius: 6px;
}
QLabel[role="statValue"] { background: transparent; }
QLabel[role="statLabel"] { color: %textMuted%; background: transparent; }

QLabel[role="chip"] {
    background: %bgTertiary%; border: 1px solid %borderDim%; border-radius: 9px;
    padding: 1px 8px;
}

QLabel[tone="primary"]   { color: %textPrimary%; }
QLabel[tone="secondary"] { color: %textSecondary%; }
QLabel[tone="muted"]     { color: %textMuted%; }
QLabel[tone="accent"]    { color: %accent%; }
QLabel[tone="success"]   { color: %success%; }
QLabel[tone="danger"]    { color: %danger%; }
QLabel[tone="warning"]   { color: %warning%; }
QLabel[tone="info"]      { color: %info%; }

QPushButton {
    background: transparent; border: 1px solid %borderDim%; border-radius: 6px;
    padding: 5px 12px; color: %textSecondary%;
}
QPushButton:hover { border-color: %accent%; color: %accent%; }
QPushButton:checked { border-color: %accent%; color: %accent%; background: %bgSelected%; }
QPushButton:disabled { color: %textMuted%; border-color: %borderDim%; }
QPushButton[primary="true"] {
    border: none; border-radius: 8px; font-weight: 600; color: %onAccent%;
    background: qlineargradient(x1:0, y1:0, x2:1, y2:0,
                                stop:0 %accentDim%, stop:1 %accent%);
}
QPushButton[primary="true"]:hover {
    background: qlineargradient(x1:0, y1:0, x2:1, y2:0,
                                stop:0 %accent%, stop:1 %accentDim%);
}

QLineEdit, QComboBox {
    background: %bgInput%; border: 1px solid %borderDim%; border-radius: 6px;
    padding: 4px 8px; color: %textPrimary%;
    selection-background-color: %accent%; selection-color: %onAccent%;
}
QLineEdit:focus, QComboBox:focus { border-color: %accent%; }
QComboBox:hover { border-color: %accent%; }
QComboBox::drop-down { border: none; width: 18px; }
QComboBox::down-arrow { image: url(:/omegamaps/icons/chevron-down.svg); width: 10px; height: 10px; }
QComboBox QAbstractItemView {
    background: %bgSecondary%; color: %textPrimary%; border: 1px solid %borderSecondary%;
    selection-background-color: %bgSelected%; selection-color: %textPrimary%;
    outline: none;
}

QSpinBox {
    background: %bgInput%; border: 1px solid %borderDim%; border-radius: 6px;
    padding: 4px 6px; color: %textPrimary%;
}
QSpinBox:focus { border-color: %accent%; }
QPlainTextEdit {
    background: %bgInput%; border: 1px solid %borderDim%; border-radius: 6px;
    color: %textPrimary%; padding: 2px 4px;
}
QPlainTextEdit:focus { border-color: %accent%; }
QCheckBox { color: %textPrimary%; spacing: 8px; background: transparent; }
QCheckBox::indicator {
    width: 14px; height: 14px; border: 1px solid %borderSecondary%;
    border-radius: 3px; background: %bgInput%;
}
QCheckBox::indicator:checked { background: %accent%; border-color: %accent%; }

QTableWidget {
    background: %bgInput%; border: 1px solid %borderDim%; border-radius: 6px;
    color: %textPrimary%; gridline-color: %borderDim%;
    selection-background-color: %bgSelected%; selection-color: %textPrimary%;
}
QHeaderView::section {
    background: %bgTertiary%; color: %textSecondary%; border: none;
    border-bottom: 1px solid %borderDim%; padding: 4px 6px; font-weight: 600;
}
QTableCornerButton::section { background: %bgTertiary%; border: none; }

QDialog { background: %bgSecondary%; }
QScrollArea { background: transparent; border: none; }
QScrollArea > QWidget > QWidget#central { background: %bgPrimary%; }

QLineEdit[invalid="true"], QPlainTextEdit[invalid="true"], QSpinBox[invalid="true"],
QComboBox[invalid="true"] { border: 1px solid %danger%; }
QLabel[invalid="true"] { color: %danger%; }

QPlainTextEdit[role="log"] {
    background: %bgInput%; border: 1px solid %borderDim%; border-radius: 6px;
    color: %textPrimary%; padding: 4px;
}
QGraphicsView[role="canvas"] {
    background: %bgPrimary%; border: 1px solid %borderDim%; border-radius: 6px;
}

QSplitter::handle { background: %borderDim%; }
QSplitter::handle:hover { background: %accent%; }

QScrollBar:vertical { background: transparent; width: 10px; margin: 0; }
QScrollBar::handle:vertical {
    background: %scrollHandle%; border-radius: 5px; min-height: 24px;
}
QScrollBar::handle:vertical:hover { background: %scrollHover%; }
QScrollBar:horizontal { background: transparent; height: 10px; margin: 0; }
QScrollBar::handle:horizontal {
    background: %scrollHandle%; border-radius: 5px; min-width: 24px;
}
QScrollBar::handle:horizontal:hover { background: %scrollHover%; }
QScrollBar::add-line, QScrollBar::sub-line { width: 0; height: 0; }
QScrollBar::add-page, QScrollBar::sub-page { background: none; }
)");

    const std::pair<const char *, QColor> vars[] = {
        {"%bgPrimary%", t.bgPrimary},         {"%bgSecondary%", t.bgSecondary},
        {"%bgTertiary%", t.bgTertiary},       {"%bgInput%", t.bgInput},
        {"%bgSelected%", t.bgSelected},       {"%bgHover%", t.bgHover},
        {"%accent%", t.accent},
        {"%accentDim%", t.accentDim},         {"%success%", t.success},
        {"%danger%", t.danger},               {"%warning%", t.warning},
        {"%info%", t.info},                   {"%textPrimary%", t.textPrimary},
        {"%textSecondary%", t.textSecondary}, {"%textMuted%", t.textMuted},
        {"%borderSecondary%", t.borderSecondary}, {"%borderDim%", t.borderDim},
        {"%scrollHandle%", t.scrollHandle},   {"%scrollHover%", t.scrollHover},
    };
    for (const auto &[key, color] : vars) s.replace(QLatin1String(key), hex(color));
    s.replace(QLatin1String("%onAccent%"), onAccent);
    return s;
}

ThemeManager &ThemeManager::instance() {
    static ThemeManager tm;
    return tm;
}

void ThemeManager::setTheme(ThemeId id) {
    initResources();  // the sheet refers to :/omegamaps images
    m_id = id;
    if (qApp) qApp->setStyleSheet(styleSheet(tokensFor(id)));
    emit changed();
}

void setStyleProperty(QWidget *w, const char *name, const QString &value) {
    if (!w) return;
    if (w->property(name).toString() == value) return;
    w->setProperty(name, value);
    w->style()->unpolish(w);
    w->style()->polish(w);
    w->update();
}

}  // namespace omegamaps
