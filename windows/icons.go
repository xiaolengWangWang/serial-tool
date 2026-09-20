//go:build windows

package main

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/lxn/walk"
)

//go:embed icons/*.ico
var iconFiles embed.FS
var toolbarIcons = map[string]*walk.Icon{}

func uiIcon(name string) *walk.Icon {
	if icon := toolbarIcons[name]; icon != nil {
		return icon
	}
	if name == "app" {
		// Resource 2 is the application icon in rsrc_windows_amd64.syso.
		// Loading it directly also works when the disk icon cache is unavailable.
		icon, err := walk.NewIconFromResourceIdWithSize(2, walk.Size{Width: 32, Height: 32})
		if err != nil {
			icon = walk.IconApplication()
		}
		toolbarIcons[name] = icon
		return icon
	}
	data, err := iconFiles.ReadFile("icons/" + name + ".ico")
	if err != nil {
		return nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(cache, "CommBox", "icons-v082")
	if os.MkdirAll(dir, 0700) != nil {
		return nil
	}
	path := filepath.Join(dir, name+".ico")
	if os.WriteFile(path, data, 0600) != nil {
		return nil
	}
	icon, err := walk.NewIconFromFileWithSize(path, walk.Size{Width: 24, Height: 24})
	if err != nil {
		return nil
	}
	toolbarIcons[name] = icon
	return icon
}
