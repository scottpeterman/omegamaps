/**
 * internal/mapweb/assets/app.js
 *
 * Page controller: fetches the map from the application, drives the viewer,
 * and turns a click on a node into a connect request.
 *
 * The token arrives in the URL because that is the only channel the app has
 * to a browser it just launched. It is read once, removed from the address
 * bar so it does not end up in history or in a screenshot, and thereafter
 * lives only in this closure. It goes on API calls as a header rather than a
 * query string: a header cannot be sent by a cross-origin page that has not
 * already been let through the checks on the server.
 */

'use strict';

(function () {

  var TOKEN = '';
  var viewer = null;
  var mapName = '';
  var canConnect = false;
  var lastNode = null;       // node currently in the detail panel
  var nodeIDs = {};          // node label -> opaque connect id
  var layoutOnServer = false; // the application keeps layouts (layout_store)
  var pageState = 'loading';  // loading, ready, error -- for the host window
  var LAYOUT_PREFIX = 'omegamaps-map:';


  // ── Token ─────────────────────────────────────────────────────

  // The token arrives once, in the URL. It is moved straight into
  // sessionStorage and out of the address bar: sessionStorage belongs to
  // this tab alone and dies with it, whereas an address bar ends up in
  // history, in screenshots and in whatever the browser syncs.
  //
  // Reading it back is what makes a refresh work. Without this, F5 reloads
  // a URL the token has already been removed from and the page returns a
  // token error on a map that was working a second earlier.
  var TOKEN_KEY = 'omegamaps-map-token';

  function takeToken() {
    var params = new URLSearchParams(window.location.search);
    var fromURL = params.get('t') || '';

    if (fromURL) {
      TOKEN = fromURL;
      try { sessionStorage.setItem(TOKEN_KEY, fromURL); } catch (e) { /* private mode */ }
      history.replaceState(null, '', window.location.pathname);
      return;
    }

    try { TOKEN = sessionStorage.getItem(TOKEN_KEY) || ''; } catch (e) { TOKEN = ''; }
  }

  // A token that no longer works is worth discarding: the application has
  // restarted and issued a new one, so every later reload of this tab
  // should say so rather than retry a credential that expired with the
  // previous process.
  function forgetToken() {
    TOKEN = '';
    try { sessionStorage.removeItem(TOKEN_KEY); } catch (e) { /* private mode */ }
  }

  function api(path, options) {
    var opts = options || {};
    opts.headers = Object.assign({}, opts.headers, {
      'X-Omegamaps-Token': TOKEN,
      'Content-Type': 'application/json',
    });
    opts.cache = 'no-store';
    return fetch(path, opts);
  }


  // ── UI helpers ────────────────────────────────────────────────

  function el(id) { return document.getElementById(id); }

  function setText(id, val) {
    var e = el(id);
    if (e) e.textContent = val;
  }

  function showBanner(msg) {
    var b = el('banner');
    b.textContent = msg;
    b.classList.add('show');
  }

  function hideBanner() { el('banner').classList.remove('show'); }

  var toastTimer = null;
  function toast(msg) {
    var t = el('toast');
    t.textContent = msg;
    t.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.classList.remove('show'); }, 2200);
  }

  function updateStats(stats) {
    setText('statDevices', stats.nodes);
    setText('statConnections', stats.edges);
    setText('statVendors', stats.vendors);
    setText('statUndiscovered', stats.undiscovered);
  }

  function updateFilterInfo(info) {
    setText('filterInfo', info.hidden > 0
      ? 'showing ' + info.visible + ' of ' + info.total
      : '');
  }

  function selectedLayout() {
    var s = el('layoutSelect');
    return s ? s.value : 'breadthfirst';
  }


  // ── Device list ───────────────────────────────────────────────

  function updateDeviceList() {
    var container = el('deviceList');
    if (!container || !viewer) return;

    container.innerHTML = '';
    viewer.getDeviceList().forEach(function (d) {
      var item = document.createElement('div');
      item.className = 'device-item';
      item.dataset.id = d.id;

      var dot = document.createElement('span');
      dot.className = 'dot ' + (d.discovered ? 'dot-green' : 'dot-red');

      var name = document.createElement('span');
      name.className = 'device-name';
      name.textContent = d.label;
      name.title = d.platform || '';

      item.appendChild(dot);
      item.appendChild(name);

      if (d.vendor !== 'default') {
        var v = document.createElement('span');
        v.className = 'device-vendor';
        v.textContent = d.vendor;
        item.appendChild(v);
      }

      // Built as elements rather than an HTML string on purpose: a device
      // name comes off a network device and is not to be trusted as markup.
      item.addEventListener('click', function () { viewer.selectNode(d.id); });
      container.appendChild(item);
    });
  }


  // ── Node detail + connect ─────────────────────────────────────

  function kv(label, value) {
    var row = document.createElement('div');
    row.className = 'kv-row';
    var l = document.createElement('span');
    l.className = 'kv-label';
    l.textContent = label;
    var v = document.createElement('span');
    v.className = 'kv-value mono';
    v.textContent = value || '—';
    row.appendChild(l);
    row.appendChild(v);
    return row;
  }

  function tag(text, cls) {
    var t = document.createElement('span');
    t.className = 'tag ' + cls;
    t.textContent = text;
    return t;
  }

  // Connect stops being offered the moment the application cannot answer
  // for it: no callback wired, or the process is gone. A button that dials
  // nothing is worse than no button — it reads as a broken device rather
  // than a closed application.
  function setConnectAvailable(available, why) {
    if (canConnect === available) return;
    canConnect = available;
    if (!available && why) showBanner(why);
    updateNodeDetail(lastNode);
  }

  function updateNodeDetail(data) {
    lastNode = data;
    var panel = el('nodeDetail');
    var content = el('nodeDetailContent');
    if (!panel || !content) return;

    if (!data) {
      panel.classList.remove('active');
      document.querySelectorAll('.device-item.active').forEach(function (e) {
        e.classList.remove('active');
      });
      return;
    }

    panel.classList.add('active');
    content.innerHTML = '';

    var head = document.createElement('div');
    var host = document.createElement('div');
    host.className = 'detail-hostname';
    host.textContent = data.label || data.id;
    head.appendChild(host);

    var tags = document.createElement('div');
    tags.style.margin = '4px 0 8px';
    tags.appendChild(data.discovered ? tag('Discovered', 'tag-green') : tag('Undiscovered', 'tag-red'));
    var vendor = viewer._detectVendor(data.platform, data.id);
    if (vendor !== 'default') {
      tags.appendChild(document.createTextNode(' '));
      tags.appendChild(tag(vendor, 'tag-blue'));
    }
    head.appendChild(tags);
    content.appendChild(head);

    content.appendChild(kv('IP', data.ip));
    content.appendChild(kv('Platform', data.platform));

    var id = nodeIDs[data.id];
    if (canConnect && id) {
      var btn = document.createElement('button');
      btn.className = 'primary';
      btn.style.marginTop = '8px';
      btn.style.width = '100%';
      btn.textContent = 'Connect';
      btn.addEventListener('click', function () { connect(id, data.label || data.id, btn); });
      content.appendChild(btn);
    }

    document.querySelectorAll('.device-item').forEach(function (e) {
      e.classList.toggle('active', e.dataset.id === data.id);
    });
  }

  function connect(id, label, btn) {
    btn.disabled = true;
    api('/api/connect', { method: 'POST', body: JSON.stringify({ id: id }) })
      .then(function (resp) {
        if (resp.status === 501) {
          // The server is up and says it has no way to open a session.
          setConnectAvailable(false, 'This map is open in a viewer that cannot start sessions.');
          return;
        }
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        toast('Opening ' + label + ' in omegamaps');
      })
      .catch(function (e) {
        // A rejected fetch means nothing answered, which for a loopback
        // address means the application exited.
        if (e instanceof TypeError) {
          setConnectAvailable(false, 'omegamaps is no longer running. Reopen the map from the application to connect.');
          return;
        }
        toast('Could not open ' + label + ': ' + e.message);
      })
      .finally(function () { btn.disabled = false; });
  }


  // ── Layout persistence ────────────────────────────────────────

  // Where the application offers a layout store (layout_store in the map
  // payload), layouts go there: a file per map that any later window finds.
  // Otherwise localStorage, which is per origin -- and the origin's port is
  // new every time the server starts, so a layout kept there lasts only as
  // long as this server does. That fallback is for a bare harness; the
  // application always offers the store.
  var LayoutStore = {
    maxAge: 90 * 24 * 60 * 60 * 1000,
    key: function () { return LAYOUT_PREFIX + (mapName || 'default'); },

    snapshot: function () {
      return {
        positions: viewer.getPositions(),
        layout: selectedLayout(),
        hideUndiscovered: viewer.hideUndiscovered,
        hideLeafNodes: viewer.hideLeafNodes,
        zoom: viewer.cy.zoom(),
        pan: viewer.cy.pan(),
        savedAt: new Date().toISOString(),
      };
    },

    save: async function () {
      if (!viewer || !viewer.cy) return false;
      var body = JSON.stringify(this.snapshot());
      if (layoutOnServer) {
        try {
          var resp = await api('/api/layout', { method: 'PUT', body: body });
          return resp.ok;
        } catch (e) { return false; }
      }
      try { localStorage.setItem(this.key(), body); return true; } catch (e) { return false; }
    },

    load: async function () {
      var data = null;
      try {
        if (layoutOnServer) {
          var resp = await api('/api/layout');
          if (resp.status !== 200) return null;
          data = await resp.json();
        } else {
          var raw = localStorage.getItem(this.key());
          if (!raw) return null;
          data = JSON.parse(raw);
        }
      } catch (e) { return null; }
      if (!data || Date.now() - new Date(data.savedAt).getTime() > this.maxAge) {
        if (data) await this.clear();
        return null;
      }
      return data;
    },

    clear: async function () {
      if (layoutOnServer) {
        try {
          var resp = await api('/api/layout', { method: 'DELETE' });
          return resp.ok;
        } catch (e) { return false; }
      }
      try { localStorage.removeItem(this.key()); } catch (e) { /* private mode */ }
      return true;
    },
  };

  async function restoreLayout() {
    var saved = await LayoutStore.load();
    if (!saved || !saved.positions || !viewer) return false;

    if (saved.hideUndiscovered !== undefined) {
      el('hideUndiscovered').checked = saved.hideUndiscovered;
      viewer.hideUndiscovered = saved.hideUndiscovered;
    }
    if (saved.hideLeafNodes !== undefined) {
      el('hideLeaf').checked = saved.hideLeafNodes;
      viewer.hideLeafNodes = saved.hideLeafNodes;
    }
    viewer._applyFilters();

    if (saved.layout) el('layoutSelect').value = saved.layout;

    viewer.applyPositions(saved.positions);
    if (saved.zoom && saved.pan) viewer.cy.viewport({ zoom: saved.zoom, pan: saved.pan });

    setText('layoutStatus', 'restored');
    return true;
  }


  // ── Load ──────────────────────────────────────────────────────

  function applyFilters() {
    if (!viewer) return;
    viewer.hideUndiscovered = el('hideUndiscovered').checked;
    viewer.hideLeafNodes = el('hideLeaf').checked;
    viewer._applyFilters();
    viewer.applyLayout(selectedLayout());
    updateDeviceList();
  }

  // The platform map decides which icon a platform string gets. It is a
  // static asset rather than part of the map payload: it changes when the
  // knowledge changes, not when the network does.
  async function loadPlatformMap() {
    try {
      var resp = await fetch('/assets/platform_map.json', { cache: 'no-store' });
      if (!resp.ok) return;
      viewer.loadPlatformMap(await resp.json());
    } catch (e) {
      console.warn('[map] no platform map; icons fall back to role detection');
    }
  }

  async function loadMap() {
    pageState = 'loading';
    var ok = false;
    try {
      ok = await fetchAndShowMap();
    } finally {
      pageState = ok ? 'ready' : 'error';
    }
  }

  async function fetchAndShowMap() {
    hideBanner();
    var resp;
    try {
      resp = await api('/api/map');
    } catch (e) {
      canConnect = false;
      updateNodeDetail(lastNode);
      showBanner('Cannot reach omegamaps. The application may have closed.');
      return false;
    }

    if (resp.status === 403) {
      forgetToken();
      canConnect = false;
      showBanner(TOKEN
        ? 'This viewer was opened without a valid token. Open the map from omegamaps.'
        : 'This map link has expired — omegamaps has restarted since it was opened. Open the map again from the application.');
      return false;
    }
    if (resp.status === 404) {
      showBanner('No map is loaded.');
      return false;
    }
    if (!resp.ok) {
      showBanner('Could not load the map (HTTP ' + resp.status + ').');
      return false;
    }

    var payload = await resp.json();
    mapName = payload.name || 'map.json';
    nodeIDs = payload.ids || {};
    canConnect = !!payload.can_connect;
    layoutOnServer = !!payload.layout_store;
    lastNode = null;
    setText('mapName', mapName);
    document.title = mapName + ' — Map';

    viewer.hideUndiscovered = el('hideUndiscovered').checked;
    viewer.hideLeafNodes = el('hideLeaf').checked;
    viewer.loadTopology(payload.map);

    if (!(await restoreLayout())) {
      viewer.applyLayout(selectedLayout());
      setText('layoutStatus', '');
    }
    updateDeviceList();
    return true;
  }


  // ── Host window ───────────────────────────────────────────────

  // For a window that hosts this page (the Qt viewer): a theme change
  // without a reload, and one read of what the page is showing. A browser
  // never calls either.
  window.omegamapsSetTheme = function (theme) {
    if (!window.omegamapsApplyTheme || !window.omegamapsApplyTheme(theme)) return false;
    try { sessionStorage.setItem('omegamaps-map-theme', JSON.stringify(theme)); } catch (e) { /* private mode */ }
    if (viewer) viewer.restyle();
    return true;
  };

  // The three exports as data, for a host that writes files itself rather
  // than taking a browser download. Same content as the buttons produce.
  // Throws, with the message the button would toast, when there is nothing
  // to export.
  window.omegamapsExport = function (kind, shapeMode) {
    if (!viewer || !viewer.cy || !viewer.rawData) throw new Error('no map loaded');
    var base = (mapName || 'map').replace(/\.json$/i, '');
    switch (kind) {
      case 'png':
        return { name: base + '.png', encoding: 'base64', data: viewer.pngBase64() };
      case 'json':
        return { name: base + '.json', encoding: 'utf8', data: JSON.stringify(viewer.rawData, null, 2) };
      case 'drawio':
        return {
          name: base + '.drawio', encoding: 'utf8',
          data: DrawIOExport.generate(viewer, { title: base, shapeMode: shapeMode || el('drawioMode').value }),
          nodes: viewer.cy.nodes(':visible').length,
        };
    }
    throw new Error('unknown export ' + kind);
  };

  window.omegamapsStatus = function () {
    var cy = viewer && viewer.cy;
    return {
      state: pageState,
      name: mapName,
      nodes: cy ? cy.nodes().length : 0,
      edges: cy ? cy.edges().length : 0,
      visible: cy ? cy.nodes(':visible').length : 0,
      // The whole map, whatever the filters show: crawled devices, leaves
      // (named by a neighbour only) and every link. The sidebar counts what
      // is visible; a line about the file should not move with a checkbox.
      stats: (function () {
        var s = viewer ? viewer.getStats() : { total: 0, undiscovered: 0 };
        return { devices: s.total - s.undiscovered, leaves: s.undiscovered, links: cy ? cy.edges().length : 0 };
      })(),
      layout_store: layoutOnServer,
      layout_status: (el('layoutStatus') || {}).textContent || '',
      banner: el('banner').classList.contains('show') ? el('banner').textContent : '',
    };
  };


  // ── Init ──────────────────────────────────────────────────────

  async function init() {
    takeToken();

    viewer = new TopologyViewer('cy', {
      onNodeSelect: updateNodeDetail,
      onStatsUpdate: updateStats,
      onFilterUpdate: updateFilterInfo,
    });
    viewer.init();
    await viewer.preloadIcons();
    await loadPlatformMap();

    el('layoutSelect').addEventListener('change', function () {
      viewer.applyLayout(selectedLayout());
    });
    el('hideUndiscovered').addEventListener('change', applyFilters);
    el('hideLeaf').addEventListener('change', applyFilters);
    el('btnFit').addEventListener('click', function () { viewer.fitView(); });
    el('btnZoomIn').addEventListener('click', function () { viewer.zoomIn(); });
    el('btnZoomOut').addEventListener('click', function () { viewer.zoomOut(); });
    el('btnPNG').addEventListener('click', function () { viewer.exportPNG(); });
    el('btnJSON').addEventListener('click', function () { viewer.exportJSON(); });
    el('btnDrawIO').addEventListener('click', function () {
      // The two failures worth naming are "no map" and "everything is
      // filtered out". Both are things the person can act on, and a silent
      // no-op after pressing an export button reads as a broken button.
      try {
        var n = DrawIOExport.download(viewer, mapName, el('drawioMode').value);
        toast('Exported ' + n + (n === 1 ? ' node' : ' nodes') + ' to draw.io');
      } catch (err) {
        toast('Draw.io export: ' + (err && err.message ? err.message : 'failed'));
      }
    });
    el('btnReload').addEventListener('click', loadMap);
    el('btnSaveLayout').addEventListener('click', async function () {
      setText('layoutStatus', 'saving\u2026');
      setText('layoutStatus', (await LayoutStore.save()) ? 'saved' : 'could not save');
    });
    el('btnClearLayout').addEventListener('click', async function () {
      setText('layoutStatus', (await LayoutStore.clear()) ? 'cleared' : 'could not clear');
    });

    await loadMap();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

})();