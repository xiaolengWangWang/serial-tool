//go:build windows

package main

import (
	"embed"
	"github.com/lxn/walk"
	"os"
	"path/filepath"
)

//go:embed icons/*.ico
var iconFiles embed.FS
var toolbarIcons = map[string]*walk.Icon{}

func uiIcon(name string) *walk.Icon {
	if icon := toolbarIcons[name]; icon != nil {
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
	size := 24
	if name == "app" {
		size = 32
	}
	icon, err := walk.NewIconFromFileWithSize(path, walk.Size{Width: size, Height: size})
	if err != nil {
		return nil
	}
	toolbarIcons[name] = icon
	return icon
}
