/* include/omegamaps/omegamaps.h
 *
 * Stable C surface over the omegamaps run model. Hand-written: cgo emits a
 * header too (libomegamaps.h, in the build directory) and it is an artifact,
 * not the contract.
 *
 * The vault is a separate surface, omegamaps/vault.h; a crawl takes a vault
 * handle from it.
 *
 * WHAT THIS IS. A run -- a crawl, or a recorded one played back -- as a
 * handle whose state is pulled: progress in one consistent read, and the
 * rows and decisions that changed after a sequence number. Nothing after the
 * open depends on where the run came from, so a view built against a replay
 * works against a live crawl unchanged.
 *
 * DELIVERY MODEL. No callback into C. Each run owns a notifier;
 * omegamaps_notify_handle returns its readable end -- an fd on POSIX, a
 * socket on Windows -- which the caller watches with QSocketNotifier. When it
 * becomes readable, drain it, then pull:
 *
 *     progress = omegamaps_progress(h);          // FIRST
 *     rows     = omegamaps_rows_since(h, rseq);  // then these
 *     notes    = omegamaps_decisions_since(h, dseq);
 *
 * Order matters at the end of a run. If progress says finished, the run can
 * no longer change, so rows and decisions read after it are complete. Read
 * the other way round, the last few changes can land between the row read
 * and a "finished" progress read, and a caller that stops pulling on
 * finished loses them. One wake covers any number of changes; always pull
 * everything since the last seq rather than counting wakes.
 *
 * A running device's phase time moves without any event, so a view showing
 * "waiting on X for 12s" polls omegamaps_progress on a timer while the run is
 * unfinished. That is the only reason to call without a wake.
 *
 * THREADING. Every call is safe from any thread and none blocks on the
 * network. The caller's thread is the one that sees the results.
 *
 * RESULTS are JSON, UTF-8, owned by the caller and released with
 * omegamaps_free. Field names are snake_case and stable; fields may be added,
 * and a reader ignores what it does not know.
 *
 *   progress        {"seq", "depth", "elapsed_ms", "finished",
 *                    "counts": {"queued","running","reached","failed",
 *                               "not_dialed","new_host_keys","attempts",
 *                               "rejections"},
 *                    "depths": [{"depth","total","queued","running",
 *                                "reached","failed","not_dialed"}],
 *                    "running": [row]}   longest in its phase first
 *   rows_since      {"seq", "rows": [row]}   rows changed after seq
 *   decisions_since {"seq", "decisions": [{"seq","at_ms","kind","identity",
 *                    "name","via","detail","text"}]}
 *
 *   row  {"seq","identity","name","display","depth","platform","state",
 *         "via","detail","descr","caps","credential","cred_reason",
 *         "attempts","neighbors","new","method","phase","phase_ms",
 *         "duration_ms"}
 *
 * state is one of queued, running, reached, failed, "not dialed". via is the
 * identity of the device that reported this one, empty for a seed.
 * identity is the crawl's claim key and never changes; display is what to
 * show, and two devices can share one -- key anything on identity.
 *
 * Every *_ms field is in the run's own time. For a replay that means the
 * recorded durations whatever the speed: at 50x a 7s collection is still
 * duration_ms 7000, and elapsed_ms runs at 50 ms per wall-clock ms. at_ms is
 * the exception: it is this machine's clock, when the event was delivered. The "seq" returned by each *_since call is the value to pass next.
 *
 * ERRORS. A failing call returns -1 or NULL and leaves a message in
 * omegamaps_last_error(), which is per-thread and cleared by the next
 * successful call on that thread.
 */

#ifndef OMEGAMAPS_OMEGAMAPS_H
#define OMEGAMAPS_OMEGAMAPS_H

#ifdef __cplusplus
extern "C" {
#endif

/* The library's version string, as every CLI tool reports it. */
char *omegamaps_version(void);

/* Releases any char* this library returned. NULL is fine. */
void omegamaps_free(void *p);

/* The last failure on the calling thread, or "" -- never NULL. Free it. */
char *omegamaps_last_error(void);

/* Opens a recorded event stream (crawl -events) and plays it into a new run.
 * speed divides the recorded gaps between events: 1 is real time, 10 is ten
 * times faster, and 0 or below delivers everything at once while keeping the
 * recorded durations. Returns a handle > 0, or -1. */
long long omegamaps_replay_open(const char *path, double speed);

/* Starts a live crawl and returns its run handle (> 0), or -1 with the
 * reason in omegamaps_last_error(). Everything after this -- the notifier,
 * progress, rows, decisions, cancel, close -- is the same as for a replay.
 *
 * vault is a handle from omegamaps_vault_open, unlocked; credentials are
 * resolved from it inside Go, per device, and never appear in the request.
 * 0 means no vault, which only an SNMP-free run with an SSH agent can use.
 *
 * request is JSON. Every field but seeds and map_path is optional:
 *
 *   { "seeds": ["172.16.1.2"],          free text is fine: split on , ; space
 *     "depth": 3, "concurrency": 5,     depth 0 is seeds only
 *     "timeout_ms": 30000,              per command
 *     "snmp_timeout_ms": 5000,          per SNMP request
 *     "domains": ["lab.local"],         appended to bare names, stripped from
 *                                       the map
 *     "allow_domains": [],              dial only under these; others map as
 *                                       leaves
 *     "exclude": ["linux"],             substrings of platform, hostname,
 *                                       sysname or port description -- not
 *                                       globs
 *     "methods": ["snmp", "ssh"],       order to try; default ssh
 *     "cred_tags": ["lab"], "max_creds": 0, "cred_breaker": 0,
 *     "host_keys": "tofu",              or "strict"
 *     "known_hosts_path": "",           default ~/.ssh/known_hosts
 *     "legacy": false, "trust_unidirectional": false,
 *     "map_path": "/home/me/maps/lab-map.json",
 *     "events_path": "",                default lab-map.events.jsonl beside it
 *     "log_path": "" }                  default lab-map.log beside it
 *
 * Every crawl records its event stream, so every crawl can be replayed. The
 * map is written -- whole, through a rename -- before the run reports
 * finished, so a caller that loads it on "finished" always finds it. A
 * cancelled crawl still writes the map of what it reached. */
long long omegamaps_crawl_open(long long vault, const char *request);

/* The request a form starts from, as JSON in the same shape: the defaults the
 * command line uses (depth, concurrency, timeouts, methods, host keys), with
 * empty lists everywhere else. snmp_timeout_ms 0 means the collector's own
 * default. */
char *omegamaps_crawl_defaults(void);

/* Checks a request the way omegamaps_crawl_open would, without starting
 * anything, for a form that marks bad fields as they are typed. Returns a
 * JSON array, [] when the request is good:
 *
 *   [{"field": "seeds", "message": "at least one seed is required"}, ...]
 *
 * field names match the request's. NULL only when the JSON does not parse. */
char *omegamaps_crawl_validate(const char *request);

/* Where a run's output went and how it ended, as JSON:
 *
 *   { "kind": "crawl",                  or "replay"
 *     "state": "done",                  running, done, cancelled, failed
 *     "error": "",                      why, when failed
 *     "map_path": "...", "events_path": "...", "log_path": "...",
 *     "devices": 6, "nodes": 5 }
 *
 * Read it after progress reports finished; before that, state is "running"
 * and the counts are zero. */
char *omegamaps_run_result(long long h);

/* The readable end of the run's notifier: an fd on POSIX, a SOCKET on
 * Windows. Valid until omegamaps_close. Drain it before pulling -- its
 * contents are wakeups, not data. */
long long omegamaps_notify_handle(long long h);

/* Stops the run. Devices in flight end as failed, the way a cancelled crawl
 * ends them, and one last wake follows. The handle stays valid. */
int omegamaps_cancel(long long h);

/* Cancels if needed and releases the run and its notifier. No wake arrives
 * after this returns. The handle is invalid afterwards. */
int omegamaps_close(long long h);

char *omegamaps_progress(long long h);
char *omegamaps_rows_since(long long h, unsigned long long seq);
char *omegamaps_decisions_since(long long h, unsigned long long seq);

#ifdef __cplusplus
}
#endif

#endif /* OMEGAMAPS_OMEGAMAPS_H */
