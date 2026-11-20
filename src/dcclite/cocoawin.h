//go:build darwin && native

#ifndef DCC_COCOAWIN_H
#define DCC_COCOAWIN_H

int dcc_cocoa_run(const char *url, const char *origin, const char *datadir, const char *title, int w, int h);

void dcc_cocoa_quit(void);
void dcc_cocoa_box(int x, int y, int w, int h, int show);
void dcc_cocoa_fullscreen(int on);
void dcc_cocoa_cursor(int hide);
void dcc_cocoa_ping(void);

unsigned long dcc_cocoa_video_view(void);
void dcc_cocoa_view_size(int *w, int *h);

#endif
