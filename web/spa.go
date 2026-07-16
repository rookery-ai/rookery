package web

import (
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ilijad1/simple-agents/web/ui"
	"github.com/labstack/echo/v4"
)

// setupSPARoutes mounts the embedded SPA at /app. Until sub-plan 6 removes the
// template UI, /app is the only SPA mount point; the cutover to / happens there.
func (s *Server) setupSPARoutes() {
	distFS, ok := ui.DistFS()
	h := s.spaHandler(distFS, ok)
	s.echo.GET("/app", h)
	s.echo.GET("/app/*", h)
}

// spaHandler serves real files from the embedded dist and falls back to
// index.html for client-side routes. With no built UI it answers 503 so a
// node-less build still runs the API + template UI cleanly.
func (s *Server) spaHandler(distFS fs.FS, ok bool) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !ok {
			return c.String(http.StatusServiceUnavailable, "UI not built — run `make ui`, then rebuild the binary")
		}
		p := strings.TrimPrefix(c.Request().URL.Path, "/app")
		p = strings.TrimPrefix(p, "/")
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
