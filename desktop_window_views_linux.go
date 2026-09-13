//go:build desktop && linux

package main

/*
#cgo pkg-config: gtk+-3.0 x11
#include <gtk/gtk.h>
#include <gdk/gdkx.h>
#include <X11/Xlib.h>
#include <stdlib.h>

typedef struct {
 GMutex mutex;
 GCond condition;
 gint references;
 gboolean done;
 gboolean pointer;
 gboolean x11;
 gboolean visible;
 unsigned long handles[32];
 int count;
} DengWindowQuery;
static void deng_window_query_unref(DengWindowQuery *q) {
 if (g_atomic_int_dec_and_test(&q->references)) {g_cond_clear(&q->condition);g_mutex_clear(&q->mutex);g_free(q);}
}
// All GTK/GDK/X11 operations stay on the GTK main loop. GDK's X11 trap makes
// window destruction during a drag a harmless empty target, never an X error.
static gboolean deng_window_query_run(gpointer data) {
 DengWindowQuery *q=data;
 GdkDisplay *display=gdk_display_get_default();
 g_mutex_lock(&q->mutex);
 q->x11=display && GDK_IS_X11_DISPLAY(display);
 if(q->x11 && q->pointer) {
  Display *xd=gdk_x11_display_get_xdisplay(display);
  Window root=DefaultRootWindow(xd), win=root;
  gdk_x11_display_error_trap_push(display);
  for(int depth=0;depth<32;depth++) {
   Window returned_root=None,child=None;int rx=0,ry=0,wx=0,wy=0;unsigned int mask=0;
   if(!XQueryPointer(xd,win,&returned_root,&child,&rx,&ry,&wx,&wy,&mask)||child==None)break;
   q->handles[q->count++]=child;win=child;
  }
  if(gdk_x11_display_error_trap_pop(display)!=0)q->count=0;
 } else if(q->x11) {
  GList *windows=gtk_window_list_toplevels();
  for(GList *l=windows;l;l=l->next) {
   GtkWindow *w=GTK_WINDOW(l->data);
   if(g_strcmp0(gtk_window_get_title(w),"DengShell")!=0)continue;
   GdkWindow *gw=gtk_widget_get_window(GTK_WIDGET(w));
   if(!gw)continue;
   q->handles[q->count++]=gdk_x11_window_get_xid(gw);
   q->visible=gtk_widget_get_visible(GTK_WIDGET(w)) && !(gdk_window_get_state(gw)&GDK_WINDOW_STATE_ICONIFIED);
   break;
  }
  g_list_free(windows);
 }
 q->done=TRUE;g_cond_signal(&q->condition);g_mutex_unlock(&q->mutex);
 deng_window_query_unref(q);return G_SOURCE_REMOVE;
}
static int deng_window_query(int pointer,unsigned long *handles,int *visible,int *supported) {
 DengWindowQuery *q=g_new0(DengWindowQuery,1);
 g_mutex_init(&q->mutex);g_cond_init(&q->condition);q->references=2;q->pointer=pointer;
 g_mutex_lock(&q->mutex);
 g_idle_add(deng_window_query_run,q);
 gint64 deadline=g_get_monotonic_time()+250*G_TIME_SPAN_MILLISECOND;
 while(!q->done) {if(!g_cond_wait_until(&q->condition,&q->mutex,deadline))break;}
 int count=0;
 if(q->done) {count=q->count;*visible=q->visible;*supported=q->x11;for(int i=0;i<count;i++)handles[i]=q->handles[i];}
 g_mutex_unlock(&q->mutex);deng_window_query_unref(q);return count;
}
*/
import "C"

import "strconv"

func platformWindowViewIdentity() (string, bool) {
	var handles [32]C.ulong
	var visible, supported C.int
	count := C.deng_window_query(0, &handles[0], &visible, &supported)
	if count < 1 {
		return "", true
	}
	return strconv.FormatUint(uint64(handles[0]), 16), visible != 0
}
func platformWindowPointerTargets() ([]string, bool, string) {
	var handles [32]C.ulong
	var visible, supported C.int
	count := C.deng_window_query(1, &handles[0], &visible, &supported)
	if supported == 0 {
		return nil, false, "Wayland 不提供跨窗口鼠标定位，请使用“合并到窗口”菜单"
	}
	ids := make([]string, 0, int(count))
	for i := 0; i < int(count); i++ {
		ids = append(ids, strconv.FormatUint(uint64(handles[i]), 16))
	}
	return ids, true, ""
}
