//go:build linux && native && gtk

#ifndef DCC_GTKWIN_H
#define DCC_GTKWIN_H

int dcc_gtk_run(const char *url, const char *origin, const char *datadir, const char *title, int w, int h);

// The calls below may be made from any thread; they run on the GTK thread.
void dcc_gtk_quit(void);
void dcc_gtk_box(int x, int y, int w, int h, int show);
void dcc_gtk_fullscreen(int on);
void dcc_gtk_cursor(int hide);
void dcc_gtk_ping(void);

// Plain reads, safe from any thread.
unsigned long dcc_gtk_video_xid(void);
void dcc_gtk_view_size(int *w, int *h);

#endif
