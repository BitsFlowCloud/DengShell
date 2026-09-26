package com.bitsflow.dengshell;

import android.app.Activity;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.graphics.Color;
import android.graphics.Insets;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.SystemClock;
import android.provider.DocumentsContract;
import android.util.Log;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.webkit.ConsoleMessage;
import android.webkit.SslErrorHandler;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.webkit.WebSettings;
import android.webkit.MimeTypeMap;
import android.webkit.URLUtil;
import android.widget.FrameLayout;
import android.widget.TextView;
import android.widget.Toast;
import android.net.http.SslError;

import java.io.ByteArrayInputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.Locale;

import go.Seq;
import mobile.Mobile;

public final class MainActivity extends Activity {
    private static final String TAG = "DengShellAndroid";
    private static final int PICK_UPLOAD = 41;
    private static final int SAVE_DOWNLOAD = 42;

    private WebView webView;
    private TextView status;
    private ValueCallback<Uri[]> fileResult;
    private volatile String localOrigin;
    private volatile String bootstrapToken;
    private String downloadUrl;
    private volatile boolean destroyed;
    private long lastExitBackAt;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        getWindow().setNavigationBarColor(Color.rgb(248, 251, 250));
        getWindow().setStatusBarColor(Color.rgb(248, 251, 250));
        int systemUiFlags = View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR;
        if (Build.VERSION.SDK_INT >= 26) systemUiFlags |= View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR;
        getWindow().getDecorView().setSystemUiVisibility(systemUiFlags);
        WebView.setWebContentsDebuggingEnabled((getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0);
        buildView();
        Seq.setContext(getApplicationContext());
        new Thread(() -> {
            try {
                String address = Mobile.start(getFilesDir().getAbsolutePath());
                Uri parsed = Uri.parse(address);
                String origin = parsed.getScheme() + "://" + parsed.getAuthority();
                String token = parsed.getFragment();
                if (token == null || !token.startsWith("token=") || !"127.0.0.1".equals(parsed.getHost())) {
                    throw new IllegalStateException("本地服务地址无效");
                }
                localOrigin = origin;
                bootstrapToken = Uri.decode(token.substring(6));
                runOnUiThread(() -> {
                    if (!destroyed && webView != null) webView.loadUrl(address);
                });
            } catch (Exception error) {
                Log.e(TAG, "Backend failed to start", error);
                runOnUiThread(() -> {
                    if (!destroyed) showStatus("无法启动 DengShell：" + error.getMessage());
                });
            }
        }, "dengshell-backend").start();
    }

    private void buildView() {
        FrameLayout root = new FrameLayout(this);
        root.setBackgroundColor(Color.rgb(238, 243, 245));
        webView = new WebView(this);
        webView.setBackgroundColor(Color.rgb(238, 243, 245));
        root.addView(webView, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        status = new TextView(this);
        status.setText("正在启动 DengShell…");
        status.setTextColor(Color.rgb(33, 65, 75));
        status.setTextSize(16);
        status.setGravity(Gravity.CENTER);
        status.setPadding(24, 24, 24, 24);
        status.setBackgroundColor(Color.rgb(238, 243, 245));
        root.addView(status, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        if (Build.VERSION.SDK_INT >= 35) {
            // Android 15 draws target-35 apps behind the system bars. Keep the
            // terminal, bottom navigation and keyboard above those insets.
            root.setOnApplyWindowInsetsListener((view, windowInsets) -> {
                Insets bars = windowInsets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout());
                Insets ime = windowInsets.getInsets(WindowInsets.Type.ime());
                view.setPadding(bars.left, bars.top, bars.right, Math.max(bars.bottom, ime.bottom));
                return WindowInsets.CONSUMED;
            });
        }
        setContentView(root);

        WebSettings settings = webView.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setAllowFileAccess(false);
        settings.setAllowContentAccess(true);
        settings.setSupportZoom(false);
        settings.setBuiltInZoomControls(false);
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        settings.setMediaPlaybackRequiresUserGesture(true);
        if (Build.VERSION.SDK_INT >= 26) settings.setSafeBrowsingEnabled(true);
        webView.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                Uri uri = request.getUrl();
                if (isLocal(uri)) return false;
                if (!request.isForMainFrame()) return true;
                if ("https".equals(uri.getScheme())) {
                    try { startActivity(new Intent(Intent.ACTION_VIEW, uri)); }
                    catch (ActivityNotFoundException error) { showToast("没有可打开此链接的浏览器"); }
                }
                return true;
            }
            @Override public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
                Uri uri = request.getUrl();
                String scheme = uri.getScheme();
                if (("http".equals(scheme) || "https".equals(scheme)) && !isLocal(uri)) {
                    return new WebResourceResponse("text/plain", "UTF-8", 403, "Forbidden",
                            null, new ByteArrayInputStream(new byte[0]));
                }
                return null;
            }
            @Override public void onPageFinished(WebView view, String url) {
                if (isLocal(Uri.parse(url))) {
                    // The embedded UI uses window.open for its website links.
                    // WebView has no popup window, so route HTTPS links through
                    // main-frame navigation and the browser handler above.
                    view.evaluateJavascript("(function(){window.open=function(url){if(typeof url==='string'&&url.indexOf('https://')===0){location.href=url;}return null;};document.addEventListener('click',function(event){var a=event.target.closest('a[target=\"_blank\"]');if(a&&a.href.indexOf('https://')===0){event.preventDefault();location.href=a.href;}},true);})()", null);
                    status.setVisibility(View.GONE);
                }
            }
            @Override public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) showStatus("页面加载失败，请重新打开应用");
            }
            @Override public void onReceivedSslError(WebView view, SslErrorHandler handler, SslError error) {
                handler.cancel();
            }
        });
        webView.setWebChromeClient(new WebChromeClient() {
            @Override public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> callback, FileChooserParams params) {
                if (fileResult != null) fileResult.onReceiveValue(null);
                fileResult = callback;
                try { startActivityForResult(params.createIntent(), PICK_UPLOAD); }
                catch (Exception error) {
                    fileResult = null;
                    callback.onReceiveValue(null);
                    showToast("无法打开文件选择器");
                }
                return true;
            }
            @Override public boolean onConsoleMessage(ConsoleMessage message) {
                if (message.messageLevel() == ConsoleMessage.MessageLevel.ERROR) Log.e(TAG, message.message());
                return true;
            }
        });
        webView.setDownloadListener((url, userAgent, disposition, mimeType, contentLength) -> {
            Uri uri = Uri.parse(url);
            if (!isLocal(uri)) { showToast("已阻止非本地下载"); return; }
            downloadUrl = url;
            String fileName = suggestedDownloadName(uri, disposition, mimeType);
            Intent intent = new Intent(Intent.ACTION_CREATE_DOCUMENT);
            intent.addCategory(Intent.CATEGORY_OPENABLE);
            intent.setType(mimeTypeForName(fileName, mimeType));
            intent.putExtra(Intent.EXTRA_TITLE, fileName);
            try { startActivityForResult(intent, SAVE_DOWNLOAD); }
            catch (ActivityNotFoundException error) { downloadUrl = null; showToast("无法选择保存位置"); }
        });
    }

    private static String suggestedDownloadName(Uri uri, String disposition, String mimeType) {
        // The backend serves every SFTP file from /download with a generic
        // octet-stream MIME type. URLUtil otherwise proposes download.bin.
        String remotePath = uri.getQueryParameter("path");
        if (remotePath != null) {
            String baseName = remotePath.substring(remotePath.lastIndexOf('/') + 1);
            StringBuilder clean = new StringBuilder(baseName.length());
            for (int i = 0; i < baseName.length(); i++) {
                char c = baseName.charAt(i);
                clean.append(c == '/' || c == '\\' || Character.isISOControl(c) ? '_' : c);
            }
            String name = clean.toString();
            if (!name.isEmpty() && !name.equals(".") && !name.equals("..")) return name;
        }
        return URLUtil.guessFileName(uri.toString(), disposition, mimeType);
    }

    private static String mimeTypeForName(String name, String provided) {
        int dot = name.lastIndexOf('.');
        if (dot > 0 && dot < name.length() - 1) {
            String extension = name.substring(dot + 1).toLowerCase(Locale.ROOT);
            String known = MimeTypeMap.getSingleton().getMimeTypeFromExtension(extension);
            if (known != null && !known.isEmpty()) return known;
        }
        return provided == null || provided.isEmpty() ? "application/octet-stream" : provided;
    }

    private boolean isLocal(Uri uri) {
        return localOrigin != null && "http".equals(uri.getScheme()) &&
                "127.0.0.1".equals(uri.getHost()) &&
                localOrigin.equals(uri.getScheme() + "://" + uri.getAuthority());
    }

    private void showStatus(String text) {
        status.setText(text);
        status.setVisibility(View.VISIBLE);
    }

    private void showToast(String text) { Toast.makeText(this, text, Toast.LENGTH_LONG).show(); }

    @Override protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == PICK_UPLOAD) {
            if (fileResult == null) return;
            fileResult.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(resultCode, data));
            fileResult = null;
            return;
        }
        if (requestCode == SAVE_DOWNLOAD && resultCode == RESULT_OK && data != null && data.getData() != null && downloadUrl != null) {
            String url = downloadUrl;
            Uri target = data.getData();
            downloadUrl = null;
            showToast("正在保存文件，完成后会提示");
            new Thread(() -> saveDownload(url, target), "dengshell-download").start();
        } else if (requestCode == SAVE_DOWNLOAD) {
            downloadUrl = null;
        }
    }

    private void saveDownload(String address, Uri destination) {
        HttpURLConnection conn = null;
        try {
            Uri parsed = Uri.parse(address);
            if (!isLocal(parsed)) throw new IllegalArgumentException("下载来源无效");
            conn = (HttpURLConnection) new URL(address).openConnection();
            conn.setConnectTimeout(15000);
            conn.setReadTimeout(30000);
            conn.setRequestProperty("X-CloudShell-Token", bootstrapToken);
            int status = conn.getResponseCode();
            if (status != 200) throw new IllegalStateException("下载失败（HTTP " + status + "）");
            try (InputStream input = conn.getInputStream(); OutputStream output = getContentResolver().openOutputStream(destination)) {
                if (output == null) throw new IllegalStateException("无法写入选定位置");
                byte[] buffer = new byte[64 * 1024];
                int count;
                while ((count = input.read(buffer)) != -1) output.write(buffer, 0, count);
            }
            runOnUiThread(() -> showToast("文件已保存"));
        } catch (Exception error) {
            Log.e(TAG, "Download failed", error);
            try { DocumentsContract.deleteDocument(getContentResolver(), destination); }
            catch (Exception ignored) { /* Some providers do not support deleting a new document. */ }
            runOnUiThread(() -> showToast("保存失败：" + error.getMessage()));
        } finally {
            if (conn != null) conn.disconnect();
        }
    }

    @Override public void onBackPressed() {
        if (!destroyed && webView != null) {
            webView.evaluateJavascript("(function(){const ds=document.querySelectorAll('dialog[open]');const d=ds[ds.length-1];if(d){const cancel=new Event('cancel',{cancelable:true});const close=d.dispatchEvent(cancel);if(close&&d.open)d.close();return true;}const drawer=document.getElementById('connections-drawer');if(drawer&&!drawer.hidden){document.getElementById('close-connections').click();return true;}if(window.DengShellMobile&&window.DengShellMobile.back&&window.DengShellMobile.back()){return true;}return document.querySelector('#session-tabs .session-tab')?'connected':false;})()", value -> {
                if ("true".equals(value)) {
                    lastExitBackAt = 0;
                    return;
                }
                if ("\"connected\"".equals(value)) {
                    long now = SystemClock.elapsedRealtime();
                    if (now - lastExitBackAt > 2000) {
                        lastExitBackAt = now;
                        showToast("再按一次返回退出，SSH 连接将断开");
                        return;
                    }
                }
                MainActivity.super.onBackPressed();
            });
        } else super.onBackPressed();
    }

    @Override protected void onDestroy() {
        destroyed = true;
        if (fileResult != null) { fileResult.onReceiveValue(null); fileResult = null; }
        if (webView != null) {
            webView.destroy();
            webView = null;
        }
        if (isFinishing()) new Thread(Mobile::stop, "dengshell-stop").start();
        super.onDestroy();
    }
}
