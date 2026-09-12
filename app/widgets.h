// app/widgets.h
//
// Small building blocks: the stat box from sc2's widgets/stat_box.py, and the
// per-depth bar the progress panel is built from.

#ifndef OMEGAMAPS_APP_WIDGETS_H
#define OMEGAMAPS_APP_WIDGETS_H

#include <QFrame>
#include <QWidget>

class QLabel;

namespace omegamaps {

// A number over a caption. The number's colour is a tone, so it follows the
// theme; sc2 set it as an inline colour and re-applied it on every change.
class StatBox : public QFrame {
    Q_OBJECT
public:
    StatBox(const QString &caption, const QString &tone, QWidget *parent = nullptr);
    void setValue(int v);
    void setText(const QString &text);

private:
    QLabel *m_value = nullptr;
};

// One depth's devices as a stacked bar: reached, failed, not dialed,
// running, queued, in that order, against the depth's total.
struct DepthSegments {
    int reached = 0;
    int failed = 0;
    int notDialed = 0;
    int running = 0;
    int queued = 0;
    int total() const { return reached + failed + notDialed + running + queued; }
};

class DepthBar : public QWidget {
    Q_OBJECT
public:
    explicit DepthBar(QWidget *parent = nullptr);
    void setSegments(const DepthSegments &s);
    QSize sizeHint() const override { return {160, 10}; }
    QSize minimumSizeHint() const override { return {40, 8}; }

protected:
    void paintEvent(QPaintEvent *) override;

private:
    DepthSegments m_s;
};

}  // namespace omegamaps

#endif
