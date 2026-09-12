// app/topologypreview.h
//
// The map, drawn natively while the crawl runs.
//
// sc2 renders its preview in a QWebEngineView running cytoscape. That puts a
// Chromium runtime in every bundle and makes the preview impossible to
// capture offscreen. This is a QGraphicsView instead, and it can do one thing
// the web preview could not: draw from the run as it happens. A device
// appears when it is admitted, in the row for its depth, under the device
// that reported it; its ring says where it is (queued, running, reached,
// failed); and when the run finishes and map.json exists, the reporting tree
// is replaced by the real links.
//
// Devices that were deliberately not dialed are not drawn. Where switches
// report the hosts behind them, excluded hosts can outnumber the network
// several times over; they are counted in the progress panel and listed in
// the log instead.

#ifndef OMEGAMAPS_APP_TOPOLOGYPREVIEW_H
#define OMEGAMAPS_APP_TOPOLOGYPREVIEW_H

#include <QElapsedTimer>
#include <QGraphicsView>
#include <QHash>
#include <QTimer>
#include <QVector>

#include "panel.h"
#include "runtypes.h"

class QGraphicsScene;
class QLabel;
class QLineEdit;

namespace omegamaps {

class NodeItem;
class EdgeItem;

// The view: wheel zoom about the cursor, drag to pan, and a flag the preview
// reads to stop auto-fitting once the user has taken over.
class CanvasView : public QGraphicsView {
    Q_OBJECT
public:
    explicit CanvasView(QGraphicsScene *scene, QWidget *parent = nullptr);
    bool userMoved() const { return m_userMoved; }
    void clearUserMoved() { m_userMoved = false; }
    void markUserMoved() { m_userMoved = true; }

signals:
    void resized();

protected:
    void wheelEvent(QWheelEvent *e) override;
    void resizeEvent(QResizeEvent *e) override;
    void mousePressEvent(QMouseEvent *e) override;

private:
    bool m_userMoved = false;
};

class TopologyPreview : public Panel {
    Q_OBJECT
public:
    explicit TopologyPreview(QWidget *parent = nullptr);
    ~TopologyPreview() override;

    void reset();
    void updateRows(const QVector<RunRow> &rows);
    void setFinished(bool finished);

    // Replaces the reporting tree with the links in map.json. Returns how
    // many device-to-device links were drawn; *unmatched counts map devices
    // that could not be tied to a row of this run.
    int setFinalMap(const TopologyMap &map, int *unmatched = nullptr);

    int deviceCount() const { return m_nodes.size(); }
    int linkCount() const { return m_links.size(); }

public slots:
    void fit();

private:
    void scheduleLayout();
    void relayout();
    void updateStats();
    void applySearch(const QString &text);
    void centerOnMatch();
    void clearLinks();

    QGraphicsScene *m_scene = nullptr;
    CanvasView *m_view = nullptr;
    QLabel *m_stats = nullptr;
    QLabel *m_live = nullptr;
    QLineEdit *m_search = nullptr;

    QHash<QString, NodeItem *> m_nodes;      // by identity; dialed devices only
    QHash<QString, EdgeItem *> m_treeEdges;  // by child identity
    QVector<EdgeItem *> m_links;             // from map.json, once finished
    QHash<QString, QVector<QString>> m_linkNeighbors;
    QTimer m_layoutTimer;
    QElapsedTimer m_sinceLayout;
    bool m_finished = false;
};

}  // namespace omegamaps

#endif
