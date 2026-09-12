/**
 * internal/mapweb/assets/viewer.js
 *
 * Cytoscape topology viewer. Renders one map.json: crawled devices as nodes,
 * neighbour claims as edges, everything the crawler never dialled as a leaf
 * that is drawn differently and is still clickable.
 *
 * Icon pipeline:
 *   SVG string -> offscreen canvas -> PNG data URI -> Cytoscape
 *   Rasterized once at 200x200 (4x node size) so the icons stay crisp from
 *   0.1x to 4x zoom. The SVG sources are inline below rather than fetched,
 *   which is what makes the page work with nothing but this file.
 *
 * Layouts are cytoscape.js core only (breadthfirst, cose, concentric, circle,
 * grid). dagre / fcose / cola are extensions this build does not ship; the
 * configs below still name them and fall back to a core layout, so dropping
 * the script tag in is the only thing needed to turn one on.
 *
 * Usage:
 *   const viewer = new TopologyViewer('cy-container-id');
 *   viewer.init();
 *   await viewer.preloadIcons();
 *   viewer.loadTopology(mapData);
 */

'use strict';

// Register layout extensions
if (typeof cytoscape !== 'undefined') {
  if (typeof cytoscapeDagre !== 'undefined') cytoscape.use(cytoscapeDagre);
  if (typeof cytoscapeFcose !== 'undefined') cytoscape.use(cytoscapeFcose);
  if (typeof cytoscapeCola !== 'undefined') cytoscape.use(cytoscapeCola);
}


class TopologyViewer {

  // ── Shared PNG data URI cache (survives across instances) ──
  static _iconDataUriCache = new Map();

  constructor(containerId, options = {}) {
    this.containerId = containerId;
    this.cy = null;
    this.dagreAvailable = typeof cytoscapeDagre !== 'undefined';
    this.fcoseAvailable = typeof cytoscapeFcose !== 'undefined';
    this.colaAvailable = typeof cytoscapeCola !== 'undefined';
    this.selectedNode = null;
    this.rawData = null;

    // Optional platform map (assets/platform_map.json). Without it the
    // viewer still picks an icon, just from the platform string alone.
    this._platformMap = null;

    // Filter state
    this.hideUndiscovered = true;
    this.hideLeafNodes = true;

    // Callbacks
    this.onNodeSelect = options.onNodeSelect || null;
    this.onEdgeSelect = options.onEdgeSelect || null;
    this.onStatsUpdate = options.onStatsUpdate || null;
    this.onFilterUpdate = options.onFilterUpdate || null;
  }


  // ═══════════════════════════════════════════════════════════════
  // Embedded SVG Icon Sources
  //
  // Cisco-style network device icons, 48×48 viewBox.
  // Stroke color #2d5f8a is mid-tone blue — visible on both
  // light (#fff) and dark (#1c2127) canvas backgrounds.
  // ═══════════════════════════════════════════════════════════════

  static _embeddedSvgs = {

    // ── Router: circle with crosshairs (classic Cisco symbol) ──
    'router': `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">
      <circle cx="24" cy="24" r="16" fill="#fff" stroke="#2d5f8a" stroke-width="2.5"/>
      <line x1="8" y1="24" x2="40" y2="24" stroke="#2d5f8a" stroke-width="2"/>
      <line x1="24" y1="8" x2="24" y2="40" stroke="#2d5f8a" stroke-width="2"/>
      <polyline points="14,20 8,24 14,28" fill="none" stroke="#2d5f8a" stroke-width="1.8"/>
      <polyline points="34,20 40,24 34,28" fill="none" stroke="#2d5f8a" stroke-width="1.8"/>
      <polyline points="20,14 24,8 28,14" fill="none" stroke="#2d5f8a" stroke-width="1.8"/>
      <polyline points="20,34 24,40 28,34" fill="none" stroke="#2d5f8a" stroke-width="1.8"/>
    </svg>`,

    // ── L3 Switch: box with routing arrows ──
    'layer-3-switch': `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">
      <rect x="4" y="10" width="40" height="28" rx="3" fill="#fff" stroke="#2d5f8a" stroke-width="2.5"/>
      <line x1="4" y1="22" x2="44" y2="22" stroke="#2d5f8a" stroke-width="1.5"/>
      <circle cx="12" cy="16" r="2.5" fill="#2d5f8a"/>
      <circle cx="20" cy="16" r="2.5" fill="#2d5f8a"/>
      <circle cx="28" cy="16" r="2.5" fill="#2d5f8a"/>
      <circle cx="36" cy="16" r="2.5" fill="#2d5f8a"/>
      <line x1="16" y1="30" x2="32" y2="30" stroke="#2d5f8a" stroke-width="1.8"/>
      <polyline points="28,27 32,30 28,33" fill="none" stroke="#2d5f8a" stroke-width="1.8"/>
      <line x1="24" y1="30" x2="24" y2="36" stroke="#2d5f8a" stroke-width="1.8"/>
      <polyline points="21,33 24,36 27,33" fill="none" stroke="#2d5f8a" stroke-width="1.8"/>
    </svg>`,

    // ── L2 Switch (workgroup): box with port indicators ──
    'workgroup-switch': `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">
      <rect x="4" y="14" width="40" height="20" rx="3" fill="#fff" stroke="#2d5f8a" stroke-width="2.5"/>
      <rect x="10" y="19" width="4" height="10" rx="1" fill="#2d5f8a" opacity="0.8"/>
      <rect x="17" y="19" width="4" height="10" rx="1" fill="#2d5f8a" opacity="0.8"/>
      <rect x="24" y="19" width="4" height="10" rx="1" fill="#2d5f8a" opacity="0.8"/>
      <rect x="31" y="19" width="4" height="10" rx="1" fill="#2d5f8a" opacity="0.8"/>
      <line x1="12" y1="19" x2="12" y2="17" stroke="#2d5f8a" stroke-width="1.2"/>
      <line x1="19" y1="19" x2="19" y2="17" stroke="#2d5f8a" stroke-width="1.2"/>
      <line x1="26" y1="19" x2="26" y2="17" stroke="#2d5f8a" stroke-width="1.2"/>
      <line x1="33" y1="19" x2="33" y2="17" stroke="#2d5f8a" stroke-width="1.2"/>
    </svg>`,

    // ── Firewall: brick wall pattern ──
    'firewall': `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">
      <rect x="4" y="8" width="40" height="32" rx="3" fill="#fff" stroke="#b83a2a" stroke-width="2.5"/>
      <line x1="4" y1="16" x2="44" y2="16" stroke="#b83a2a" stroke-width="1.5"/>
      <line x1="4" y1="24" x2="44" y2="24" stroke="#b83a2a" stroke-width="1.5"/>
      <line x1="4" y1="32" x2="44" y2="32" stroke="#b83a2a" stroke-width="1.5"/>
      <line x1="16" y1="8" x2="16" y2="16" stroke="#b83a2a" stroke-width="1.2"/>
      <line x1="32" y1="8" x2="32" y2="16" stroke="#b83a2a" stroke-width="1.2"/>
      <line x1="10" y1="16" x2="10" y2="24" stroke="#b83a2a" stroke-width="1.2"/>
      <line x1="24" y1="16" x2="24" y2="24" stroke="#b83a2a" stroke-width="1.2"/>
      <line x1="38" y1="16" x2="38" y2="24" stroke="#b83a2a" stroke-width="1.2"/>
      <line x1="16" y1="24" x2="16" y2="32" stroke="#b83a2a" stroke-width="1.2"/>
      <line x1="32" y1="24" x2="32" y2="32" stroke="#b83a2a" stroke-width="1.2"/>
      <line x1="10" y1="32" x2="10" y2="40" stroke="#b83a2a" stroke-width="1.2"/>
      <line x1="24" y1="32" x2="24" y2="40" stroke="#b83a2a" stroke-width="1.2"/>
      <line x1="38" y1="32" x2="38" y2="40" stroke="#b83a2a" stroke-width="1.2"/>
    </svg>`,

    // ── Multilayer Switch: stacked box with L3 arrows ──
    'multilayer-switch': `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">
      <rect x="6" y="6" width="38" height="24" rx="3" fill="#e8edf2" stroke="#2d5f8a" stroke-width="1.5"/>
      <rect x="4" y="10" width="38" height="24" rx="3" fill="#f0f3f7" stroke="#2d5f8a" stroke-width="1.5"/>
      <rect x="2" y="14" width="38" height="24" rx="3" fill="#fff" stroke="#2d5f8a" stroke-width="2.5"/>
      <line x1="2" y1="24" x2="40" y2="24" stroke="#2d5f8a" stroke-width="1.5"/>
      <circle cx="10" cy="19" r="2" fill="#2d5f8a"/>
      <circle cx="18" cy="19" r="2" fill="#2d5f8a"/>
      <circle cx="26" cy="19" r="2" fill="#2d5f8a"/>
      <circle cx="34" cy="19" r="2" fill="#2d5f8a"/>
      <line x1="14" y1="30" x2="28" y2="30" stroke="#2d5f8a" stroke-width="1.8"/>
      <polyline points="24,27 28,30 24,33" fill="none" stroke="#2d5f8a" stroke-width="1.8"/>
      <line x1="21" y1="30" x2="21" y2="36" stroke="#2d5f8a" stroke-width="1.8"/>
      <polyline points="18,33 21,36 24,33" fill="none" stroke="#2d5f8a" stroke-width="1.8"/>
    </svg>`,

    // ── Server / endpoint: rack unit with drive bays ──
    'server': `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">
      <rect x="10" y="6" width="28" height="36" rx="3" fill="#fff" stroke="#2d5f8a" stroke-width="2.5"/>
      <line x1="10" y1="18" x2="38" y2="18" stroke="#2d5f8a" stroke-width="1.5"/>
      <line x1="10" y1="30" x2="38" y2="30" stroke="#2d5f8a" stroke-width="1.5"/>
      <circle cx="15" cy="12" r="1.6" fill="#2d5f8a"/>
      <circle cx="15" cy="24" r="1.6" fill="#2d5f8a"/>
      <circle cx="15" cy="36" r="1.6" fill="#2d5f8a"/>
      <line x1="21" y1="12" x2="33" y2="12" stroke="#2d5f8a" stroke-width="1.4" opacity="0.7"/>
      <line x1="21" y1="24" x2="33" y2="24" stroke="#2d5f8a" stroke-width="1.4" opacity="0.7"/>
      <line x1="21" y1="36" x2="33" y2="36" stroke="#2d5f8a" stroke-width="1.4" opacity="0.7"/>
    </svg>`,

    // ── Access point: puck with radiating waves ──
    'access-point': `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">
      <ellipse cx="24" cy="34" rx="15" ry="6" fill="#fff" stroke="#2d5f8a" stroke-width="2.5"/>
      <line x1="24" y1="28" x2="24" y2="18" stroke="#2d5f8a" stroke-width="2"/>
      <path d="M16 16 A11 11 0 0 1 32 16" fill="none" stroke="#2d5f8a" stroke-width="1.8"/>
      <path d="M19 20 A7 7 0 0 1 29 20" fill="none" stroke="#2d5f8a" stroke-width="1.8"/>
      <circle cx="24" cy="34" r="2" fill="#2d5f8a"/>
    </svg>`,

    // ── Undiscovered: dashed box with question mark ──
    'undiscovered': `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">
      <rect x="4" y="14" width="40" height="20" rx="3" fill="#f5f5f5" stroke="#999" stroke-width="2" stroke-dasharray="4,2"/>
      <text x="24" y="29" text-anchor="middle" fill="#999" font-size="16" font-weight="bold" font-family="sans-serif">?</text>
    </svg>`,
  };


  // ═══════════════════════════════════════════════════════════════
  // SVG → High-Res PNG Rasterization
  //
  // Identical to SCJS: Cytoscape's canvas renderer rasterizes SVG
  // background images once at the node's CSS pixel size, then
  // scales that bitmap during zoom.  At high zoom the stretched
  // bitmap looks terrible.
  //
  // Fix: pre-rasterize each SVG to a high-resolution PNG (4× the
  // node size) via an offscreen canvas, then hand that PNG data
  // URI to Cytoscape.  The oversampled bitmap stays crisp across
  // the full 0.1–4× zoom range.
  // ═══════════════════════════════════════════════════════════════

  /** Rasterize an SVG string to a PNG data URI at the given pixel size. */
  _rasterizeSvg(svgText, size = 200) {
    return new Promise((resolve, reject) => {
      const dataUri = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svgText.trim());
      const img = new Image();
      img.onload = () => {
        const canvas = document.createElement('canvas');
        canvas.width = size;
        canvas.height = size;
        const ctx = canvas.getContext('2d');
        ctx.drawImage(img, 0, 0, size, size);
        resolve(canvas.toDataURL('image/png'));
      };
      img.onerror = (e) => {
        reject(new Error('SVG rasterize failed'));
      };
      img.src = dataUri;
    });
  }

  /**
   * Pre-rasterize all embedded SVG icons to PNG data URIs.
   * Call once at startup, before loading any topology.
   * Results are cached in the static _iconDataUriCache (shared across instances).
   */
  async preloadIcons() {
    const cache = TopologyViewer._iconDataUriCache;
    const svgs = TopologyViewer._embeddedSvgs;
    const pending = [];

    for (const [name, svg] of Object.entries(svgs)) {
      if (cache.has(name)) continue;
      const p = this._rasterizeSvg(svg, 200)
        .then(pngUri => {
          cache.set(name, pngUri);
        })
        .catch(e => {
          console.error(`[icon] ✗ rasterize failed: "${name}" — ${e.message}`);
        });
      pending.push(p);
    }

    if (pending.length) await Promise.all(pending);
    console.log(`[icon] preloadIcons complete: ${cache.size} icons cached`);
  }

  /**
   * Get pre-rasterized PNG data URI for an icon name.
   * Returns empty string if not yet cached (call preloadIcons first).
   */
  _getCachedIcon(name) {
    return TopologyViewer._iconDataUriCache.get(name) || '';
  }


  // ═══════════════════════════════════════════════════════════════
  // Initialization
  // ═══════════════════════════════════════════════════════════════

  init() {
    this.cy = cytoscape({
      container: document.getElementById(this.containerId),
      elements: [],
      style: this._getStyles(),
      layout: { name: 'preset' },
      minZoom: 0.1,
      maxZoom: 4,
      textureOnViewport: false,
      pixelRatio: 'auto',
    });

    this._setupEventHandlers();
    return this;
  }

  _getStyles() {
    const dark = document.documentElement.dataset.theme === 'dark';
    // A host that sets a theme (the Qt viewer, from the application's own
    // palette) supplies the graph colours too; a browser gets the built-in
    // pair.
    const pal = (window.omegamapsTheme && window.omegamapsTheme.cy) || {};
    // Labels sit on top of edges and icons, so they need a chip behind
    // them to stay readable — but the chip is the page's own panel colour,
    // not a white card punched into a dark canvas.
    const textColor = pal.text || (dark ? '#ffffff' : '#1c2127');
    const textBg = pal.textBg || (dark ? '#12151a' : '#ffffff');
    const edgeColor = pal.edge || (dark ? '#4c90f0' : '#2d72d2');
    const selectColor = pal.select || (dark ? '#4c90f0' : '#2d72d2');
    const undiscColor = pal.undiscovered || (dark ? '#e55656' : '#c23030');

    return [
      {
        selector: 'node',
        style: {
          'shape': 'round-rectangle',
          // The tile behind the icon. It was 'transparent', which Cytoscape
          // reads as black at full background-opacity: invisible on the
          // dark canvas it was drawn on, a black block on a light one. The
          // label chip's colour keeps the dark look and suits the light.
          'background-color': textBg,
          'background-image': 'data(icon)',
          'background-fit': 'contain',
          'background-clip': 'node',
          'background-width': '100%',
          'background-height': '100%',
          'width': 50,
          'height': 50,
          'label': 'data(label)',
          'text-valign': 'bottom',
          'text-halign': 'center',
          'text-margin-y': 5,
          'font-size': '10px',
          'font-weight': '500',
          'font-family': "'Roboto Mono', 'Consolas', monospace",
          'color': textColor,
          'text-background-color': textBg,
          'text-background-opacity': 0.75,
          'text-background-padding': '3px',
          'text-background-shape': 'roundrectangle',
          'border-width': 2,
          'border-color': 'data(vendorColor)',
          'border-opacity': 0.8,
        },
      },
      {
        selector: 'node[?discovered]',
        style: { 'border-style': 'solid' },
      },
      {
        selector: 'node[!discovered]',
        style: {
          'border-style': 'dashed',
          'border-color': undiscColor,
          'border-width': 2,
          'border-opacity': 0.8,
          'opacity': 0.6,
        },
      },
      {
        selector: 'node:selected',
        style: {
          'border-width': 4,
          'border-opacity': 1,
        },
      },
      {
        selector: 'edge',
        style: {
          'width': 2,
          'line-color': edgeColor,
          'curve-style': 'bezier',
          'label': 'data(label)',
          'text-wrap': 'wrap',
          'font-size': '8px',
          'font-family': "'Roboto Mono', 'Consolas', monospace",
          'color': textColor,
          'text-background-color': textBg,
          'text-background-opacity': 0.7,
          'text-background-padding': '2px',
          'text-background-shape': 'roundrectangle',
          'text-rotation': 'autorotate',
          'text-margin-y': -8,
        },
      },
      {
        // A bundled link reads as one link at a distance. Thicker line.
        selector: 'edge[connectionCount > 1]',
        style: { 'width': 3 },
      },
      {
        selector: 'edge:selected',
        style: { 'width': 3, 'line-color': selectColor },
      },
    ];
  }

  // Re-reads the colours after a theme change, without a relayout.
  restyle() {
    if (this.cy) this.cy.style(this._getStyles());
  }

  _setupEventHandlers() {
    if (!this.cy) return;

    this.cy.on('tap', 'node', (evt) => {
      const data = evt.target.data();
      this.selectedNode = data.id;
      if (this.onNodeSelect) this.onNodeSelect(data);
    });

    this.cy.on('tap', 'edge', (evt) => {
      const data = evt.target.data();
      if (this.onEdgeSelect) this.onEdgeSelect(data);
    });

    this.cy.on('tap', (evt) => {
      if (evt.target === this.cy) {
        this.selectedNode = null;
        if (this.onNodeSelect) this.onNodeSelect(null);
      }
    });
  }


  // ═══════════════════════════════════════════════════════════════
  // Data Loading
  // ═══════════════════════════════════════════════════════════════

  loadTopology(data) {
    this.rawData = data;

    // Map format only. Domain suffixes are stripped by the crawler when the
    // map is generated, so there is nothing to normalize here.
    if (typeof data !== 'object' || data === null || Array.isArray(data)) {
      console.error('[map] not a topology map');
      return;
    }
    const elements = this._parseMapFormat(data);

    if (this.cy) {
      this.cy.destroy();
      this.cy = null;
    }

    this.cy = cytoscape({
      container: document.getElementById(this.containerId),
      elements: elements,
      style: this._getStyles(),
      layout: { name: 'preset' },
      minZoom: 0.1,
      maxZoom: 4,
      textureOnViewport: false,
      pixelRatio: 'auto',
    });

    this._setupEventHandlers();
    this._applyFilters();

    if (this.cy.nodes(':visible').length > 0) {
      const visibleEles = this.cy.elements(':visible');
      const layoutConfig = this.dagreAvailable
        ? { name: 'dagre', rankDir: 'TB', nodeSep: 60, rankSep: 80, edgeSep: 10, ranker: 'network-simplex', animate: false, fit: true, padding: 30 }
        : { name: 'breadthfirst', directed: true, spacingFactor: 1.5, animate: false, fit: true, padding: 30 };
      visibleEles.layout(layoutConfig).run();
    }

    this._emitStats();
  }


  // ═══════════════════════════════════════════════════════════════
  // Format Parsers
  // ═══════════════════════════════════════════════════════════════

  _parseMapFormat(data) {
    const elements = [];
    const addedEdges = new Set();
    const nodeIds = new Set();

    for (const [deviceName, deviceData] of Object.entries(data)) {
      const details = deviceData.node_details || {};
      nodeIds.add(deviceName);

      elements.push({
        group: 'nodes',
        data: {
          id: deviceName,
          label: deviceName,
          ip: details.ip || '',
          platform: details.platform || 'Unknown',
          icon: this._getIconForPlatform(details.platform, deviceName),
          discovered: true,
          vendorColor: this._getVendorColor(details.platform, deviceName),
          vendorFill: this._getVendorFill(details.platform, deviceName),
        },
      });
    }

    for (const [deviceName, deviceData] of Object.entries(data)) {
      const peers = deviceData.peers || {};

      for (const [peerName, peerData] of Object.entries(peers)) {
        if (!nodeIds.has(peerName)) {
          nodeIds.add(peerName);
          elements.push({
            group: 'nodes',
            data: {
              id: peerName,
              label: peerName + ' ⚠',
              ip: '', platform: 'Undiscovered',
              icon: this._getCachedIcon('undiscovered'),
              discovered: false,
              vendorColor: '#c23030',
              vendorFill: 'rgba(194,48,48,0.1)',
            },
          });
        }

        const edgeId = [deviceName, peerName].sort().join('--');
        if (!addedEdges.has(edgeId)) {
          addedEdges.add(edgeId);
          // Every connection, one per line — not just the first. A bundle
          // between two devices is one edge with several member interfaces,
          // and showing only connections[0] hides the rest of the bundle
          // while looking like a complete answer. Rendering needs
          // 'text-wrap': 'wrap' in the edge style or the newlines collapse.
          const connections = (peerData.connections || []).filter(c => c && c.length >= 2);
          const label = connections.map(c => `${c[0]} ↔ ${c[1]}`).join('\n');
          elements.push({
            group: 'edges',
            data: {
              id: edgeId, source: deviceName, target: peerName, label,
              // Carried so the style can thicken a bundle. At the zoom levels
              // where a whole fabric fits on screen the label is unreadable
              // and the line is all there is, so the count has to reach the
              // renderer as data rather than only as text.
              connectionCount: connections.length,
            },
          });
        }
      }
    }

    return elements;
  }


  /**
   * Load the platform map: a table of platform substring -> DrawIO shape,
   * with a second tier of looser rules that also look at the hostname.
   * It is the accumulated field knowledge about what a platform string
   * actually means, and it is optional on purpose — a missing or malformed
   * file costs icon precision and nothing else.
   */
  loadPlatformMap(data) {
    this._platformMap = typeof data === 'string' ? JSON.parse(data) : data;
    const m = this._platformMap;
    const tier1 = m && m.platform_patterns
      ? Object.keys(m.platform_patterns).filter(k => !k.startsWith('_comment')).length
      : 0;
    const tier2 = m && m.fallback_patterns ? Object.keys(m.fallback_patterns).length : 0;
    console.log(`[map] platform map: ${tier1} platform patterns, ${tier2} fallback rules`);
  }

  /**
   * DrawIO shape name -> embedded icon key.
   * "shape=mxgraph.cisco.switches.layer_3_switch" -> "layer-3-switch"
   *
   * An unmapped shape returns null rather than guessing a switch, so the
   * caller falls through to the next tier instead of drawing an IP phone
   * as a chassis.
   */
  _shapeToIconKey(shapeStr) {
    if (!shapeStr) return null;
    const parts = shapeStr.replace('shape=', '').split('.');
    const name = parts[parts.length - 1];

    const shapeMap = {
      'router': 'router',
      'layer_3_switch': 'layer-3-switch',
      'workgroup_switch': 'workgroup-switch',
      'multilayer_switch': 'multilayer-switch',
      'multilayer_remote_switch': 'multilayer-switch',
      'firewall': 'firewall',
      'asa_5500': 'firewall',
      'pix_firewall': 'firewall',
      'access_point': 'access-point',
      'wireless_transport': 'access-point',
      'generic_server': 'server',
      'pc': 'server',
      'workstation': 'server',
    };
    return shapeMap[name] || null;
  }

  /** Resolve an icon key through the platform map's three tiers. */
  _resolveIconKey(platform, nodeId) {
    if (!platform || platform === 'Undiscovered') return 'undiscovered';

    const map = this._platformMap;

    // Tier 1: exact substring match on the platform string. Longest
    // pattern first, so "c9300" wins over "c9".
    if (map && map.platform_patterns) {
      const patterns = Object.entries(map.platform_patterns)
        .filter(([k]) => !k.startsWith('_comment'))
        .sort((a, b) => b[0].length - a[0].length);

      for (const [pattern, shape] of patterns) {
        if (platform.includes(pattern)) {
          const key = this._shapeToIconKey(shape);
          if (key) return key;
        }
      }
    }

    // Tier 2: looser rules that also match on the hostname, which is how
    // a device whose platform string says nothing useful still gets the
    // right icon.
    if (map && map.fallback_patterns) {
      const pLower = platform.toLowerCase();
      const nLower = (nodeId || '').toLowerCase();

      for (const config of Object.values(map.fallback_patterns)) {
        const platMatch = (config.platform_patterns || []).some(x => pLower.includes(x));
        const nameMatch = (config.name_patterns || []).some(x => nLower.includes(x));
        if (platMatch || nameMatch) {
          const key = this._shapeToIconKey(config.shape);
          if (key) return key;
        }
      }
    }

    // Tier 3: role detection from the platform string alone.
    return this._roleToIconKey(this._detectDeviceRole(platform, nodeId));
  }

  _roleToIconKey(role) {
    const map = {
      'firewall': 'firewall',
      'router': 'router',
      'l2-switch': 'workgroup-switch',
      'l3-switch': 'layer-3-switch',
    };
    return map[role] || 'layer-3-switch';
  }

  /** Pre-rasterized PNG data URI for a platform/hostname combo. */
  _getIconForPlatform(platform, nodeId) {
    if (!platform || platform === 'Undiscovered') {
      return this._getCachedIcon('undiscovered');
    }

    const key = this._platformMap
      ? this._resolveIconKey(platform, nodeId)
      : this._roleToIconKey(this._detectDeviceRole(platform, nodeId));
    return this._getCachedIcon(key) || this._getCachedIcon('layer-3-switch');
  }


  // ═══════════════════════════════════════════════════════════════
  // Layouts
  // ═══════════════════════════════════════════════════════════════

  applyLayout(algorithm = 'breadthfirst') {
    if (!this.cy || this.cy.nodes().length === 0) return;

    const shared = { animate: true, animationDuration: 500, fit: true, padding: 30 };
    const fast   = { animate: true, animationDuration: 300, fit: true, padding: 30 };

    const configs = {
      dagre: this.dagreAvailable
        ? { name: 'dagre', rankDir: 'TB', nodeSep: 60, rankSep: 80, edgeSep: 10, ranker: 'network-simplex', ...shared }
        : { name: 'breadthfirst', directed: true, spacingFactor: 1.5, ...shared },

      'dagre-lr': this.dagreAvailable
        ? { name: 'dagre', rankDir: 'LR', nodeSep: 50, rankSep: 120, edgeSep: 10, ranker: 'network-simplex', ...shared }
        : { name: 'breadthfirst', directed: true, spacingFactor: 1.5, ...shared },

      breadthfirst: { name: 'breadthfirst', directed: true, spacingFactor: 1.5, ...fast },

      fcose: this.fcoseAvailable
        ? { name: 'fcose', quality: 'default', randomize: true,
            nodeRepulsion: () => 6000, idealEdgeLength: () => 80,
            edgeElasticity: () => 0.45, nestingFactor: 0.1,
            gravity: 0.25, gravityRange: 3.8, numIter: 2500,
            tile: true, tilingPaddingVertical: 10, tilingPaddingHorizontal: 10,
            ...shared }
        : { name: 'cose', nodeRepulsion: 8000, idealEdgeLength: 100, gravity: 0.25, ...shared },

      cose: { name: 'cose', nodeRepulsion: 8000, idealEdgeLength: 100, edgeElasticity: 100, gravity: 0.25, numIter: 1000, ...shared },

      cola: this.colaAvailable
        ? { name: 'cola', maxSimulationTime: 4000, nodeSpacing: 30, edgeLength: 120,
            convergenceThreshold: 0.01, avoidOverlap: true, handleDisconnected: true,
            flow: { axis: 'y', minSeparation: 60 }, ...shared }
        : { name: 'cose', nodeRepulsion: 8000, idealEdgeLength: 100, gravity: 0.25, ...shared },

      concentric: { name: 'concentric', concentric: (n) => n.degree(), levelWidth: (nodes) => Math.max(1, Math.floor(nodes.length / 4)), minNodeSpacing: 50, ...shared },
      circle: { name: 'circle', avoidOverlap: true, ...fast },
      grid: { name: 'grid', avoidOverlap: true, condense: true, ...fast },
    };

    const config = configs[algorithm] || configs.breadthfirst;
    if (this._activeLayout) this._activeLayout.stop();

    const visibleEles = this.cy.elements(':visible');
    if (visibleEles.nodes().length === 0) return;

    const layout = visibleEles.layout(config);
    this._activeLayout = layout;
    layout.on('layoutstop', () => { this._activeLayout = null; });
    layout.run();
  }


  // ═══════════════════════════════════════════════════════════════
  // Controls
  // ═══════════════════════════════════════════════════════════════

  fitView() { if (this.cy) this.cy.fit(this.cy.elements(':visible'), 30); }
  zoomIn() { if (this.cy) this.cy.zoom(this.cy.zoom() * 1.2); }
  zoomOut() { if (this.cy) this.cy.zoom(this.cy.zoom() * 0.8); }


  // ═══════════════════════════════════════════════════════════════
  // Filtering
  // ═══════════════════════════════════════════════════════════════

  toggleUndiscovered(hide) { this.hideUndiscovered = hide; this._applyFilters(); }
  toggleLeafNodes(hide) { this.hideLeafNodes = hide; this._applyFilters(); }

  _applyFilters() {
    if (!this.cy) return;
    const nodes = this.cy.nodes();
    nodes.show();
    this.cy.edges().show();

    const toHide = this.cy.collection();
    nodes.forEach(node => {
      if (this.hideUndiscovered && !node.data('discovered')) { toHide.merge(node); return; }
      if (this.hideLeafNodes && node.degree(false) <= 1) { toHide.merge(node); return; }
    });

    if (toHide.length > 0) { toHide.hide(); toHide.connectedEdges().hide(); }
    this.cy.edges().forEach(edge => {
      if (!edge.source().visible() || !edge.target().visible()) edge.hide();
    });

    this._emitStats();
    if (this.onFilterUpdate) {
      const total = this.cy.nodes().length;
      const visible = this.cy.nodes(':visible').length;
      this.onFilterUpdate({ total, visible, hidden: total - visible });
    }
  }


  // ═══════════════════════════════════════════════════════════════
  // Export
  // ═══════════════════════════════════════════════════════════════

  // PNG options, shared by the download and by a host that writes the file
  // itself: the whole graph, at 2x, on the canvas colour.
  _pngOptions(output) {
    const dark = document.documentElement.dataset.theme === 'dark';
    const pal = (window.omegamapsTheme && window.omegamapsTheme.cy) || {};
    return { output: output, bg: pal.bg || (dark ? '#1c2127' : '#ffffff'), full: true, scale: 2 };
  }

  pngBase64() {
    return this.cy ? this.cy.png(this._pngOptions('base64')) : '';
  }

  exportPNG() {
    if (!this.cy) return;
    const png = this.cy.png(this._pngOptions('blob'));
    const a = document.createElement('a');
    a.href = URL.createObjectURL(png);
    a.download = 'topology.png';
    a.click();
    URL.revokeObjectURL(a.href);
  }

  exportJSON() {
    if (!this.rawData) return;
    const blob = new Blob([JSON.stringify(this.rawData, null, 2)], { type: 'application/json' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = 'map.json';
    a.click();
    URL.revokeObjectURL(a.href);
  }


  // ═══════════════════════════════════════════════════════════════
  // Stats & Device List
  // ═══════════════════════════════════════════════════════════════

  getStats() {
    if (!this.cy) return { nodes: 0, edges: 0, vendors: 0, undiscovered: 0, total: 0, hidden: 0 };
    const allNodes = this.cy.nodes();
    const visibleNodes = this.cy.nodes(':visible');
    const visibleEdges = this.cy.edges(':visible');
    const vendors = new Set();
    visibleNodes.forEach(n => {
      const v = this._detectVendor(n.data('platform') || '', n.id());
      if (v !== 'default') vendors.add(v);
    });

    // Counted over EVERY node, not the visible ones: this is the number
    // the leaf filter hides, and a count that reads 0 whenever the filter
    // is on tells you nothing.
    let undiscovered = 0;
    allNodes.forEach(n => { if (!n.data('discovered')) undiscovered++; });
    return {
      nodes: visibleNodes.length,
      edges: visibleEdges.length,
      vendors: vendors.size,
      undiscovered,
      total: allNodes.length,
      hidden: allNodes.length - visibleNodes.length,
    };
  }

  getDeviceList() {
    if (!this.cy) return [];
    return this.cy.nodes(':visible').map(n => ({
      id: n.id(),
      label: n.data('label'),
      ip: n.data('ip'),
      platform: n.data('platform'),
      vendor: this._detectVendor(n.data('platform'), n.id()),
      discovered: n.data('discovered'),
    })).sort((a, b) => a.label.localeCompare(b.label));
  }

  selectNode(nodeId) {
    if (!this.cy) return;
    this.cy.nodes().unselect();
    const node = this.cy.getElementById(nodeId);
    if (node.length) {
      node.select();
      this.cy.animate({ center: { eles: node }, duration: 300 });
      this.selectedNode = nodeId;
      if (this.onNodeSelect) this.onNodeSelect(node.data());
    }
  }


  // ═══════════════════════════════════════════════════════════════
  // Layout Persistence
  // ═══════════════════════════════════════════════════════════════

  getPositions() {
    if (!this.cy) return {};
    const positions = {};
    this.cy.nodes().forEach(node => {
      const pos = node.position();
      positions[node.id()] = { x: Math.round(pos.x), y: Math.round(pos.y) };
    });
    return positions;
  }

  applyPositions(savedPositions) {
    if (!this.cy || !savedPositions) return { applied: 0, missing: [] };
    let applied = 0;
    const missing = [];
    this.cy.nodes().forEach(node => {
      const id = node.id();
      const pos = savedPositions[id];
      if (pos) { node.position(pos); applied++; }
      else missing.push(id);
    });
    if (applied > 0) this.cy.fit(null, 30);
    return { applied, missing };
  }

  _emitStats() {
    if (this.onStatsUpdate) this.onStatsUpdate(this.getStats());
  }


  // ═══════════════════════════════════════════════════════════════
  // Vendor Detection & Colors
  // ═══════════════════════════════════════════════════════════════

  _vendorColors = {
    cisco: '#049fd9', juniper: '#F58536', arista: '#2D8659',
    paloalto: '#FA582D', fortinet: '#EE3124', default: '#4a9eff',
  };

  _vendorFills = {
    cisco: 'rgba(4,159,217,0.25)', juniper: 'rgba(245,133,54,0.25)',
    arista: 'rgba(45,134,89,0.25)', paloalto: 'rgba(250,88,45,0.25)',
    fortinet: 'rgba(238,49,36,0.25)', default: 'rgba(74,158,255,0.2)',
  };

  _detectVendor(platform, nodeId) {
    const p = (platform || '').toLowerCase();
    const n = (nodeId || '').toLowerCase();
    const checks = [
      [['junos', 'juniper', 'mx', 'qfx', 'ex2', 'ex3', 'ex4', 'srx', 'ptx', 'acx'], 'juniper'],
      [['arista', 'eos', 'veos', 'dcs-', 'ccs-'], 'arista'],
      [['palo', 'pan-', 'pa-'], 'paloalto'],
      [['forti', 'fortigate', 'fortios'], 'fortinet'],
      [['cisco', 'ios', 'nx-os', 'nexus', 'catalyst', 'c9', 'ws-c', 'isr', 'asr', 'asa'], 'cisco'],
    ];
    for (const [patterns, vendor] of checks) {
      for (const pat of patterns) {
        if (p.includes(pat) || n.includes(pat)) return vendor;
      }
    }
    return 'default';
  }

  _detectDeviceRole(platform, nodeId) {
    const p = (platform || '').toLowerCase();
    const n = (nodeId || '').toLowerCase();

    const fwPlatform = ['asa', 'firepower', 'ftd', 'fxos', 'pan-os', 'pa-', 'panos',
                        'fortigate', 'fortios', 'srx', 'screenos', 'checkpoint', 'gaia'];
    const fwName = ['fw', 'firewall', 'palo', 'forti', 'asa'];
    if (fwPlatform.some(pat => p.includes(pat)) || fwName.some(pat => n.includes(pat))) return 'firewall';

    const rtrPlatform = ['isr', 'asr', 'ncs', 'crs', 'c8000', '7600', '7200', '7500',
                         'mx-', 'mx9', 'mx4', 'mx2', 'mx1', 'mx8', 'mx10',
                         'vmx', 'ptx', 'acx', '7500r', '7280r'];
    const rtrName = ['rtr', '-rt-', '-rt.', 'router', 'gw-', 'gw.', 'gateway',
                     'wan-', 'wan.', 'border', 'br-', 'br.', 'pe-', 'pe.', '-pe-',
                     'ce-', 'ce.', 'mx-', 'mx.'];
    if (rtrPlatform.some(pat => p.includes(pat)) || rtrName.some(pat => n.includes(pat))) return 'router';

    const l2Platform = ['2960', '3560', '3750', 'c1000', 'cbs', 'ex2200', 'ex2300',
                        'ex3300', 'ws-c29', 'ws-c35', 'ws-c37', 'ie-', 'ie2000',
                        'ie3000', 'ie4000', 'sf', 'sg', 'c1200', 'c1300'];
    const l2Name = ['access', 'acc-', 'acc.', 'closet', 'idf', 'mdf',
                    'edge-sw', 'tor-', 'tor.', 'leaf-', 'leaf.'];
    if (l2Platform.some(pat => p.includes(pat)) || l2Name.some(pat => n.includes(pat))) return 'l2-switch';

    return 'l3-switch';
  }

  _getVendorColor(platform, nodeId) {
    return this._vendorColors[this._detectVendor(platform, nodeId)] || this._vendorColors.default;
  }

  _getVendorFill(platform, nodeId) {
    return this._vendorFills[this._detectVendor(platform, nodeId)] || this._vendorFills.default;
  }
}

// Export
window.TopologyViewer = TopologyViewer;