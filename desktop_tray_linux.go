//go:build desktop && linux

package main

/*
#cgo pkg-config: gtk+-3.0
#cgo LDFLAGS: -ldl
#include <gtk/gtk.h>
#include <dlfcn.h>
#include <unistd.h>
static gpointer deng_indicator=NULL;
static gint deng_tray_connected=0,deng_tray_events=0;
static void(*deng_set_status)(gpointer,int)=NULL;
static void deng_tray_open(GtkMenuItem *i,gpointer d){g_atomic_int_or(&deng_tray_events,1);}
static void deng_tray_quit(GtkMenuItem *i,gpointer d){g_atomic_int_or(&deng_tray_events,2);}
static void deng_connected(gpointer i,gboolean connected,gpointer d){g_atomic_int_set(&deng_tray_connected,connected);}
static gboolean deng_tray_create(gpointer filename){
 void *lib=dlopen("libayatana-appindicator3.so.1",RTLD_NOW|RTLD_LOCAL);if(!lib)lib=dlopen("libappindicator3.so.1",RTLD_NOW|RTLD_LOCAL);if(!lib){g_free(filename);return FALSE;}
 gpointer(*create)(const char*,const char*,int)=dlsym(lib,"app_indicator_new");void(*menu)(gpointer,GtkMenu*)=dlsym(lib,"app_indicator_set_menu");deng_set_status=dlsym(lib,"app_indicator_set_status");
 if(!create||!menu||!deng_set_status){g_free(filename);return FALSE;}
 char id[80];g_snprintf(id,sizeof(id),"dengshell-%d",getpid());deng_indicator=create(id,(const char*)filename,0);g_free(filename);if(!deng_indicator)return FALSE;
 GtkWidget *items=gtk_menu_new(),*open=gtk_menu_item_new_with_label("打开 DengShell"),*quit=gtk_menu_item_new_with_label("退出 DengShell");gtk_menu_shell_append(GTK_MENU_SHELL(items),open);gtk_menu_shell_append(GTK_MENU_SHELL(items),quit);g_signal_connect(open,"activate",G_CALLBACK(deng_tray_open),NULL);g_signal_connect(quit,"activate",G_CALLBACK(deng_tray_quit),NULL);gtk_widget_show_all(items);menu(deng_indicator,GTK_MENU(items));g_signal_connect(deng_indicator,"connection-changed",G_CALLBACK(deng_connected),NULL);deng_set_status(deng_indicator,1);
 gboolean connected=FALSE;g_object_get(deng_indicator,"connected",&connected,NULL);g_atomic_int_set(&deng_tray_connected,connected);return FALSE;
}
static void deng_tray_start(char *filename){g_idle_add(deng_tray_create,filename);}
static int deng_tray_available(){return g_atomic_int_get(&deng_tray_connected);}
// g_atomic_int_exchange requires GLib 2.74. Keep the native release compatible
// with Ubuntu 22.04's GLib 2.72 while atomically preserving concurrent events.
static int deng_tray_take_events(){int events;do{events=g_atomic_int_get(&deng_tray_events);}while(!g_atomic_int_compare_and_exchange(&deng_tray_events,events,0));return events;}
static gboolean deng_tray_stop(gpointer ignored){if(deng_indicator&&deng_set_status)deng_set_status(deng_indicator,0);g_atomic_int_set(&deng_tray_connected,0);return FALSE;}
static void deng_tray_close(){g_idle_add(deng_tray_stop,NULL);}
static gboolean deng_present(gpointer ignored){GList *windows=gtk_window_list_toplevels();for(GList *l=windows;l;l=l->next){GtkWindow *w=GTK_WINDOW(l->data);if(g_strcmp0(gtk_window_get_title(w),"DengShell")==0){gtk_window_present(w);break;}}g_list_free(windows);return FALSE;}
static void deng_raise(){g_idle_add(deng_present,NULL);}
*/
import "C"

func platformInitializeTray(path string) { C.deng_tray_start(C.CString(path)) }
func platformTrayAvailable() bool        { return C.deng_tray_available() != 0 }
func platformTrayEvents() int            { return int(C.deng_tray_take_events()) }
func platformCloseTray()                 { C.deng_tray_close() }
func platformRaiseWindow()               { C.deng_raise() }
