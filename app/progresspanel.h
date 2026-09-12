// app/progresspanel.h
//
// Where the run is. sc2's progress panel shows a single percentage, which a
// breadth-first crawl cannot know -- it does not know how many devices it
// will find. What it does know is each depth: how many are done out of how
// many admitted, while the next depth fills as neighbors are claimed. So
// this panel is one bar per depth, the counts, and the devices the current
// depth is waiting on, longest first -- the head of that list is why a depth
// is slow.

#ifndef OMEGAMAPS_APP_PROGRESSPANEL_H
#define OMEGAMAPS_APP_PROGRESSPANEL_H

#include <QVector>

#include "panel.h"
#include "runtypes.h"

class QGridLayout;
class QLabel;

namespace omegamaps {

class DepthBar;
class StatBox;

class ProgressPanel : public Panel {
    Q_OBJECT
public:
    explicit ProgressPanel(QWidget *parent = nullptr);

    void reset(const QString &sourceLabel);
    void setProgress(const RunProgress &p);

private:
    struct DepthRow {
        QLabel *label = nullptr;
        DepthBar *bar = nullptr;
        QLabel *count = nullptr;
    };
    DepthRow &depthRow(int index);

    QLabel *m_state = nullptr;
    QLabel *m_source = nullptr;
    QLabel *m_elapsed = nullptr;
    StatBox *m_reached = nullptr;
    StatBox *m_failed = nullptr;
    StatBox *m_notDialed = nullptr;
    StatBox *m_inFlight = nullptr;
    QLabel *m_cost = nullptr;
    QGridLayout *m_depthGrid = nullptr;
    QVector<DepthRow> m_depthRows;
    QVector<QLabel *> m_waiting;
    QLabel *m_waitingCaption = nullptr;
};

}  // namespace omegamaps

#endif
