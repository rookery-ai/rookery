package web

import (
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ilijad1/rookery/web/ui"
	"github.com/labstack/echo/v4"
)

// setupSPARoutes mounts the embedded SPA at the site root. The catch-all
// GET /* is registered LAST (after every API, static, callback, and template
// route) so it only handles genuinely-unmatched paths — Echo gives explicit
// routes precedence over the /* param route. The legacy /app and /app/* paths
// 301-redirect to their /app-stripped equivalents (the SPA moved from /app to
// /), preserving the query string.
func (s *Server) setupSPARoutes() {
	distFS, ok := ui.DistFS()
	h := s.spaHandler(distFS, ok)
	s.echo.GET("/app", redirectAppPath)
	s.echo.GET("/app/*", redirectAppPath)
	// Echo's /* param route does not match the bare root path "/", so register
	// the exact root explicitly; the catch-all handles every deeper path.
	s.echo.GET("/", h)
	s.echo.GET("/*", h)
}

// redirectAppPath 301-redirects a legacy /app or /app/* request to the same
// path with the /app prefix stripped, preserving the raw query string. It is a
// pure path rewrite and fires regardless of whether the UI is built.
//
//	/app            → /
//	/app/kb         → /kb
//	/app/kb?path=z  → /kb?path=z
func redirectAppPath(c echo.Context) error {
	target := strings.TrimPrefix(c.Request().URL.Path, "/app")
	if target == "" {
		target = "/"
	}
	if q := c.Request().URL.RawQuery; q != "" {
		target += "?" + q
	}
	return c.Redirect(http.StatusMovedPermanently, target)
}

// spaHandler serves real files from the embedded dist and falls back to
// index.html for client-side routes. With no built UI it answers 503 so a
// node-less build still runs the API cleanly (SPA serves a 503 until built).
func (s *Server) spaHandler(distFS fs.FS, ok bool) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !ok {
			return c.String(http.StatusServiceUnavailable, "UI not built — run `make ui`, then rebuild the binary")
		}
		p := strings.TrimPrefix(c.Request().URL.Path, "/")
		if p != "" {
			if st, err := fs.Stat(distFS, p); err == nil && !st.IsDir() {
				return serveFile(c, p, distFS)
			}
		}
		return serveFile(c, "index.html", distFS)
	}
}

// serveFile serves a file from an fs.FS using the Echo context.
func serveFile(c echo.Context, file string, filesystem fs.FS) error {
	f, err := filesystem.Open(file)
	if err != nil {
		return c.NoContent(http.StatusNotFound)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	ff, ok := f.(io.ReadSeeker)
	if !ok {
		return c.String(http.StatusInternalServerError, "file does not implement io.ReadSeeker")
	}

	// Pass *echo.Response (implements http.ResponseWriter), not the raw
	// http.ResponseWriter.Writer — this keeps Echo's logger status/size
	// accounting correct. echo.Context has no FileFS method in v4.15.2 (it's
	// on the concrete *echo.Echo/Group, not the interface), so we can't use
	// c.FileFS here.
	http.ServeContent(c.Response(), c.Request(), fi.Name(), fi.ModTime(), ff)
	return nil
}
