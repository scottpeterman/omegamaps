// app/topologypreview.cpp

#include "topologypreview.h"

#include <QGraphicsItem>
#include <QGraphicsScene>
#include <QHBoxLayout>
#include <QLabel>
#include <QLineEdit>
#include <QMap>
#include <QMouseEvent>
#include <QPainter>
#include <QPushButton>
#include <QRegularExpression>
#include <QResizeEvent>
#include <QScrollBar>
#include <QSet>
#include <QVBoxLayout>
#include <QStyleOptionGraphicsItem>
#include <QWheelEvent>

#include <algorithm>
#include <cmath>
#include <utility>

#include "icons.h"
#include "theme.h"

namespace omegamaps {

namespace {

// Scene geometry. The view scales; these are proportions.
constexpr qreal kIcon = 36;        // icon square
constexpr qreal kRing = 44;        // state ring around it
constexpr qreal kIconY = -8;       // icon centre, relative to the item origin
constexpr qreal kDx = 104;         // node pitch within a row
constexpr qreal kSubDy = 86;       // pitch between wrapped rows of one depth
constexpr qreal kLayerGap = 46;    // extra gap between depths
constexpr int kMaxPerRow = 16;     // wrap a depth wider than this
constexpr qint64 kLayoutEveryMs = 150;  // at most this often while rows arrive

// A label short enough to sit under an icon: the first two DNS labels, the
// full string for an address. The full name is in the tooltip.
QString shortLabel(const QString &display) {
    static const QRegularExpression ipv4(QStringLiteral("^\\d+\\.\\d+\\.\\d+\\.\\d+$"));
    if (ipv4.match(display).hasMatch() || display.contains(QLatin1Char(':'))) return display;
    const QStringList parts = display.split(QLatin1Char('.'));
    QString s = parts.size() > 2 ? parts.mid(0, 2).join(QLatin1Char('.')) : display;
    if (s.size() > 20) s = s.left(19) + QChar(0x2026);
    return s;
}

}  // namespace

// ---------------------------------------------------------------------------

class NodeItem : public QGraphicsItem {
public:
    explicit NodeItem(const RunRow &row) { setRow(row); setZValue(1); setAcceptHoverEvents(true); }

    void setRow(const RunRow &row) {
        const bool iconChanged = row.platform != m_row.platform || row.display != m_row.display;
        m_row = row;
        if (iconChanged || m_iconKey.isEmpty()) {
            m_iconKey = IconLibrary::instance().keyFor(row.platform, row.display);
            m_vendor = IconLibrary::instance().vendorColor(row.platform, row.display);
            m_label = shortLabel(row.display);
        }
        QString tip = QStringLiteral("<b>%1</b><br>%2").arg(row.display.toHtmlEscaped(),
                                                             stateName(row.state));
        if (!row.platform.isEmpty()) tip += QStringLiteral(" · ") + row.platform.toHtmlEscaped();
        if (!row.method.isEmpty()) tip += QStringLiteral(" · via ") + row.method;
        if (row.durationMs > 0) tip += QStringLiteral(" · ") + formatShort(row.durationMs);
        if (row.neighbors > 0) tip += QStringLiteral("<br>%1 neighbors").arg(row.neighbors);
        if (!row.detail.isEmpty() && row.state == RowState::Failed)
            tip += QStringLiteral("<br>") + row.detail.toHtmlEscaped();
        setToolTip(tip);
        update();
    }

    const RunRow &row() const { return m_row; }
    QPointF anchor() const { return pos() + QPointF(0, kIconY); }

    void setHighlighted(bool on) {
        if (on == m_highlight) return;
        m_highlight = on;
        update();
    }

    QRectF boundingRect() const override { return QRectF(-52, -34, 104, 72); }

    void paint(QPainter *p, const QStyleOptionGraphicsItem *, QWidget *) override {
        const Tokens &t = ThemeManager::instance().tokens();
        p->setRenderHint(QPainter::Antialiasing);
        p->setRenderHint(QPainter::SmoothPixmapTransform);

        const QRectF ring(-kRing / 2, kIconY - kRing / 2, kRing, kRing);
        const QRectF icon(-kIcon / 2, kIconY - kIcon / 2, kIcon, kIcon);

        QPen ringPen(m_vendor, 2);
        qreal iconOpacity = 1.0;
        switch (m_row.state) {
        case RowState::Reached: break;
        case RowState::Running:
            ringPen = QPen(t.accent, 2.5, Qt::DashLine);
            break;
        case RowState::Queued:
            ringPen = QPen(t.borderSecondary, 1.5, Qt::DashLine);
            iconOpacity = 0.55;
            break;
        case RowState::Failed:
            ringPen = QPen(t.danger, 2, Qt::DashLine);
            iconOpacity = 0.45;
            break;
        case RowState::NotDialed:
            ringPen = QPen(t.textMuted, 1, Qt::DotLine);
            iconOpacity = 0.4;
            break;
        }

        if (m_highlight) {
            p->setPen(QPen(t.accent, 4));
            p->setBrush(Qt::NoBrush);
            p->drawRoundedRect(ring.adjusted(-4, -4, 4, 4), 9, 9);
        }

        p->setPen(ringPen);
        p->setBrush(t.bgSecondary);
        p->drawRoundedRect(ring, 7, 7);

        const qreal scale = p->worldTransform().m11();
        const int px = std::clamp(int(std::ceil(kIcon * scale * 2)), 32, 160);
        p->setOpacity(iconOpacity);
        p->drawPixmap(icon, IconLibrary::instance().pixmap(m_iconKey, px), QRectF(0, 0, px, px));
        p->setOpacity(1.0);

        QFont f = p->font();
        f.setPointSizeF(7);
        p->setFont(f);
        const QFontMetricsF fm(f);
        const qreal w = fm.horizontalAdvance(m_label) + 8;
        const QRectF chip(-w / 2, kIconY + kRing / 2 + 4, w, fm.height() + 2);
        QColor chipBg = t.bgSecondary;
        chipBg.setAlpha(220);
        p->setPen(Qt::NoPen);
        p->setBrush(chipBg);
        p->drawRoundedRect(chip, 3, 3);
        p->setPen(m_row.state == RowState::Failed ? t.danger : t.textPrimary);
        p->drawText(chip, Qt::AlignCenter, m_label);
    }

private:
    RunRow m_row;
    QString m_iconKey;
    QString m_label;
    QColor m_vendor;
    bool m_highlight = false;
};

// ---------------------------------------------------------------------------

class EdgeItem : public QGraphicsItem {
public:
    enum Kind { Tree, Link };

    EdgeItem(NodeItem *a, NodeItem *b, Kind kind) : m_a(a), m_b(b), m_kind(kind) {
        setZValue(0);
        setAcceptHoverEvents(true);
        adjust();
    }

    void adjust() {
        prepareGeometryChange();
        m_line = QLineF(m_a->anchor(), m_b->anchor());
    }

    NodeItem *a() const { return m_a; }
    NodeItem *b() const { return m_b; }

    QRectF boundingRect() const override {
        return QRectF(m_line.p1(), m_line.p2()).normalized().adjusted(-3, -3, 3, 3);
    }

    QPainterPath shape() const override {
        QPainterPath path(m_line.p1());
        path.lineTo(m_line.p2());
        QPainterPathStroker stroker;
        stroker.setWidth(6);
        return stroker.createStroke(path);
    }

    void paint(QPainter *p, const QStyleOptionGraphicsItem *, QWidget *) override {
        const Tokens &t = ThemeManager::instance().tokens();
        p->setRenderHint(QPainter::Antialiasing);
        if (m_kind == Tree) {
            QPen pen(t.borderSecondary, 1.2, Qt::DashLine);
            pen.setCosmetic(true);
            p->setPen(pen);
        } else {
            QColor c = t.accent;
            c.setAlpha(isUnderMouse() ? 255 : 150);
            QPen pen(c, isUnderMouse() ? 2.2 : 1.3);
            pen.setCosmetic(true);
            p->setPen(pen);
        }
        p->drawLine(m_line);
    }

protected:
    void hoverEnterEvent(QGraphicsSceneHoverEvent *) override { update(); }
    void hoverLeaveEvent(QGraphicsSceneHoverEvent *) override { update(); }

private:
    NodeItem *m_a;
    NodeItem *m_b;
    Kind m_kind;
    QLineF m_line;
};

// ---------------------------------------------------------------------------

CanvasView::CanvasView(QGraphicsScene *scene, QWidget *parent) : QGraphicsView(scene, parent) {
    setProperty("role", QStringLiteral("canvas"));
    setRenderHints(QPainter::Antialiasing | QPainter::TextAntialiasing |
                   QPainter::SmoothPixmapTransform);
    setDragMode(QGraphicsView::ScrollHandDrag);
    setTransformationAnchor(QGraphicsView::AnchorUnderMouse);
    setViewportUpdateMode(QGraphicsView::BoundingRectViewportUpdate);
    setHorizontalScrollBarPolicy(Qt::ScrollBarAlwaysOff);
    setVerticalScrollBarPolicy(Qt::ScrollBarAlwaysOff);
}

void CanvasView::wheelEvent(QWheelEvent *e) {
    const qreal factor = std::pow(1.0015, e->angleDelta().y());
    const qreal current = transform().m11();
    const qreal next = std::clamp(current * factor, 0.05, 6.0);
    scale(next / current, next / current);
    m_userMoved = true;
    e->accept();
}

void CanvasView::resizeEvent(QResizeEvent *e) {
    QGraphicsView::resizeEvent(e);
    emit resized();
}

void CanvasView::mousePressEvent(QMouseEvent *e) {
    m_userMoved = true;
    QGraphicsView::mousePressEvent(e);
}

// ---------------------------------------------------------------------------

TopologyPreview::TopologyPreview(QWidget *parent) : Panel(QStringLiteral("Topology preview"), parent) {
    m_stats = new QLabel(this);
    m_stats->setProperty("tone", QStringLiteral("secondary"));
    barLayout()->addWidget(m_stats);

    m_live = new QLabel(QStringLiteral("\u25CF"), this);
    m_live->setProperty("tone", QStringLiteral("muted"));
    barLayout()->addWidget(m_live);

    m_search = new QLineEdit(this);
    m_search->setPlaceholderText(tr("Find device"));
    m_search->setClearButtonEnabled(true);
    m_search->setFixedWidth(170);
    barLayout()->addWidget(m_search);
    connect(m_search, &QLineEdit::textChanged, this, &TopologyPreview::applySearch);
    connect(m_search, &QLineEdit::returnPressed, this, &TopologyPreview::centerOnMatch);

    auto *fitBtn = new QPushButton(tr("Fit"), this);
    fitBtn->setCursor(Qt::PointingHandCursor);
    barLayout()->addWidget(fitBtn);
    connect(fitBtn, &QPushButton::clicked, this, [this] {
        m_view->clearUserMoved();
        fit();
    });

    m_scene = new QGraphicsScene(this);
    m_scene->setItemIndexMethod(QGraphicsScene::NoIndex);
    m_view = new CanvasView(m_scene, this);
    m_view->setMinimumHeight(160);
    bodyLayout()->setContentsMargins(10, 10, 10, 10);
    bodyLayout()->addWidget(m_view, 1);

    // A panel that changes size refits, unless the user has zoomed or panned:
    // the fit made for the old size clips the new one.
    connect(m_view, &CanvasView::resized, this, [this] {
        if (!m_view->userMoved()) fit();
    });

    m_layoutTimer.setSingleShot(true);
    connect(&m_layoutTimer, &QTimer::timeout, this, &TopologyPreview::relayout);

    connect(&ThemeManager::instance(), &ThemeManager::changed, this, [this] {
        m_scene->update();
        m_view->viewport()->update();
    });

    updateStats();
}

TopologyPreview::~TopologyPreview() = default;

void TopologyPreview::reset() {
    m_layoutTimer.stop();
    m_scene->clear();  // owns every item
    m_nodes.clear();
    m_treeEdges.clear();
    m_links.clear();
    m_linkNeighbors.clear();
    m_finished = false;
    m_view->clearUserMoved();
    m_view->resetTransform();
    setTone(m_live, QStringLiteral("muted"));
    updateStats();
}

void TopologyPreview::setFinished(bool finished) {
    m_finished = finished;
    setTone(m_live, finished ? QStringLiteral("success") : QStringLiteral("accent"));
    m_live->setToolTip(finished ? tr("Run complete") : tr("Run in progress"));
    updateStats();
}

void TopologyPreview::updateRows(const QVector<RunRow> &rows) {
    bool structural = false;
    for (const RunRow &r : rows) {
        // Not-dialed devices are never drawn. A row reaches that state inside
        // a single event, before any pull can see it queued, so nothing drawn
        // ever turns into one.
        if (!r.dialed()) continue;
        auto it = m_nodes.find(r.identity);
        if (it == m_nodes.end()) {
            auto *n = new NodeItem(r);
            // Hidden until a layout places it: a new item sits at the scene
            // origin, which is where the seed is drawn, and a depth admitted
            // in one batch would otherwise flash as a pile on top of it.
            n->setVisible(false);
            m_scene->addItem(n);
            m_nodes.insert(r.identity, n);
            structural = true;
        } else {
            if ((*it)->row().depth != r.depth || (*it)->row().via != r.via) structural = true;
            (*it)->setRow(r);
        }
    }

    // Reporting edges, for devices whose reporter is on the map. A reporter
    // arrives before what it reports, so one pass after the batch suffices.
    if (m_links.isEmpty()) {
        for (const RunRow &r : rows) {
            if (!r.dialed() || r.via.isEmpty() || m_treeEdges.contains(r.identity)) continue;
            NodeItem *child = m_nodes.value(r.identity);
            NodeItem *parent = m_nodes.value(r.via);
            if (!child || !parent) continue;
            auto *e = new EdgeItem(parent, child, EdgeItem::Tree);
            e->setVisible(parent->isVisible() && child->isVisible());
            m_scene->addItem(e);
            m_treeEdges.insert(r.identity, e);
            structural = true;
        }
    }

    if (!m_search->text().isEmpty()) applySearch(m_search->text());
    if (structural) scheduleLayout();
    updateStats();
}

void TopologyPreview::clearLinks() {
    for (EdgeItem *e : m_links) {
        m_scene->removeItem(e);
        delete e;
    }
    m_links.clear();
    m_linkNeighbors.clear();
}

int TopologyPreview::setFinalMap(const TopologyMap &map, int *unmatched) {
    clearLinks();

    // map.json is keyed by the name each device goes by; the run by its
    // claim identity. Either may be what the map used.
    QHash<QString, NodeItem *> byName;
    for (NodeItem *n : std::as_const(m_nodes)) {
        byName.insert(n->row().identity, n);
        if (!n->row().name.isEmpty()) byName.insert(n->row().name, n);
        byName.insert(n->row().display, n);
    }

    int missing = 0;
    QSet<QPair<NodeItem *, NodeItem *>> seen;
    QHash<QPair<NodeItem *, NodeItem *>, QStringList> linkText;
    for (auto it = map.nodes.constBegin(); it != map.nodes.constEnd(); ++it) {
        NodeItem *a = byName.value(it.key());
        if (!a) {
            ++missing;
            continue;
        }
        for (auto pit = it->peers.constBegin(); pit != it->peers.constEnd(); ++pit) {
            NodeItem *b = byName.value(pit.key());
            if (!b || b == a) continue;
            const auto key = a < b ? qMakePair(a, b) : qMakePair(b, a);
            QStringList &lines = linkText[key];
            for (const MapLink &l : pit->links) {
                // Both ends list each link; show it once, from the lesser end.
                if (a == key.first)
                    lines << QStringLiteral("%1  %2 \u2014 %3  %4")
                                 .arg(shortLabel(a->row().display), l.local, l.remote,
                                      shortLabel(b->row().display));
            }
            seen.insert(key);
        }
    }

    for (const auto &key : std::as_const(seen)) {
        auto *e = new EdgeItem(key.first, key.second, EdgeItem::Link);
        QStringList lines = linkText.value(key);
        lines.removeDuplicates();
        e->setToolTip(lines.isEmpty() ? key.first->row().display + QStringLiteral(" \u2014 ") +
                                            key.second->row().display
                                      : lines.join(QLatin1Char('\n')));
        m_scene->addItem(e);
        m_links.append(e);
        m_linkNeighbors[key.first->row().identity].append(key.second->row().identity);
        m_linkNeighbors[key.second->row().identity].append(key.first->row().identity);
    }

    // The links are the map now; the reporting tree was only how it was found.
    if (!m_links.isEmpty()) {
        for (EdgeItem *e : std::as_const(m_treeEdges)) {
            m_scene->removeItem(e);
            delete e;
        }
        m_treeEdges.clear();
    }

    if (unmatched) *unmatched = missing;
    scheduleLayout();
    updateStats();
    return m_links.size();
}

// Throttled, not debounced: during a busy depth rows change on every pull,
// and a debounce that restarts each time would never fire until the depth
// went quiet, leaving new devices unplaced (and so hidden) the whole while.
void TopologyPreview::scheduleLayout() {
    if (m_layoutTimer.isActive()) return;
    const qint64 since = m_sinceLayout.isValid() ? m_sinceLayout.elapsed() : 1 << 30;
    m_layoutTimer.start(int(std::max<qint64>(0, kLayoutEveryMs - since)));
}

// Rows by depth, each depth ordered under what it hangs from, wide depths
// wrapped. "Hangs from" is the reporting device during the run, and the mean
// position of shallower linked neighbours once the real links are known --
// which is what pulls a leaf pair under the spine pair that serves them
// rather than under whichever spine happened to report them first.
void TopologyPreview::relayout() {
    QMap<int, QVector<NodeItem *>> byDepth;
    for (NodeItem *n : std::as_const(m_nodes)) byDepth[n->row().depth].append(n);

    QHash<QString, qreal> placedX;
    qreal y = 0;
    for (auto it = byDepth.begin(); it != byDepth.end(); ++it) {
        QVector<NodeItem *> &layer = it.value();
        const int depth = it.key();

        QHash<NodeItem *, qreal> key;
        for (NodeItem *n : layer) {
            qreal sum = 0;
            int count = 0;
            for (const QString &nb : m_linkNeighbors.value(n->row().identity)) {
                NodeItem *other = m_nodes.value(nb);
                if (other && other->row().depth < depth && placedX.contains(nb)) {
                    sum += placedX.value(nb);
                    ++count;
                }
            }
            if (count == 0 && placedX.contains(n->row().via)) {
                sum = placedX.value(n->row().via);
                count = 1;
            }
            key.insert(n, count > 0 ? sum / count : 1e9);
        }
        std::sort(layer.begin(), layer.end(), [&](NodeItem *a, NodeItem *b) {
            const qreal ka = key.value(a), kb = key.value(b);
            if (!qFuzzyCompare(ka + 1, kb + 1)) return ka < kb;
            return a->row().display < b->row().display;
        });

        const int n = layer.size();
        const int rows = (n + kMaxPerRow - 1) / kMaxPerRow;
        for (int r = 0; r < rows; ++r) {
            const int from = r * kMaxPerRow;
            const int count = std::min(kMaxPerRow, n - from);
            // Alternate rows are offset by half a pitch so a wrapped depth
            // reads as one band rather than as two depths.
            const qreal offset = (r % 2) ? kDx / 2 : 0;
            for (int i = 0; i < count; ++i) {
                NodeItem *node = layer.at(from + i);
                const qreal x = (i - (count - 1) / 2.0) * kDx + offset;
                node->setPos(x, y + r * kSubDy);
                placedX.insert(node->row().identity, x);
            }
        }
        y += rows * kSubDy + kLayerGap;
    }

    for (NodeItem *n : std::as_const(m_nodes)) n->setVisible(true);
    for (EdgeItem *e : std::as_const(m_treeEdges)) {
        e->adjust();
        e->setVisible(true);
    }
    for (EdgeItem *e : std::as_const(m_links)) e->adjust();
    m_sinceLayout.restart();

    m_scene->setSceneRect(m_scene->itemsBoundingRect().adjusted(-40, -40, 40, 40));
    if (!m_view->userMoved()) fit();
}

void TopologyPreview::fit() {
    if (m_nodes.isEmpty()) return;
    const QRectF r = m_scene->itemsBoundingRect().adjusted(-30, -30, 30, 30);
    m_view->fitInView(r, Qt::KeepAspectRatio);
    // Never blow a handful of devices up to fill the canvas.
    const qreal s = m_view->transform().m11();
    if (s > 1.6) m_view->scale(1.6 / s, 1.6 / s);
}

void TopologyPreview::updateStats() {
    const int links = m_links.isEmpty() ? m_treeEdges.size() : m_links.size();
    const QString what = m_links.isEmpty() ? tr("Reported") : tr("Connections");
    m_stats->setText(tr("Devices: %1  |  %2: %3").arg(m_nodes.size()).arg(what).arg(links));
}

void TopologyPreview::applySearch(const QString &text) {
    const QString needle = text.trimmed();
    for (NodeItem *n : std::as_const(m_nodes)) {
        const bool hit = !needle.isEmpty() &&
                         (n->row().display.contains(needle, Qt::CaseInsensitive) ||
                          n->row().identity.contains(needle, Qt::CaseInsensitive));
        n->setHighlighted(hit);
    }
}

void TopologyPreview::centerOnMatch() {
    const QString needle = m_search->text().trimmed();
    if (needle.isEmpty()) return;
    NodeItem *exact = nullptr;
    NodeItem *shallowest = nullptr;
    for (NodeItem *n : std::as_const(m_nodes)) {
        const RunRow &r = n->row();
        if (r.display.compare(needle, Qt::CaseInsensitive) == 0 ||
            r.identity.compare(needle, Qt::CaseInsensitive) == 0) {
            exact = n;
            break;
        }
        if ((r.display.contains(needle, Qt::CaseInsensitive) ||
             r.identity.contains(needle, Qt::CaseInsensitive)) &&
            (!shallowest || r.depth < shallowest->row().depth))
            shallowest = n;
    }
    NodeItem *best = exact ? exact : shallowest;
    if (!best) return;
    m_view->markUserMoved();
    const qreal s = m_view->transform().m11();
    if (s < 1.0) m_view->scale(1.0 / s, 1.0 / s);
    m_view->centerOn(best);
}

}  // namespace omegamaps
