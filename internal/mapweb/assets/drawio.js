/**
 * internal/mapweb/assets/drawio.js
 *
 * Draw.io (.drawio / mxGraphModel) export for the topology viewer.
 *
 * The point of this export is not "the map, in another format" — that is what
 * the JSON button already does. It is "the map as I have it arranged right
 * now", so a diagram can be finished by hand in a tool built for diagrams.
 * Everything below follows from that:
 *
 *   - positions come from the live Cytoscape graph, not from a fresh layout
 *   - only VISIBLE nodes are exported, so the leaf and undiscovered filters
 *     mean the same thing here as they do on screen
 *   - vendor and role detection are DELEGATED to the viewer rather than
 *     reimplemented, so an exported node can never disagree with the node the
 *     user was looking at when they pressed the button
 *
 * This is also the native consumer of platform_map.json: the values in that
 * file are draw.io shape strings, and the viewer converts them to icon keys.
 * Here they are used as they are written. The viewer has already fetched and
 * parsed it, so it is read off the viewer rather than loaded a second time.
 *
 * Two shape modes. 'icons' draws mxgraph.cisco stencils, which look like a
 * network diagram and depend on the Cisco shape library being available in
 * whatever opens the file. 'shapes' draws plain geometry that any draw.io
 * renders, colour-coded by vendor. Icons by default; shapes when the stencils
 * come up blank.
 */

'use strict';

const DrawIOExport = {

  // ═══════════════════════════════════════════════════════════════
  // Shape, colour and geometry tables
  // ═══════════════════════════════════════════════════════════════

  // Role -> stencil. Tier 3 of the resolution below, and the whole of it when
  // platform_map.json is absent.
  _roleShapes: {
    'firewall': 'shape=mxgraph.cisco.security.firewall_2;',
    'router': 'shape=mxgraph.cisco.routers.router;',
    'l2-switch': 'shape=mxgraph.cisco.switches.workgroup_switch;',
    'l3-switch': 'shape=mxgraph.cisco.switches.layer_3_switch;',
  },

  // Role -> plain geometry, for the mode that needs no stencil library.
  _roleGeometry: {
    'firewall': 'shape=mxgraph.basic.octagon2;',
    'router': 'shape=hexagon;perimeter=hexagonPerimeter2;size=0.15;',
    'l2-switch': 'rounded=0;',
    'l3-switch': 'rounded=1;arcSize=12;',
  },

  // Vendor -> fill/stroke/font. Mid-tone rather than the viewer's dark-theme
  // palette: a draw.io canvas is white by default and the page's colours are
  // unreadable on it.
  _vendorStyle: {
    cisco: { fill: '#7ECDE8', stroke: '#036897', font: '#003B5C' },
    arista: { fill: '#7BC8A0', stroke: '#1A6B40', font: '#0E3D24' },
    juniper: { fill: '#F5B87A', stroke: '#C96A1F', font: '#5C3010' },
    paloalto: { fill: '#F5A08E', stroke: '#D04425', font: '#6B2010' },
    fortinet: { fill: '#F09088', stroke: '#C41E14', font: '#6B100A' },
    default: { fill: '#8BB8F0', stroke: '#3070CC', font: '#1A3060' },
  },

  // A node nobody dialled is drawn dashed and pale. It is a claim, not a fact,
  // and the diagram should keep saying so after it leaves this application.
  _undiscoveredStyle: { fill: '#FFE0E0', stroke: '#FF6B6B', font: '#993333' },

  NODE_WIDTH: 60,
  NODE_HEIGHT: 60,
  SHAPE_NODE_WIDTH: 140,
  SHAPE_NODE_HEIGHT: 60,

  GRID_COL_SPACING: 160,
  GRID_ROW_SPACING: 140,
  GRID_COLS: 5,
  GRID_OFFSET_X: 80,
  GRID_OFFSET_Y: 80,

  // Cytoscape packs tighter than draw.io does; without this the exported
  // diagram is legible only when zoomed in.
  POSITION_SCALE: 1.8,


  // ═══════════════════════════════════════════════════════════════
  // Shape resolution — three tiers, mirroring the viewer's icon path
  // ═══════════════════════════════════════════════════════════════

  /**
   * Tier 1: exact platform_patterns hit, longest pattern first.
   * Tier 2: fallback_patterns against platform and hostname.
   * Tier 3: role default.
   *
   * @param {Object|null} platformMap  the viewer's parsed platform_map.json
   * @param {string} role              the viewer's own role verdict
   */
  _resolveShape(platformMap, platform, nodeId, role) {
    if (platformMap && platformMap.platform_patterns) {
      const patterns = Object.entries(platformMap.platform_patterns)
        .filter(([k]) => !k.startsWith('_comment'))
        .sort((a, b) => b[0].length - a[0].length);

      for (const [pattern, shape] of patterns) {
        if ((platform || '').includes(pattern)) return this._terminate(shape);
      }
    }

    if (platformMap && platformMap.fallback_patterns) {
      const p = (platform || '').toLowerCase();
      const n = (nodeId || '').toLowerCase();

      for (const config of Object.values(platformMap.fallback_patterns)) {
        const platMatch = (config.platform_patterns || []).some(x => p.includes(x));
        const nameMatch = (config.name_patterns || []).some(x => n.includes(x));
        if ((platMatch || nameMatch) && config.shape) return this._terminate(config.shape);
      }
    }

    return this._roleShapes[role] || this._roleShapes['l3-switch'];
  },

  // Shape strings in platform_map.json are written without the separator that
  // the rest of the style needs after them.
  _terminate(shape) {
    return shape.endsWith(';') ? shape : shape + ';';
  },


  // ═══════════════════════════════════════════════════════════════
  // Style builders
  // ═══════════════════════════════════════════════════════════════

  _buildNodeStyle(platformMap, node, useShapes) {
    if (!node.discovered) {
      const s = this._undiscoveredStyle;
      const width = useShapes ? 'strokeWidth=2;' : '';
      return 'rounded=1;whiteSpace=wrap;html=1;dashed=1;dashPattern=8 4;' +
        `fillColor=${s.fill};strokeColor=${s.stroke};fontColor=${s.font};` +
        `fontSize=10;fontStyle=1;verticalAlign=middle;${width}`;
    }

    const colors = this._vendorStyle[node.vendor] || this._vendorStyle.default;

    if (useShapes) {
      const geometry = this._roleGeometry[node.role] || this._roleGeometry['l3-switch'];
      return `${geometry}whiteSpace=wrap;html=1;` +
        `fillColor=${colors.fill};strokeColor=${colors.stroke};fontColor=${colors.font};` +
        'fontSize=9;fontStyle=1;strokeWidth=2;verticalAlign=middle;';
    }

    const shape = this._resolveShape(platformMap, node.platform, node.id, node.role);
    return `${shape}fillColor=${colors.fill};strokeColor=${colors.stroke};fontColor=${colors.font};` +
      'fontSize=10;fontStyle=1;verticalLabelPosition=bottom;verticalAlign=top;html=1;';
  },

  _edgeStyle:
    'rounded=1;orthogonalLoop=1;jettySize=auto;html=1;strokeColor=#4A9EFF;' +
    'strokeWidth=1;fontSize=8;fontColor=#333333;labelBackgroundColor=#FFFFFF;' +
    'endArrow=none;startArrow=none;',


  // ═══════════════════════════════════════════════════════════════
  // Positions
  // ═══════════════════════════════════════════════════════════════

  /**
   * Cytoscape positions are node-centre and may be negative; draw.io wants
   * top-left and non-negative. Translate to the bounding box, scale, then
   * shift by half the node so the centres still line up.
   *
   * With no positions at all — an export before the graph existed — fall back
   * to a grid, which is ugly and readable rather than a pile at the origin.
   */
  _buildPositions(nodeIds, cyPositions, nodeW, nodeH) {
    const positions = {};

    if (!cyPositions || Object.keys(cyPositions).length === 0) {
      nodeIds.forEach((id, idx) => {
        positions[id] = {
          x: this.GRID_OFFSET_X + (idx % this.GRID_COLS) * this.GRID_COL_SPACING,
          y: this.GRID_OFFSET_Y + Math.floor(idx / this.GRID_COLS) * this.GRID_ROW_SPACING,
        };
      });
      return positions;
    }

    let minX = Infinity, minY = Infinity;
    for (const id of nodeIds) {
      const pos = cyPositions[id];
      if (pos) {
        minX = Math.min(minX, pos.x);
        minY = Math.min(minY, pos.y);
      }
    }

    const halfW = nodeW / 2;
    const halfH = nodeH / 2;

    for (const id of nodeIds) {
      const pos = cyPositions[id];
      positions[id] = pos
        ? {
          x: Math.round((pos.x - minX) * this.POSITION_SCALE + this.GRID_OFFSET_X - halfW),
          y: Math.round((pos.y - minY) * this.POSITION_SCALE + this.GRID_OFFSET_Y - halfH),
        }
        : { x: this.GRID_OFFSET_X, y: this.GRID_OFFSET_Y };
    }
    return positions;
  },


  // ═══════════════════════════════════════════════════════════════
  // XML
  // ═══════════════════════════════════════════════════════════════

  // Device names are neighbour-reported strings. They are markup here in the
  // same way they are markup in the device list, and get the same treatment.
  _escapeXml(str) {
    if (str === null || str === undefined) return '';
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&apos;');
  },

  _nodeLabel(node) {
    if (!node.discovered) return this._escapeXml(node.label) + '&#xa;?';

    let label = this._escapeXml(node.label);
    if (node.ip) label += '&#xa;' + this._escapeXml(node.ip);
    if (node.platform) {
      const plat = node.platform.length > 24
        ? node.platform.slice(0, 22) + '…'
        : node.platform;
      label += '&#xa;' + this._escapeXml(plat);
    }
    return label;
  },


  // ═══════════════════════════════════════════════════════════════
  // Reading the graph
  // ═══════════════════════════════════════════════════════════════

  /**
   * Pull the export set out of a live viewer: every visible node, its position,
   * and the viewer's own vendor and role verdicts. Nothing here re-derives what
   * the viewer already decided.
   */
  _readViewer(viewer) {
    const nodes = [];
    const positions = {};
    const visible = new Set();

    viewer.cy.nodes(':visible').forEach(n => {
      const id = n.id();
      const platform = n.data('platform') || '';
      visible.add(id);
      positions[id] = { x: n.position().x, y: n.position().y };
      nodes.push({
        id: id,
        label: n.data('label') || id,
        ip: n.data('ip') || '',
        platform: platform === 'Unknown' ? '' : platform,
        discovered: !!n.data('discovered'),
        vendor: viewer._detectVendor(platform, id),
        role: viewer._detectDeviceRole(platform, id),
      });
    });

    return { nodes, positions, visible };
  },

  /**
   * Edges come from the map rather than from Cytoscape so the interface-pair
   * labels survive; the visible set is what filters them.
   *
   * A link with several member interfaces gets one pair per line. The break
   * has to be the XML entity, not a literal newline: draw.io renders the label
   * as HTML and a raw newline collapses to a space, which turns two pairs into
   * one unreadable run-on.
   */
  _readEdges(mapData, visible) {
    const edges = [];
    const seen = new Set();

    for (const [deviceName, deviceData] of Object.entries(mapData)) {
      if (!visible.has(deviceName)) continue;

      for (const [peerName, peerData] of Object.entries(deviceData.peers || {})) {
        if (!visible.has(peerName)) continue;

        const key = [deviceName, peerName].sort().join('--');
        if (seen.has(key)) continue;
        seen.add(key);

        const connections = (peerData.connections || []).filter(c => c && c.length >= 2);
        const label = connections
          .map(c => `${this._escapeXml(c[0])} \u2194 ${this._escapeXml(c[1])}`)
          .join('&#xa;');
        edges.push({ source: deviceName, target: peerName, label });
      }
    }
    return edges;
  },


  // ═══════════════════════════════════════════════════════════════
  // Export
  // ═══════════════════════════════════════════════════════════════

  /**
   * Generate draw.io XML for what the viewer is currently showing.
   *
   * @param {TopologyViewer} viewer
   * @param {Object}  [options]
   * @param {string}  [options.title]      diagram name, default 'Topology'
   * @param {string}  [options.shapeMode]  'icons' (default) or 'shapes'
   * @returns {string} .drawio XML
   */
  generate(viewer, options = {}) {
    if (!viewer || !viewer.cy || !viewer.rawData) {
      throw new Error('no map loaded');
    }

    const title = options.title || 'Topology';
    const useShapes = options.shapeMode === 'shapes';
    const nodeW = useShapes ? this.SHAPE_NODE_WIDTH : this.NODE_WIDTH;
    const nodeH = useShapes ? this.SHAPE_NODE_HEIGHT : this.NODE_HEIGHT;
    const platformMap = viewer._platformMap || null;

    const { nodes, positions: cyPositions, visible } = this._readViewer(viewer);
    if (nodes.length === 0) throw new Error('nothing visible to export');

    const edges = this._readEdges(viewer.rawData, visible);
    const positions = this._buildPositions(nodes.map(n => n.id), cyPositions, nodeW, nodeH);

    // 0 and 1 are draw.io's own root cells; everything else counts from 2.
    let nextId = 2;
    const cellIds = {};
    for (const node of nodes) cellIds[node.id] = nextId++;

    const xml = [];
    xml.push('<?xml version="1.0" encoding="UTF-8"?>');
    xml.push(`<mxfile host="omegamaps" modified="${new Date().toISOString()}" type="device">`);
    xml.push(`  <diagram name="${this._escapeXml(title)}" id="topology">`);
    xml.push('    <mxGraphModel dx="1200" dy="800" grid="1" gridSize="10" guides="1" tooltips="1" connect="1" arrows="1" fold="1" page="0" pageScale="1" pageWidth="1169" pageHeight="827" math="0" shadow="0">');
    xml.push('      <root>');
    xml.push('        <mxCell id="0"/>');
    xml.push('        <mxCell id="1" parent="0"/>');

    for (const node of nodes) {
      const pos = positions[node.id] || { x: 0, y: 0 };
      const style = this._buildNodeStyle(platformMap, node, useShapes);
      xml.push(`        <mxCell id="${cellIds[node.id]}" value="${this._nodeLabel(node)}" style="${style}" vertex="1" parent="1">`);
      xml.push(`          <mxGeometry x="${pos.x}" y="${pos.y}" width="${nodeW}" height="${nodeH}" as="geometry"/>`);
      xml.push('        </mxCell>');
    }

    for (const edge of edges) {
      const src = cellIds[edge.source];
      const tgt = cellIds[edge.target];
      if (src === undefined || tgt === undefined) continue;
      xml.push(`        <mxCell id="${nextId++}" value="${edge.label}" style="${this._edgeStyle}" edge="1" parent="1" source="${src}" target="${tgt}">`);
      xml.push('          <mxGeometry relative="1" as="geometry"/>');
      xml.push('        </mxCell>');
    }

    xml.push('      </root>');
    xml.push('    </mxGraphModel>');
    xml.push('  </diagram>');
    xml.push('</mxfile>');

    return xml.join('\n');
  },

  /**
   * Generate and hand the browser a file. Returns the node count so the caller
   * can say what was written; throws with a usable message when it cannot.
   */
  download(viewer, mapName, shapeMode) {
    const title = (mapName || 'map').replace(/\.json$/i, '');
    const xml = this.generate(viewer, { title: title, shapeMode: shapeMode });

    const blob = new Blob([xml], { type: 'application/xml' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = title + '.drawio';
    a.click();
    URL.revokeObjectURL(a.href);

    return viewer.cy.nodes(':visible').length;
  },
};

if (typeof window !== 'undefined') window.DrawIOExport = DrawIOExport;
if (typeof module !== 'undefined' && module.exports) module.exports = { DrawIOExport };