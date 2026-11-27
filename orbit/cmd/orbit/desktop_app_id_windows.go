//go:build windows

package main

import (
	_ "embed"
	"path/filepath"

	"github.com/anilyagiz/automdm/v4/orbit/pkg/toast"
	"github.com/rs/zerolog/log"
)

// automdmDesktopIcon is the same icon automdm-desktop.exe embeds.
//
//go:embed windows_app.ico
var automdmDesktopIcon []byte

// registerAutoMDMDesktopAppID gives AutoMDM Desktop's toasts their name and icon.
func registerAutoMDMDesktopAppID(rootDir string) {
	if err := toast.RegisterAutoMDMDesktopAppID(filepath.Join(rootDir, "automdm-desktop.ico"), automdmDesktopIcon); err != nil {
		log.Error().Err(err).Msg("could not register the AutoMDM Desktop notification identity")
	}
}
