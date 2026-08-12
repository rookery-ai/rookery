package web

import (
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/profile"
)

// setupStep determines which onboarding wizard step a workspace is on based on its
// state. Shared by the JSON setup API (api_settings.go) which drives the wizard.
//
//	1=basics (name+about) 2=master_password 3=coder 4=profile 5=connector 7=done
func setupStep(w *db.Workspace, database *db.DB) int {
	if database == nil {
		return 7
	}
	if v, _ := database.GetSetting(w.ID, "wizard_basics_done"); v != "1" {
		return 1
	}
	if w.SecretsSalt == "" {
		return 2
	}
	if v, _ := database.GetSetting(w.ID, "wizard_coder_done"); v != "1" {
		return 3
	}
	if !profile.IsComplete(database, w.ID) {
		return 4
	}
	conns, _ := database.ListWorkspacePlatformConnections(w.ID)
	if skipped, _ := database.GetSetting(w.ID, "wizard_connector_skipped"); len(conns) == 0 && skipped != "1" {
		return 5
	}
	return 7 // Done
}
