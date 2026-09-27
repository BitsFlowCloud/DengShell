#include <assert.h>
#include "wf_dengshell_view.h"
int main(void) {
 DengShellView v=dengshell_view(2560,1600,1280,800);
 assert(v.x==0 && v.y==0 && v.width==2560 && v.height==1600);
 assert(dengshell_pointer(2000,v.x,v.width,1280)==1000);
 v=dengshell_view(1600,900,1024,768);
 assert(v.x==200 && v.y==0 && v.width==1200 && v.height==900);
 assert(dengshell_pointer(800,v.x,v.width,1024)==512);
 assert(dengshell_pointer(450,v.y,v.height,768)==384);
 assert(dengshell_pointer(-10,v.x,v.width,1024)==0);
 assert(dengshell_pointer(1599,v.x,v.width,1024)==1023);
 for(int dw=200;dw<=8192;dw+=379) for(int dh=200;dh<=8192;dh+=487) for(int vw=1;vw<=4096;vw+=257) {
  v=dengshell_view(vw,1050,dw,dh);
  assert(v.width>0 && v.height>0 && v.width<=vw && v.height<=1050);
  assert(v.x>=0 && v.y>=0 && v.x+v.width<=vw && v.y+v.height<=1050);
 }
 return 0;
}
