// app/progresspanel.cpp

#include "progresspanel.h"

#include <QGridLayout>
#include <QHBoxLayout>
#include <QLabel>
#include <QVBoxLayout>

#include "theme.h"
#include "widgets.h"

namespace omegamaps {

namespace {
constexpr int kWaitingShown = 4;
}

ProgressPanel::ProgressPanel(QWidget *parent) : Panel(QStringLiteral("Progress"), parent) {
    m_state = new QLabel(tr("Idle"), this);
    m_state->setProperty("role", QStringLiteral("chip"));
    m_state->setProperty("tone", QStringLiteral("muted"));
    barLayout()->addWidget(m_state);

    auto *status = new QHBoxLayout;
    m_source = new QLabel(this);
    m_source->setProperty("tone", QStringLiteral("secondary"));
    m_source->setTextInteractionFlags(Qt::TextSelectableByMouse);
    m_elapsed = new QLabel(this);
    m_elapsed->setProperty("tone", QStringLiteral("secondary"));
    status->addWidget(m_source, 1);
    status->addWidget(m_elapsed);
    bodyLayout()->addLayout(status);

    auto *stats = new QHBoxLayout;
    stats->setSpacing(8);
    m_reached = new StatBox(tr("Reached"), QStringLiteral("success"), this);
    m_failed = new StatBox(tr("Failed"), QStringLiteral("danger"), this);
    m_notDialed = new StatBox(tr("Not dialed"), QStringLiteral("muted"), this);
    m_inFlight = new StatBox(tr("In flight"), QStringLiteral("accent"), this);
    for (StatBox *b : {m_reached, m_failed, m_notDialed, m_inFlight}) stats->addWidget(b);
    bodyLayout()->addLayout(stats);

    // Every credential past the first on a device is a failed login against
    // a real account; this is the number an AAA team asks about.
    m_cost = new QLabel(this);
    m_cost->setProperty("tone", QStringLiteral("muted"));
    bodyLayout()->addWidget(m_cost);

    bodyLayout()->addWidget(makeCaption(tr("BY DEPTH"), "fieldLabel", 8, this));
    m_depthGrid = new QGridLayout;
    m_depthGrid->setHorizontalSpacing(10);
    m_depthGrid->setVerticalSpacing(6);
    m_depthGrid->setColumnStretch(1, 1);
    bodyLayout()->addLayout(m_depthGrid);

    m_waitingCaption = makeCaption(tr("WAITING ON"), "fieldLabel", 8, this);
    bodyLayout()->addWidget(m_waitingCaption);
    for (int i = 0; i < kWaitingShown; ++i) {
        auto *l = new QLabel(this);
        l->setProperty("tone", QStringLiteral("secondary"));
        l->setTextFormat(Qt::PlainText);
        m_waiting.append(l);
        bodyLayout()->addWidget(l);
    }

    reset(QString());
}

ProgressPanel::DepthRow &ProgressPanel::depthRow(int index) {
    while (m_depthRows.size() <= index) {
        const int row = m_depthRows.size();
        DepthRow r;
        r.label = new QLabel(this);
        r.label->setProperty("tone", QStringLiteral("secondary"));
        r.bar = new DepthBar(this);
        r.count = new QLabel(this);
        r.count->setProperty("tone", QStringLiteral("secondary"));
        r.count->setAlignment(Qt::AlignRight | Qt::AlignVCenter);
        r.count->setMinimumWidth(90);
        m_depthGrid->addWidget(r.label, row, 0);
        m_depthGrid->addWidget(r.bar, row, 1);
        m_depthGrid->addWidget(r.count, row, 2);
        m_depthRows.append(r);
    }
    return m_depthRows[index];
}

void ProgressPanel::reset(const QString &sourceLabel) {
    m_state->setText(sourceLabel.isEmpty() ? tr("Idle") : tr("Starting"));
    setTone(m_state, QStringLiteral("muted"));
    m_source->setText(sourceLabel.isEmpty() ? tr("No run loaded") : sourceLabel);
    m_elapsed->clear();
    for (StatBox *b : {m_reached, m_failed, m_notDialed, m_inFlight}) b->setValue(0);
    m_cost->clear();
    for (DepthRow &r : m_depthRows) {
        r.label->hide();
        r.bar->hide();
        r.count->hide();
    }
    m_waitingCaption->hide();
    for (QLabel *l : m_waiting) l->hide();
}

void ProgressPanel::setProgress(const RunProgress &p) {
    const RunCounts &c = p.counts;
    if (p.finished) {
        m_state->setText(tr("Complete"));
        setTone(m_state, QStringLiteral("success"));
    } else {
        m_state->setText(tr("Depth %1").arg(p.depth));
        setTone(m_state, QStringLiteral("accent"));
    }
    m_elapsed->setText(formatElapsed(p.elapsedMs));

    m_reached->setValue(c.reached);
    m_failed->setValue(c.failed);
    m_notDialed->setValue(c.notDialed);
    m_inFlight->setValue(c.running + c.queued);

    if (c.attempts > 0) {
        m_cost->setText(tr("%1 credential attempts, %2 rejected  ·  %3 per device reached")
                            .arg(c.attempts)
                            .arg(c.rejections)
                            .arg(c.reached > 0 ? double(c.attempts) / c.reached : 0.0, 0, 'f', 2));
    } else {
        m_cost->clear();
    }

    for (int i = 0; i < p.depths.size(); ++i) {
        const DepthCounts &d = p.depths.at(i);
        DepthRow &r = depthRow(i);
        r.label->setText(QStringLiteral("D%1").arg(d.depth));
        DepthSegments s;
        s.reached = d.reached;
        s.failed = d.failed;
        s.notDialed = d.notDialed;
        s.running = d.running;
        s.queued = d.queued;
        r.bar->setSegments(s);
        const int done = d.reached + d.failed + d.notDialed;
        const int dialed = d.total - d.notDialed;
        r.count->setText(tr("%1/%2  ·  %3 dialed").arg(done).arg(d.total).arg(dialed));
        r.label->show();
        r.bar->show();
        r.count->show();
        // The batch being collected is the one to watch.
        setTone(r.label, !p.finished && d.depth == p.depth ? QStringLiteral("accent")
                                                            : QStringLiteral("secondary"));
    }
    for (int i = p.depths.size(); i < m_depthRows.size(); ++i) {
        m_depthRows[i].label->hide();
        m_depthRows[i].bar->hide();
        m_depthRows[i].count->hide();
    }

    // The waiting list keeps its lines for the whole run, blank when fewer
    // devices are in flight, rather than appearing while a depth runs and
    // vanishing between depths. A panel whose height changes with every
    // depth boundary makes the whole column re-lay out each time -- which,
    // in a window short enough, was the progress panel being crushed and
    // restored over and over.
    const bool active = !p.finished;
    m_waitingCaption->setVisible(active);
    for (int i = 0; i < m_waiting.size(); ++i) {
        QLabel *l = m_waiting.at(i);
        l->setVisible(active);
        if (!active || i >= p.running.size()) {
            l->setText(QStringLiteral("\u00A0"));  // keeps the line's height
            l->setToolTip(QString());
            continue;
        }
        const RunRow &r = p.running.at(i);
        QString line = r.display;
        if (!r.phase.isEmpty()) line += QStringLiteral("  \u00B7  ") + r.phase;
        line += QStringLiteral("  \u00B7  ") + formatShort(r.phaseMs);
        l->setText(line);
        l->setToolTip(line);
    }
    if (active && p.running.size() > m_waiting.size()) {
        QLabel *last = m_waiting.last();
        last->setText(last->text() + tr("   (+%1 more)").arg(p.running.size() - m_waiting.size()));
    }
}

}  // namespace omegamaps
