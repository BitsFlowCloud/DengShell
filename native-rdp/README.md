# Embedded RDP preview (Windows x64)

The Windows test executable contains FreeRDP 3.32.0 and OpenSSL 3.6.4.
It does not launch mstsc or instantiate the Microsoft RDP ActiveX control.
The engine is extracted into the current user's application cache and checked
against its embedded bytes before each launch. All dependencies are linked
statically, except standard Windows system DLLs.

## Architecture

- One FreeRDP process and one authenticated, destination-scoped loopback SOCKS
  tunnel per tab. The tunnel reuses DengShell's direct, SOCKS5 or HTTP CONNECT
  dialer, including upstream authentication and remote DNS. Multitransport/UDP
  is explicitly disabled so RDP traffic cannot bypass that tunnel.
- Credentials and arguments use stdin, never command-line arguments. No
  certificate-verification bypass is set. An untrusted server certificate
  requires explicit approval; changed certificates require a new decision.
- The desktop is fitted to its viewport with its aspect ratio preserved; painting,
  damage rectangles and pointer coordinates use the same transform. Automatic resolution is the default, matching the physical viewport for
  pixel-for-pixel rendering. Fixed 1024x768, 1280x800, 1600x900 and 1920x1080
  options remain available; fitting a smaller desktop can soften text.
  The toolbar reports actual framebuffer dimensions returned by the engine.
  The child process matches the host DPI awareness before creating windows;
  remote desktop scale follows the local Windows DPI. Font smoothing and
  32-bit color are requested explicitly.
- DisplayControl capability negotiation and a throttled resize timer handle
  programmatically resized child windows (which never get WM_EXITSIZEMOVE).
  Servers without that channel still get aspect-preserving local fitting;
  this fallback cannot add detail to a low-resolution source.
- The GFX channel advertises Progressive/RFX codecs and the normal graphics
  cache, with legacy RDP fallback when the server does not support the channel.
  No H.264/GPU acceleration is claimed by this build.
- Repeated identical viewport reports do not move, resize or show the native
  child again. Actual size changes remain immediate. Painting copies only the
  damaged region at native resolution and clears only the letterbox bars,
  never the desktop beneath the frame.
- Native child windows occupy the DOM viewport measured by the active tab.
  Inactive tabs and surfaces covered by dialogs/menus are hidden and disabled.
  Keyboard capture requires the actual visible child window to have focus.
- A Windows Job Object terminates the engine when its owner exits. Closing a
  tab or locking DengShell closes its RDP transport and engine.
- Clipboard redirection is opt-in. No local drives, printers or microphone
  are forwarded by the application. Ctrl+Alt+Del uses an explicit toolbar action.

## Rebuild

On Linux, install Python 3, GNU tar with zstd, patch, CMake, Ninja and MinGW-w64
(`x86_64-w64-mingw32-gcc/g++/windres`). Run:

```sh
python3 native-rdp/build-engine.py /absolute/path/to/empty/build-directory
```

The builder downloads the exact sources recorded in `sources.json`, checks
their SHA-256 values, applies `freerdp-3.32.0.patch`, builds on CPU 0–3 when
available and writes `internal/rdpengine/payload/windows-amd64.zip`.
WinPR internal MD4/RC4 are linked for protocol compatibility (NTLM/licensing);
this avoids a missing external OpenSSL legacy-provider dependency. TLS and
certificate verification remain enabled.
Build the desktop with Go's `desktop,production` tags as usual.

The patch adds ready/input-activity messages, embedded-window positioning,
focus isolation and secure-attention delivery; disables mstsc credential lookup;
and displays SHA-256 certificate fingerprints instead of raw PEM data.

## Preview limits and verification

The upstream Windows `wfreerdp` frontend is deprecated; FreeRDP recommends SDL3
for its standalone client. This preview maintains a small native embedding
patch and is not a production qualification of that frontend. H.264 hardware
decoding, audio/video and Windows-specific input methods are not certified.

Go race tests cover profile isolation, proxy authentication and destination
scope, no direct fallback, multiple sessions, locking and process teardown.
Browser tests cover tabs, viewport bounds, SSH switching, dialogs/menus,
resizing and both themes. `TestRDPEmbeddedNativeIntegration` is opt-in and uses
an isolated RDP fixture: it has been exercised under Wine with real remote
desktop output, keyboard input, resizing, hiding, locking and both proxy types.
Full Wails/WebView2 integration still needs a native Windows test machine.

Upstream source URLs and licenses are bundled beside this file and inside the
engine archive. No release or website publication is performed by this builder.

Viewport math regression test (after applying the engine patch):

```sh
gcc -Wall -Wextra -Werror -fsanitize=address,undefined \
  -I /path/to/engine-source/client/Windows native-rdp/test-view.c -o /tmp/rdp-view-test
/tmp/rdp-view-test
```

The native fixture also checks the **remote** X11 desktop dimensions after
manual and automatic resize, and the remote mouse coordinates after scaling.
A separate run disables the server's dynamic virtual channel to exercise fitting
without server-side resolution changes, including enlargement to 2000x1250.

A live Windows server check also exercises authenticated NLA, a 2000x1250
remote desktop and automatic embedded resizing. Wine testing uses
`WINPR_NATIVE_SSPI=0` because Wine's SSPI path crashes with this target;
production Windows keeps its normal native SSPI path. Test credentials are
supplied through an opt-in private JSON file and never embedded or logged.
