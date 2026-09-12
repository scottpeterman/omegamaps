// app/panel.cpp

#include "panel.h"

#include <QHBoxLayout>
#include <QLabel>
#include <QVBoxLayout>

namespace omegamaps {

QLabel *makeCaption(const QString &text, const char *role, int pointSize, QWidget *parent) {
    auto *l = new QLabel(text, parent);
    l->setProperty("role", QString::fromLatin1(role));
    QFont f = l->font();
    f.setPointSize(pointSize);
    f.setWeight(QFont::DemiBold);
    // Qt stylesheets have no letter-spacing, so it goes on the font.
    f.setLetterSpacing(QFont::AbsoluteSpacing, 1.5);
    l->setFont(f);
    return l;
}

Panel::Panel(const QString &title, QWidget *parent) : QFrame(parent) {
    setProperty("role", QStringLiteral("panel"));

    auto *outer = new QVBoxLayout(this);
    outer->setContentsMargins(0, 0, 0, 0);
    outer->setSpacing(0);

    auto *bar = new QFrame(this);
    bar->setProperty("role", QStringLiteral("panelBar"));
    auto *barRow = new QHBoxLayout(bar);
    barRow->setContentsMargins(14, 8, 10, 8);
    barRow->setSpacing(8);

    // A painted mark rather than sc2's emoji: the emoji depend on a colour
    // font being installed, and without one they render as empty boxes.
    auto *mark = new QLabel(bar);
    mark->setProperty("role", QStringLiteral("panelMark"));
    mark->setFixedSize(4, 14);
    barRow->addWidget(mark);

    barRow->addWidget(makeCaption(title.toUpper(), "panelTitle", 9, bar));
    barRow->addStretch(1);

    m_bar = new QHBoxLayout;
    m_bar->setSpacing(6);
    barRow->addLayout(m_bar);
    outer->addWidget(bar);

    auto *body = new QWidget(this);
    body->setProperty("role", QStringLiteral("panelBody"));
    m_body = new QVBoxLayout(body);
    m_body->setContentsMargins(14, 12, 14, 14);
    m_body->setSpacing(10);
    outer->addWidget(body, 1);
}

}  // namespace omegamaps
