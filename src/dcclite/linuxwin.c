//go:build linux && native && cgo

#include <gtk/gtk.h>
#include <gdk/gdkx.h>
#include <webkit2/webkit2.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "linuxwin.h"

// Implemented in Go (native_window_linux.go).
extern char *dccGoHandle(char *op, char *raw, char **err);
extern void dccGoMouse(char *kind, int x, int y);
extern void dccGoAlive(void);
extern void dccGoLog(char *level, char *text);

static GtkWidget *g_win, *g_overlay, *g_view, *g_video;
static char *g_origin;
static volatile unsigned long g_xid;
static volatile int g_view_w, g_view_h;
static gint64 g_last_move;

static void logf_(const char *level, const char *fmt, ...) G_GNUC_PRINTF(2, 3);
static void logf_(const char *level, const char *fmt, ...) {
	va_list ap;
	va_start(ap, fmt);
	char *s = g_strdup_vprintf(fmt, ap);
	va_end(ap);
	dccGoLog((char *)level, s);
	g_free(s);
}

// ---- The bridge: window.dccLive(op, json) -> Promise<string> ----

static gboolean origin_ok(void) {
	const char *uri = g_view ? webkit_web_view_get_uri(WEBKIT_WEB_VIEW(g_view)) : NULL;
	size_t n = g_origin ? strlen(g_origin) : 0;
	return uri && n > 0 && strncmp(uri, g_origin, n) == 0 && (uri[n] == '\0' || uri[n] == '/' || uri[n] == '?' || uri[n] == '#');
}

static gboolean on_message(WebKitUserContentManager *m, JSCValue *value, WebKitScriptMessageReply *reply, gpointer data) {
	(void)m; (void)data;
	if (!origin_ok()) {
		webkit_script_message_reply_return_error_message(reply, "not allowed from this page");
		return TRUE;
	}
	if (!jsc_value_is_string(value)) {
		webkit_script_message_reply_return_error_message(reply, "bad request");
		return TRUE;
	}
	char *msg = jsc_value_to_string(value);
	char *nl = msg ? strchr(msg, '\n') : NULL;
	if (!nl) {
		g_free(msg);
		webkit_script_message_reply_return_error_message(reply, "bad request");
		return TRUE;
	}
	*nl = '\0';
	char *err = NULL;
	char *out = dccGoHandle(msg, nl + 1, &err);
	if (err) {
		webkit_script_message_reply_return_error_message(reply, err);
		free(err);
	} else {
		JSCContext *ctx = jsc_value_get_context(value);
		JSCValue *rv = jsc_value_new_string(ctx, out ? out : "{}");
		webkit_script_message_reply_return_value(reply, rv);
		g_object_unref(rv);
	}
	free(out);
	g_free(msg);
	return TRUE;
}

// ---- Links, new windows and downloads ----

static void open_outside(const char *uri) {
	GError *e = NULL;
	if (!gtk_show_uri_on_window(GTK_WINDOW(g_win), uri, GDK_CURRENT_TIME, &e)) {
		logf_("warn", "could not open the link in the default browser: %s", e ? e->message : "unknown");
		g_clear_error(&e);
	}
}

static gboolean inside(const char *uri) {
	size_t n = strlen(g_origin);
	return uri && strncmp(uri, g_origin, n) == 0 && (uri[n] == '\0' || uri[n] == '/' || uri[n] == '?' || uri[n] == '#');
}

static gboolean on_decide(WebKitWebView *v, WebKitPolicyDecision *d, WebKitPolicyDecisionType t, gpointer data) {
	(void)v; (void)data;
	if (t != WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION && t != WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION) return FALSE;
	WebKitNavigationAction *na = webkit_navigation_policy_decision_get_navigation_action(WEBKIT_NAVIGATION_POLICY_DECISION(d));
	WebKitURIRequest *req = webkit_navigation_action_get_request(na);
	const char *uri = req ? webkit_uri_request_get_uri(req) : NULL;
	if (!uri) return FALSE;
	if (t == WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION) {
		webkit_policy_decision_ignore(d);
		if (inside(uri)) webkit_web_view_load_uri(WEBKIT_WEB_VIEW(g_view), uri);
		else if (g_str_has_prefix(uri, "http://") || g_str_has_prefix(uri, "https://") || g_str_has_prefix(uri, "mailto:")) open_outside(uri);
		return TRUE;
	}
	if (inside(uri) || g_str_has_prefix(uri, "blob:") || g_str_has_prefix(uri, "data:") || g_str_has_prefix(uri, "about:")) return FALSE;
	if (webkit_navigation_action_get_navigation_type(na) == WEBKIT_NAVIGATION_TYPE_LINK_CLICKED) {
		webkit_policy_decision_ignore(d);
		if (g_str_has_prefix(uri, "http://") || g_str_has_prefix(uri, "https://") || g_str_has_prefix(uri, "mailto:")) open_outside(uri);
		return TRUE;
	}
	return FALSE;
}

static gboolean on_create(WebKitWebView *v, WebKitNavigationAction *na, gpointer data) {
	(void)v; (void)data;
	WebKitURIRequest *req = webkit_navigation_action_get_request(na);
	const char *uri = req ? webkit_uri_request_get_uri(req) : NULL;
	if (uri && (g_str_has_prefix(uri, "http://") || g_str_has_prefix(uri, "https://")) && !inside(uri)) open_outside(uri);
	return FALSE; // no new window is ever made
}

static gboolean on_dest(WebKitDownload *dl, const gchar *suggested, gpointer data) {
	(void)data;
	const char *dir = g_get_user_special_dir(G_USER_DIRECTORY_DOWNLOAD);
	if (!dir) dir = g_get_home_dir();
	char *base = g_path_get_basename(suggested && *suggested ? suggested : "download");
	char *dot = strrchr(base, '.');
	char *stem = dot && dot != base ? g_strndup(base, dot - base) : g_strdup(base);
	const char *ext = dot && dot != base ? dot : "";
	char *path = g_build_filename(dir, base, NULL);
	for (int i = 1; g_file_test(path, G_FILE_TEST_EXISTS) && i < 1000; i++) {
		g_free(path);
		char *name = g_strdup_printf("%s (%d)%s", stem, i, ext);
		path = g_build_filename(dir, name, NULL);
		g_free(name);
	}
	char *uri = g_filename_to_uri(path, NULL, NULL);
	g_mkdir_with_parents(dir, 0755);
	webkit_download_set_destination(dl, uri ? uri : path);
	logf_("info", "download saved to %s", path);
	g_free(uri); g_free(path); g_free(stem); g_free(base);
	return TRUE;
}

static void on_download(WebKitWebContext *c, WebKitDownload *dl, gpointer data) {
	(void)c; (void)data;
	g_signal_connect(dl, "decide-destination", G_CALLBACK(on_dest), NULL);
}

static void on_load_failed(WebKitWebView *v, WebKitLoadEvent e, const char *uri, GError *err, gpointer data) {
	(void)v; (void)e; (void)data;
	logf_("warn", "the page failed to load: %s (%s)", uri ? uri : "", err ? err->message : "");
}

static gboolean on_process_crashed(WebKitWebView *v, gpointer data) {
	(void)data;
	logf_("warn", "the web process ended; reloading the page");
	webkit_web_view_reload(v);
	return TRUE;
}

// ---- The picture: a native X11 window over the page ----

static gboolean on_video_draw(GtkWidget *w, cairo_t *cr, gpointer data) {
	(void)w; (void)data;
	cairo_set_source_rgb(cr, 0, 0, 0);
	cairo_paint(cr);
	return TRUE;
}

static void on_video_realize(GtkWidget *w, gpointer data) {
	(void)data;
	GdkWindow *gw = gtk_widget_get_window(w);
	if (!gw) return;
	gdk_window_ensure_native(gw); // VLC needs a real X window to draw into
	if (GDK_IS_X11_WINDOW(gw)) {
		g_xid = (unsigned long)GDK_WINDOW_XID(gw);
		logf_("info", "picture window ready (xid 0x%lx)", g_xid);
	}
}

static gboolean video_xy(GtkWidget *w, double ex, double ey, int *x, int *y) {
	GtkAllocation a;
	gtk_widget_get_allocation(w, &a);
	*x = (int)(a.x + ex);
	*y = (int)(a.y + ey);
	return TRUE;
}

static gboolean on_video_button(GtkWidget *w, GdkEventButton *e, gpointer data) {
	(void)data;
	int x, y;
	video_xy(w, e->x, e->y, &x, &y);
	if (e->type == GDK_2BUTTON_PRESS && e->button == 1) dccGoMouse((char *)"dbl", x, y);
	else if (e->type == GDK_BUTTON_PRESS && e->button == 3) dccGoMouse((char *)"menu", x, y);
	return TRUE;
}

static gboolean on_video_scroll(GtkWidget *w, GdkEventScroll *e, gpointer data) {
	(void)w; (void)data;
	int dy = 0;
	if (e->direction == GDK_SCROLL_UP) dy = -100;
	else if (e->direction == GDK_SCROLL_DOWN) dy = 100;
	else if (e->direction == GDK_SCROLL_SMOOTH) dy = (int)(e->delta_y * 100);
	if (dy != 0) dccGoMouse((char *)"wheel", 0, dy);
	return TRUE;
}

static gboolean on_video_motion(GtkWidget *w, GdkEventMotion *e, gpointer data) {
	(void)data;
	gint64 now = g_get_monotonic_time();
	if (now - g_last_move < 40000) return TRUE; // at most 25 a second
	g_last_move = now;
	int x, y;
	video_xy(w, e->x, e->y, &x, &y);
	dccGoMouse((char *)"move", x, y);
	return TRUE;
}

static void on_view_size(GtkWidget *w, GtkAllocation *a, gpointer data) {
	(void)w; (void)data;
	g_view_w = a->width;
	g_view_h = a->height;
}

static void on_destroy(GtkWidget *w, gpointer data) {
	(void)w; (void)data;
	gtk_main_quit();
}

// ---- Calls from other threads, run on the GTK thread ----

typedef struct { int x, y, w, h, show; } box_t;

static gboolean idle_box(gpointer p) {
	box_t *b = p;
	if (g_video) {
		int w = b->w < 2 ? 2 : b->w, h = b->h < 2 ? 2 : b->h;
		gtk_widget_set_margin_start(g_video, b->x < 0 ? 0 : b->x);
		gtk_widget_set_margin_top(g_video, b->y < 0 ? 0 : b->y);
		gtk_widget_set_size_request(g_video, w, h);
		if (b->show) gtk_widget_show(g_video);
		else gtk_widget_hide(g_video);
	}
	g_free(b);
	return G_SOURCE_REMOVE;
}

void dcc_gtk_box(int x, int y, int w, int h, int show) {
	box_t *b = g_new(box_t, 1);
	b->x = x; b->y = y; b->w = w; b->h = h; b->show = show;
	g_idle_add(idle_box, b);
}

static gboolean idle_fs(gpointer p) {
	if (g_win) {
		if (GPOINTER_TO_INT(p)) gtk_window_fullscreen(GTK_WINDOW(g_win));
		else gtk_window_unfullscreen(GTK_WINDOW(g_win));
	}
	return G_SOURCE_REMOVE;
}
void dcc_gtk_fullscreen(int on) { g_idle_add(idle_fs, GINT_TO_POINTER(on)); }

static gboolean idle_cursor(gpointer p) {
	if (g_video && gtk_widget_get_realized(g_video)) {
		GdkWindow *gw = gtk_widget_get_window(g_video);
		GdkCursor *c = NULL;
		if (GPOINTER_TO_INT(p)) c = gdk_cursor_new_for_display(gdk_window_get_display(gw), GDK_BLANK_CURSOR);
		gdk_window_set_cursor(gw, c);
		if (c) g_object_unref(c);
	}
	return G_SOURCE_REMOVE;
}
void dcc_gtk_cursor(int hide) { g_idle_add(idle_cursor, GINT_TO_POINTER(hide)); }

static gboolean idle_quit(gpointer p) {
	(void)p;
	if (g_win) gtk_widget_destroy(g_win);
	else gtk_main_quit();
	return G_SOURCE_REMOVE;
}
void dcc_gtk_quit(void) { g_idle_add(idle_quit, NULL); }

static gboolean idle_ping(gpointer p) { (void)p; dccGoAlive(); return G_SOURCE_REMOVE; }
void dcc_gtk_ping(void) { g_idle_add(idle_ping, NULL); }

unsigned long dcc_gtk_video_xid(void) { return g_xid; }
void dcc_gtk_view_size(int *w, int *h) { *w = g_view_w; *h = g_view_h; }

// ---- The window ----

static const char *bridge_js =
	"window.__dccGui = %s;\n"
	"if (location.origin === %s) {\n"
	"  window.dccLive = function (op, args) {\n"
	"    return window.webkit.messageHandlers.dccLive.postMessage(op + '\\n' + (args || '{}'));\n"
	"  };\n"
	"}\n";

static char *js_quote(const char *s) {
	GString *g = g_string_new("\"");
	for (; *s; s++) {
		if (*s == '"' || *s == '\\') g_string_append_c(g, '\\');
		g_string_append_c(g, *s);
	}
	g_string_append_c(g, '"');
	return g_string_free(g, FALSE);
}

int dcc_gtk_run(const char *url, const char *origin, const char *datadir, const char *title, int w, int h) {
	gdk_set_allowed_backends("x11");
	int argc = 1;
	char arg0[] = "DCCLite";
	char *argv_[] = { arg0, NULL };
	char **argv = argv_;
	if (!gtk_init_check(&argc, &argv)) return -1;
	g_set_prgname("DCCLite");
	g_set_application_name("DCC Lite");
	g_origin = g_strdup(origin);

	char *base = g_build_filename(datadir, "webview-gtk", NULL);
	char *cache = g_build_filename(base, "cache", NULL);
	char *data = g_build_filename(base, "data", NULL);
	g_mkdir_with_parents(cache, 0700);
	g_mkdir_with_parents(data, 0700);
	WebKitWebsiteDataManager *wdm = webkit_website_data_manager_new("base-cache-directory", cache, "base-data-directory", data, NULL);
	WebKitWebContext *ctx = webkit_web_context_new_with_website_data_manager(wdm);
	char *cookies = g_build_filename(data, "cookies.sqlite", NULL);
	webkit_cookie_manager_set_persistent_storage(webkit_website_data_manager_get_cookie_manager(wdm), cookies, WEBKIT_COOKIE_PERSISTENT_STORAGE_SQLITE);
	g_free(cookies); g_free(cache); g_free(data); g_free(base);
	g_signal_connect(ctx, "download-started", G_CALLBACK(on_download), NULL);

	WebKitUserContentManager *ucm = webkit_user_content_manager_new();
	if (!webkit_user_content_manager_register_script_message_handler_with_reply(ucm, "dccLive", NULL)) {
		logf_("warn", "this WebKitGTK is too old for the player bridge (2.40 or newer is needed)");
	}
	g_signal_connect(ucm, "script-message-with-reply-received::dccLive", G_CALLBACK(on_message), NULL);
	char *qurl = js_quote(origin), *qorigin = js_quote(origin);
	char *js = g_strdup_printf(bridge_js, qurl, qorigin);
	WebKitUserScript *us = webkit_user_script_new(js, WEBKIT_USER_CONTENT_INJECT_TOP_FRAME, WEBKIT_USER_SCRIPT_INJECT_AT_DOCUMENT_START, NULL, NULL);
	webkit_user_content_manager_add_script(ucm, us);
	webkit_user_script_unref(us);
	WebKitUserScript *flag = webkit_user_script_new("window.dccLiveGtk = true;", WEBKIT_USER_CONTENT_INJECT_TOP_FRAME, WEBKIT_USER_SCRIPT_INJECT_AT_DOCUMENT_START, NULL, NULL);
	webkit_user_content_manager_add_script(ucm, flag);
	webkit_user_script_unref(flag);
	g_free(js); g_free(qurl); g_free(qorigin);

	g_view = g_object_new(WEBKIT_TYPE_WEB_VIEW, "web-context", ctx, "user-content-manager", ucm, NULL);
	if (!g_view) return -2;
	WebKitSettings *st = webkit_web_view_get_settings(WEBKIT_WEB_VIEW(g_view));
	webkit_settings_set_enable_developer_extras(st, g_getenv("DCC_GTK_DEVTOOLS") != NULL);
	webkit_settings_set_enable_back_forward_navigation_gestures(st, FALSE);
	webkit_settings_set_javascript_can_open_windows_automatically(st, FALSE);
	webkit_settings_set_enable_write_console_messages_to_stdout(st, g_getenv("DCC_GTK_CONSOLE") != NULL);
	g_signal_connect(g_view, "decide-policy", G_CALLBACK(on_decide), NULL);
	g_signal_connect(g_view, "create", G_CALLBACK(on_create), NULL);
	g_signal_connect(g_view, "load-failed", G_CALLBACK(on_load_failed), NULL);
	g_signal_connect(g_view, "web-process-terminated", G_CALLBACK(on_process_crashed), NULL);
	g_signal_connect(g_view, "size-allocate", G_CALLBACK(on_view_size), NULL);

	g_win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
	gtk_window_set_title(GTK_WINDOW(g_win), title);
	G_GNUC_BEGIN_IGNORE_DEPRECATIONS
	gtk_window_set_wmclass(GTK_WINDOW(g_win), "DCCLite", "DCCLite");
	G_GNUC_END_IGNORE_DEPRECATIONS
	gtk_window_set_default_size(GTK_WINDOW(g_win), w, h);
	GdkGeometry geo = { .min_width = 960, .min_height = 620 };
	gtk_window_set_geometry_hints(GTK_WINDOW(g_win), NULL, &geo, GDK_HINT_MIN_SIZE);
	gtk_window_set_position(GTK_WINDOW(g_win), GTK_WIN_POS_CENTER);
	g_signal_connect(g_win, "destroy", G_CALLBACK(on_destroy), NULL);

	g_overlay = gtk_overlay_new();
	gtk_container_add(GTK_CONTAINER(g_overlay), g_view);
	g_video = gtk_drawing_area_new();
	gtk_widget_set_name(g_video, "dcc-video");
	gtk_widget_set_halign(g_video, GTK_ALIGN_START);
	gtk_widget_set_valign(g_video, GTK_ALIGN_START);
	gtk_widget_set_size_request(g_video, 2, 2);
	gtk_widget_set_can_focus(g_video, FALSE);
	gtk_widget_add_events(g_video, GDK_BUTTON_PRESS_MASK | GDK_POINTER_MOTION_MASK | GDK_SCROLL_MASK | GDK_SMOOTH_SCROLL_MASK);
	g_signal_connect(g_video, "draw", G_CALLBACK(on_video_draw), NULL);
	g_signal_connect(g_video, "realize", G_CALLBACK(on_video_realize), NULL);
	g_signal_connect(g_video, "button-press-event", G_CALLBACK(on_video_button), NULL);
	g_signal_connect(g_video, "motion-notify-event", G_CALLBACK(on_video_motion), NULL);
	g_signal_connect(g_video, "scroll-event", G_CALLBACK(on_video_scroll), NULL);
	gtk_overlay_add_overlay(GTK_OVERLAY(g_overlay), g_video);
	gtk_container_add(GTK_CONTAINER(g_win), g_overlay);

	gtk_widget_show_all(g_win);
	gtk_widget_realize(g_video); // so the picture window exists before the first channel
	gtk_widget_hide(g_video);    // and stays out of sight until the page asks for it

	webkit_web_view_load_uri(WEBKIT_WEB_VIEW(g_view), url);
	gtk_main();
	g_win = NULL;
	g_video = NULL;
	g_view = NULL;
	return 0;
}
