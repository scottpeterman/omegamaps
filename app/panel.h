// app/panel.h
//
// The container every section of the window sits in: a titled card, after
// sc2's widgets/panel.py. Title on the left, a slot on the right of the bar
// for a panel's own controls, and a body layout below.
//
// Unlike sc2's Panel it carries no colours and no apply_theme(): it sets
// style roles and the application stylesheet does the rest.

#ifndef OMEGAMAPS_APP_PANEL_H
#define OMEGAMAPS_APP_PANEL_H

#include <QFrame>

class QHBoxLayout;
class QLabel;
class QVBoxLayout;

namespace omegamaps {

class Panel : public QFrame {
    Q_OBJECT
public:
    explicit Panel(const QString &title, QWidget *parent = nullptr);

    // Right-hand side of the title bar, for the panel's own controls.
    QHBoxLayout *barLayout() const { return m_bar; }
    QVBoxLayout *bodyLayout() const { return m_body; }

private:
    QHBoxLayout *m_bar = nullptr;
    QVBoxLayout *m_body = nullptr;
};

// An uppercase, letter-spaced caption in the sc2 style.
QLabel *makeCaption(const QString &text, const char *role, int pointSize,
                    QWidget *parent = nullptr);

}  // namespace omegamaps

#endif
