// app/theme.h
//
// Colours and the application stylesheet.
//
// THE PALETTES are Secure Cartography's three -- Cyber, Dark, Light -- with
// its token names kept, so a colour decision made in sc2's themes.py reads
// across directly.
//
// THE MECHANISM is omegassh's, not sc2's. sc2 gives every widget its own
// apply_theme() that rebuilds an inline stylesheet, and the main window calls
// each one by hand on a theme change; a widget someone forgets is a widget
// that keeps the old colours, and combobox popups needed a subclass to follow
// at all. Here there is ONE application stylesheet, and widgets opt into it
// by property:
//
//   role     header, wordmark, subtle, panel, panelBar, panelTitle, panelMark,
//            fieldLabel, stat, statValue, statLabel, chip, log, canvas
//   tone     primary, secondary, muted, accent, success, danger, warning
//   primary  "true" on the one button that is the default action
//
// Changing a property after construction needs a repolish; setTone() does it.
// Widgets that paint themselves read ThemeManager::tokens() in paintEvent and
// repaint on ThemeManager::changed -- they hold no colours of their own.

#ifndef OMEGAMAPS_APP_THEME_H
#define OMEGAMAPS_APP_THEME_H

#include <QColor>
#include <QObject>
#include <QString>

class QWidget;

namespace omegamaps {

enum class ThemeId { Cyber, Dark, Light };

struct Tokens {
    QString name;
    bool dark = true;

    QColor bgPrimary;    // window body
    QColor bgSecondary;  // panels
    QColor bgTertiary;   // nested surfaces, stat boxes, table headers
    QColor bgInput;      // input wells, the log
    QColor bgHover;
    QColor bgSelected;

    QColor accent;
    QColor accentDim;
    QColor danger;
    QColor success;
    QColor warning;
    QColor info;

    QColor textPrimary;
    QColor textSecondary;
    QColor textMuted;
    QColor textOnAccent;

    QColor borderPrimary;
    QColor borderSecondary;
    QColor borderDim;

    QColor scrollHandle;
    QColor scrollHover;
};

const Tokens &tokensFor(ThemeId id);
QString themeKey(ThemeId id);            // "cyber", "dark", "light"
ThemeId themeFromKey(const QString &key); // unknown keys fall back to Light

// The whole application sheet for one token set.
QString styleSheet(const Tokens &t);

// Colour for a tone name, for painters that want the same meaning the
// stylesheet gives it.
QColor toneColor(const Tokens &t, const QString &tone);

class ThemeManager : public QObject {
    Q_OBJECT
public:
    static ThemeManager &instance();

    const Tokens &tokens() const { return tokensFor(m_id); }
    ThemeId id() const { return m_id; }

    // Applies the sheet to the whole application and emits changed().
    void setTheme(ThemeId id);

signals:
    void changed();

private:
    ThemeManager() = default;
    ThemeId m_id = ThemeId::Light;
};

// Registers the application's resources (icons, the platform map). Safe to
// call more than once; ThemeManager and IconLibrary both do.
void initResources();

// Sets a style property and repolishes, so the stylesheet notices.
void setStyleProperty(QWidget *w, const char *name, const QString &value);
inline void setTone(QWidget *w, const QString &tone) { setStyleProperty(w, "tone", tone); }

}  // namespace omegamaps

#endif
