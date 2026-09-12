/* include/omegamaps/mapview.h
 *
 * The map viewer's server: internal/mapweb's loopback page for one map, for
 * a QWebEngineView to show. The page, its guards and its exports (PNG, JSON,
 * draw.io) are the ones `mapview` serves to a browser; this surface only
 * starts the server and says where it is.
 *
 * A viewer is a handle, independent of runs. Nothing in it changes unless the
 * caller calls in, so there is no notifier.
 *
 *     long long v = omegamaps_mapview_open("lab-map.json", NULL);
 *     char *info = omegamaps_mapview_info(v);   // {"url", ...}; free it
 *     ...load info.url in the view...
 *     omegamaps_mapview_load(v, "other-map.json");  // then reload the page
 *     omegamaps_mapview_close(v);
 *
 * The url carries the per-run token and is the credential for this server:
 * keep it in memory, do not log it. addr is the same without the token.
 *
 * Saved layouts are files under layout_dir, one per map by absolute path
 * (NULL or "" means ~/.omegamaps/layouts). Positions are keyed by node
 * name, so a re-crawl written to the same path keeps its layout.
 *
 * Failures return -1 (or NULL); omegamaps_last_error() says why, on the
 * calling thread. Every call is safe from any thread and returns at once.
 *
 *   info  {"url", "addr", "path", "name", "nodes", "layout_path"}
 */

#ifndef OMEGAMAPS_MAPVIEW_H
#define OMEGAMAPS_MAPVIEW_H

#include <omegamaps/omegamaps.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Starts a server on 127.0.0.1 (port chosen by the OS) with map_path
 * loaded. -1 when the file cannot be read or is not a map. */
long long omegamaps_mapview_open(const char *map_path, const char *layout_dir);

/* Replaces the loaded map. On failure the previous map stays loaded. The
 * page shows the new map on its next load; old node ids stop resolving. */
int omegamaps_mapview_load(long long viewer, const char *map_path);

/* JSON as above; free with omegamaps_free. */
char *omegamaps_mapview_info(long long viewer);

/* Stops the server; the handle is gone. A page still open stops working. */
int omegamaps_mapview_close(long long viewer);

#ifdef __cplusplus
} /* extern "C" */
#endif

#endif /* OMEGAMAPS_MAPVIEW_H */
