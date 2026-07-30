package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ilijad1/rookery/internal/backup"
)

// maxSnapshotUpload bounds a restore upload. It is deliberately far above the
// shared 25 MiB internal/iolimit ingest cap: a legitimate snapshot exceeds that
// as soon as a workspace holds KB attachments, and capping here would break
// restore for exactly the installs with the most to lose.
const maxSnapshotUpload = 8 << 30 // 8 GiB

// backupConfigDTO is the browser-facing shape. It deliberately omits every
// encrypted field and reports only whether a secret is set — the API must never
// hand a stored credential back to the page that stored it.
type backupConfigDTO struct {
	Enabled        bool      `json:"enabled"`
	Destination    string    `json:"destination"`
	Schedule       string    `json:"schedule"`
	Hour           int       `json:"hour"`
	Weekday        int       `json:"weekday"`
	Retention      int       `json:"retention"`
	PassphraseSet  bool      `json:"passphrase_set"`
	LocalDir       string    `json:"local_dir"`
	S3             s3DTO     `json:"s3"`
	LastRunAt      time.Time `json:"last_run_at"`
	LastStatus     string    `json:"last_status"`
	LastError      string    `json:"last_error"`
	LastSize       int64     `json:"last_size"`
	NextRunAt      time.Time `json:"next_run_at"`
	PendingRestore bool      `json:"pending_restore"`
}

type s3DTO struct {
	Endpoint     string `json:"endpoint"`
	Region       string `json:"region"`
	Bucket       string `json:"bucket"`
	Prefix       string `json:"prefix"`
	AccessKey    string `json:"access_key"`
	SecretKeySet bool   `json:"secret_key_set"`
	PathStyle    bool   `json:"path_style"`
}

func toBackupDTO(c *backup.Config, dataDir string) backupConfigDTO {
	return backupConfigDTO{
		Enabled: c.Enabled, Destination: c.Destination, Schedule: c.Schedule,
		Hour: c.Hour, Weekday: c.Weekday, Retention: c.Retention,
		PassphraseSet: c.EncryptedPassphrase != "",
		LocalDir:      c.Local.Dir,
		S3: s3DTO{
			Endpoint: c.S3.Endpoint, Region: c.S3.Region, Bucket: c.S3.Bucket,
			Prefix: c.S3.Prefix, AccessKey: c.S3.AccessKey,
			SecretKeySet: c.S3.EncryptedSecretKey != "", PathStyle: c.S3.PathStyle,
		},
		LastRunAt: c.LastRunAt, LastStatus: c.LastStatus, LastError: c.LastError,
		LastSize: c.LastSize, NextRunAt: c.NextRunAt,
		PendingRestore: backup.HasPendingRestore(dataDir),
	}
}

func (s *Server) handleGetBackupConfig(c echo.Context) error {
	cfg, err := backup.LoadConfig(s.db, s.systemKey)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, toBackupDTO(cfg, s.cfg.Data.Dir))
}

// backupConfigReq carries plaintext secrets inbound only. An empty passphrase
// means "leave the stored one alone", so saving an unrelated field does not
// wipe the credential.
type backupConfigReq struct {
	Enabled     bool   `json:"enabled"`
	Destination string `json:"destination"`
	Schedule    string `json:"schedule"`
	Hour        int    `json:"hour"`
	Weekday     int    `json:"weekday"`
	Retention   int    `json:"retention"`
	Passphrase  string `json:"passphrase"`
	Local       struct {
		Dir string `json:"dir"`
	} `json:"local"`
	S3 struct {
		Endpoint  string `json:"endpoint"`
		Region    string `json:"region"`
		Bucket    string `json:"bucket"`
		Prefix    string `json:"prefix"`
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
		PathStyle bool   `json:"path_style"`
	} `json:"s3"`
}

func (s *Server) handleSaveBackupConfig(c echo.Context) error {
	var req backupConfigReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	cfg, err := backup.LoadConfig(s.db, s.systemKey)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	cfg.Enabled = req.Enabled
	cfg.Destination = req.Destination
	cfg.Schedule = req.Schedule
	cfg.Hour = req.Hour
	cfg.Weekday = req.Weekday
	cfg.Retention = req.Retention
	cfg.Local.Dir = req.Local.Dir
	cfg.S3.Endpoint = req.S3.Endpoint
	cfg.S3.Region = req.S3.Region
	cfg.S3.Bucket = req.S3.Bucket
	cfg.S3.Prefix = req.S3.Prefix
	cfg.S3.AccessKey = req.S3.AccessKey
	cfg.S3.PathStyle = req.S3.PathStyle

	if req.Passphrase != "" {
		if err := cfg.SetPassphrase(s.systemKey, req.Passphrase); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
	}
	if req.S3.SecretKey != "" {
		if err := cfg.SetS3SecretKey(s.systemKey, req.S3.SecretKey); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
	}

	if err := cfg.Validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	// Re-arm the schedule so a cadence change takes effect without a restart.
	cfg.NextRunAt = backup.NextRun(cfg, time.Now())

	if err := backup.SaveConfig(s.db, s.systemKey, cfg); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	s.audit.Log("", "backup_config_save", "backup", cfg.Destination, c.RealIP())
	return c.JSON(http.StatusOK, toBackupDTO(cfg, s.cfg.Data.Dir))
}

func (s *Server) handleRunBackup(c echo.Context) error {
	if s.backupSched == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "backup is not available on this server")
	}
	name, err := s.backupSched.RunOnce(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	s.audit.Log("", "backup_run", name, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]string{"name": name})
}

func (s *Server) backupDestination() (backup.Destination, error) {
	cfg, err := backup.LoadConfig(s.db, s.systemKey)
	if err != nil {
		return nil, err
	}
	return cfg.BuildDestination(s.systemKey)
}

func (s *Server) handleListSnapshots(c echo.Context) error {
	dest, err := s.backupDestination()
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	entries, err := dest.List(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	if entries == nil {
		entries = []backup.Entry{}
	}
	return c.JSON(http.StatusOK, entries)
}

func (s *Server) handleDownloadSnapshot(c echo.Context) error {
	name := c.Param("name")
	if !backup.IsSnapshotName(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "not a snapshot name")
	}
	dest, err := s.backupDestination()
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	rc, err := dest.Get(c.Request().Context(), name)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	defer rc.Close()
	c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="`+name+`"`)
	return c.Stream(http.StatusOK, "application/octet-stream", rc)
}

func (s *Server) handleDeleteSnapshot(c echo.Context) error {
	name := c.Param("name")
	if !backup.IsSnapshotName(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "not a snapshot name")
	}
	dest, err := s.backupDestination()
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := dest.Delete(c.Request().Context(), name); err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	s.audit.Log("", "backup_snapshot_delete", name, "", c.RealIP())
	return c.NoContent(http.StatusNoContent)
}

type snapshotActionReq struct {
	Name       string `json:"name"`
	Passphrase string `json:"passphrase"`
	Confirm    string `json:"confirm"`
}

func (s *Server) handleVerifySnapshot(c echo.Context) error {
	var req snapshotActionReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	rc, err := s.openSnapshotForRead(c, req.Name)
	if err != nil {
		return err
	}
	defer rc.Close()

	schema, err := s.binarySchemaVersion()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	m, err := backup.Verify(rc, req.Passphrase, schema)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{
		"ok": true, "files": len(m.Files), "workspaces": m.WorkspaceCount,
		"created_at": m.CreatedAt, "app_version": m.AppVersion,
	})
}

// handleRestoreSnapshot stages a restore and asks the process to stop. The swap
// itself happens at the top of the next startup, before the database is opened
// — the one path that cannot corrupt a live install.
func (s *Server) handleRestoreSnapshot(c echo.Context) error {
	var req snapshotActionReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if req.Confirm != "RESTORE" {
		return echo.NewHTTPError(http.StatusBadRequest, "type RESTORE to confirm")
	}
	rc, err := s.openSnapshotForRead(c, req.Name)
	if err != nil {
		return err
	}
	defer rc.Close()

	schema, err := s.binarySchemaVersion()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if _, err := backup.StageRestore(rc, s.cfg.Data.Dir, req.Passphrase, schema); err != nil {
		if errors.Is(err, backup.ErrSchemaTooNew) || errors.Is(err, backup.ErrBadPassphrase) ||
			errors.Is(err, backup.ErrCorrupt) || errors.Is(err, backup.ErrSystemKeyConflict) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	s.audit.Log("", "backup_restore_staged", req.Name, "", c.RealIP())

	// Shut down after the response has been flushed, so the browser sees the
	// instruction rather than a dropped connection.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.echo.Shutdown(ctx)
	}()
	return c.JSON(http.StatusOK, map[string]string{
		"status": "staged",
		"message": "The server is stopping to apply the restore. " +
			"If you started it manually, start it again — the restore is applied on the next launch.",
	})
}

// openSnapshotForRead resolves either an uploaded file or a stored snapshot
// name. The uploaded branch streams to a temp file under maxSnapshotUpload
// rather than the shared 25 MiB ingest cap — see that constant.
func (s *Server) openSnapshotForRead(c echo.Context, name string) (io.ReadCloser, error) {
	if fh, err := c.FormFile("file"); err == nil {
		src, err := fh.Open()
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "cannot read the uploaded file")
		}
		defer src.Close()

		tmp, err := os.CreateTemp("", "rookery-restore-*.rkb")
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		if _, err := io.Copy(tmp, io.LimitReader(src, maxSnapshotUpload)); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return nil, echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return nil, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return &tempFileReader{File: tmp}, nil
	}

	if !backup.IsSnapshotName(name) {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "not a snapshot name")
	}
	dest, err := s.backupDestination()
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	rc, err := dest.Get(c.Request().Context(), name)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return rc, nil
}

// tempFileReader removes its backing file on Close.
type tempFileReader struct{ *os.File }

func (t *tempFileReader) Close() error {
	name := t.File.Name()
	err := t.File.Close()
	os.Remove(name)
	return err
}

// binarySchemaVersion reports the newest migration this build ships.
//
// A running server has already applied every migration it carries, so the
// newest APPLIED migration is exactly the newest migration the binary knows
// about — no need to re-read the migrations directory.
func (s *Server) binarySchemaVersion() (string, error) {
	return backup.LatestSchemaVersion(s.db.DB)
}

// registerBackupAPI mounts the owner-scoped backup routes. Backup covers every
// workspace, so these deliberately sit on the owner group and never require an
// active workspace.
func (s *Server) registerBackupAPI(g *echo.Group) {
	g.GET("/backup/config", s.handleGetBackupConfig)
	g.PUT("/backup/config", s.handleSaveBackupConfig)
	g.POST("/backup/run", s.handleRunBackup)
	g.GET("/backup/snapshots", s.handleListSnapshots)
	g.GET("/backup/snapshots/:name/download", s.handleDownloadSnapshot)
	g.DELETE("/backup/snapshots/:name", s.handleDeleteSnapshot)
	g.POST("/backup/verify", s.handleVerifySnapshot)
	g.POST("/backup/restore", s.handleRestoreSnapshot)
}
