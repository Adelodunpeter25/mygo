package mygo

import "context"

// The page methods of a window, from before they moved to Page. A window
// showing native UI has no page: they report it or do nothing.

// Deprecated: Use Page().LoadURL.
func (w *Window) LoadURL(rawURL string) error { return w.pg.LoadURL(rawURL) }

// Deprecated: Use Page().LoadFile.
func (w *Window) LoadFile(path string) error { return w.pg.LoadFile(path) }

// Deprecated: Use Page().LoadHTML.
func (w *Window) LoadHTML(html, baseURL string) { w.pg.LoadHTML(html, baseURL) }

// Deprecated: Use Page().Reload.
func (w *Window) Reload() { w.pg.Reload() }

// Deprecated: Use Page().ReloadIgnoringCache.
func (w *Window) ReloadIgnoringCache() { w.pg.ReloadIgnoringCache() }

// Deprecated: Use Page().Stop.
func (w *Window) Stop() { w.pg.Stop() }

// Deprecated: Use Page().GoBack.
func (w *Window) GoBack() { w.pg.GoBack() }

// Deprecated: Use Page().GoForward.
func (w *Window) GoForward() { w.pg.GoForward() }

// Deprecated: Use Page().CanGoBack.
func (w *Window) CanGoBack() bool { return w.pg.CanGoBack() }

// Deprecated: Use Page().CanGoForward.
func (w *Window) CanGoForward() bool { return w.pg.CanGoForward() }

// Deprecated: Use Page().URL.
func (w *Window) URL() string { return w.pg.URL() }

// Deprecated: Use Page().IsLoading.
func (w *Window) IsLoading() bool { return w.pg.IsLoading() }

// Deprecated: Use Page().SetZoomFactor.
func (w *Window) SetZoomFactor(f float64) { w.pg.SetZoomFactor(f) }

// Deprecated: Use Page().ZoomFactor.
func (w *Window) ZoomFactor() float64 { return w.pg.ZoomFactor() }

// Deprecated: Use Page().SetUserAgent.
func (w *Window) SetUserAgent(ua string) { w.pg.SetUserAgent(ua) }

// Deprecated: Use Page().UserAgent.
func (w *Window) UserAgent() string { return w.pg.UserAgent() }

// Deprecated: Use Page().OpenDevTools.
func (w *Window) OpenDevTools() { w.pg.OpenDevTools() }

// Deprecated: Use Page().CloseDevTools.
func (w *Window) CloseDevTools() { w.pg.CloseDevTools() }

// Deprecated: Use Page().IsDevToolsOpened.
func (w *Window) IsDevToolsOpened() bool { return w.pg.IsDevToolsOpened() }

// Deprecated: Use Page().ToggleDevTools.
func (w *Window) ToggleDevTools() { w.pg.ToggleDevTools() }

// Deprecated: Use Page().Print.
func (w *Window) Print() { w.pg.Print() }

// Deprecated: Use Page().Eval.
func (w *Window) Eval(code string) (any, error) { return w.pg.Eval(code) }

// Deprecated: Use Page().EvalContext.
func (w *Window) EvalContext(ctx context.Context, code string) (any, error) {
	return w.pg.EvalContext(ctx, code)
}

// Deprecated: Use Page().SetWindowOpenHandler.
func (w *Window) SetWindowOpenHandler(fn func(req WindowOpenRequest) *WindowOptions) {
	w.pg.SetWindowOpenHandler(fn)
}

// Deprecated: Use Page().OnPageTitleUpdated.
func (w *Window) OnPageTitleUpdated(fn func(e *TitleEvent)) (off func()) {
	return w.pg.OnPageTitleUpdated(fn)
}

// Deprecated: Use Page().OnWillNavigate.
func (w *Window) OnWillNavigate(fn func(e *NavigateEvent)) (off func()) {
	return w.pg.OnWillNavigate(fn)
}

// Deprecated: Use Page().OnDidNavigate.
func (w *Window) OnDidNavigate(fn func(url string)) (off func()) { return w.pg.OnDidNavigate(fn) }

// Deprecated: Use Page().OnDOMReady.
func (w *Window) OnDOMReady(fn func()) (off func()) { return w.pg.OnDOMReady(fn) }

// Deprecated: Use Page().OnDidFinishLoad.
func (w *Window) OnDidFinishLoad(fn func()) (off func()) { return w.pg.OnDidFinishLoad(fn) }

// Deprecated: Use Page().OnDidFailLoad.
func (w *Window) OnDidFailLoad(fn func(err *LoadError)) (off func()) { return w.pg.OnDidFailLoad(fn) }

// Deprecated: Use Page().OnRenderProcessGone.
func (w *Window) OnRenderProcessGone(fn func(reason string)) (off func()) {
	return w.pg.OnRenderProcessGone(fn)
}

// Deprecated: Use Page().PrintToPDF.
func (w *Window) PrintToPDF(opts PDFOptions) ([]byte, error) { return w.pg.PrintToPDF(opts) }

// Deprecated: Use Page().FindInPage.
func (w *Window) FindInPage(text string, opts FindOptions) (FindResult, error) {
	return w.pg.FindInPage(text, opts)
}

// Deprecated: Use Page().StopFindInPage.
func (w *Window) StopFindInPage() { w.pg.StopFindInPage() }

// Deprecated: Use Page().OnWillDownload.
func (w *Window) OnWillDownload(fn func(e *DownloadEvent)) (off func()) {
	return w.pg.OnWillDownload(fn)
}

// Deprecated: Use Page().OnDownloadDone.
func (w *Window) OnDownloadDone(fn func(d *Download)) (off func()) { return w.pg.OnDownloadDone(fn) }

// Deprecated: Use Page().SetPermissionHandler.
func (w *Window) SetPermissionHandler(fn func(req PermissionRequest) bool) {
	w.pg.SetPermissionHandler(fn)
}
