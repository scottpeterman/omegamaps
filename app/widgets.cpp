// app/widgets.cpp

#include "widgets.h"

#include <QLabel>
#include <QPainter>
#include <QPainterPath>
#include <QVBoxLayout>

#include "panel.h"
#include "theme.h"

namespace omegamaps {

StatBox::StatBox(const QString &caption, const QString &tone, QWidget *parent) : QFrame(parent) {
    setProperty("role", QStringLiteral("stat"));
    auto *col = new QVBoxLayout(this);
    col->setContentsMargins(10, 8, 10, 8);
    col->setSpacing(2);

    m_value = new QLabel(QStringLiteral("0"), this);
    m_value->setProperty("role", QStringLiteral("statValue"));
    m_value->setProperty("tone", tone);
    QFont f = m_value->font();
    f.setPointSize(18);
    f.setWeight(QFont::Bold);
    m_value->setFont(f);
    m_value->setAlignment(Qt::AlignCenter);
    col->addWidget(m_value);

    auto *cap = makeCaption(caption.toUpper(), "statLabel", 7, this);
    cap->setAlignment(Qt::AlignCenter);
    col->addWidget(cap);
}

void StatBox::setValue(int v) { m_value->setText(QString::number(v)); }
void StatBox::setText(const QString &text) { m_value->setText(text); }

DepthBar::DepthBar(QWidget *parent) : QWidget(parent) {
    setSizePolicy(QSizePolicy::Expanding, QSizePolicy::Fixed);
    setFixedHeight(10);
    connect(&ThemeManager::instance(), &ThemeManager::changed, this,
            qOverload<>(&QWidget::update));
}

void DepthBar::setSegments(const DepthSegments &s) {
    m_s = s;
    update();
}

void DepthBar::paintEvent(QPaintEvent *) {
    const Tokens &t = ThemeManager::instance().tokens();
    QPainter p(this);
    p.setRenderHint(QPainter::Antialiasing);
    const QRectF r = QRectF(rect()).adjusted(0.5, 0.5, -0.5, -0.5);
    const qreal radius = r.height() / 2;

    QPainterPath clip;
    clip.addRoundedRect(r, radius, radius);
    p.setClipPath(clip);
    p.fillRect(r, t.bgTertiary);

    const int total = m_s.total();
    if (total > 0) {
        const std::pair<int, QColor> parts[] = {
            {m_s.reached, t.success},    {m_s.failed, t.danger},
            {m_s.notDialed, t.textMuted}, {m_s.running, t.accent},
            {m_s.queued, t.borderSecondary},
        };
        qreal x = r.left();
        for (const auto &[n, color] : parts) {
            if (n <= 0) continue;
            const qreal w = r.width() * n / total;
            p.fillRect(QRectF(x, r.top(), w, r.height()), color);
            x += w;
        }
    }
    p.setClipping(false);
    p.setPen(QPen(t.borderDim, 1));
    p.setBrush(Qt::NoBrush);
    p.drawRoundedRect(r, radius, radius);
}

}  // namespace omegamaps
