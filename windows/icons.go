//go:build windows

package main

import (
	"embed"
	"os"
	"path/filepath"
	"strconv"

	"github.com/lxn/walk"
)

//go:embed icons/*.ico
var iconFiles embed.FS
var toolbarIcons = map[string]*walk.Icon{}

func uiIcon(name string) *walk.Icon {
	return sizedIcon(name, 24)
}

// smallIcon 取 .ico 自带的 16px 帧，放进状态栏这类约 20px 高的行里不会缩得发虚。
func smallIcon(name string) *walk.Icon {
	return sizedIcon(name, 16)
}

func sizedIcon(name string, px int) *walk.Icon {
	key := name
	if px != 24 {
		key += "@" + strconv.Itoa(px)
	}
	if icon := toolbarIcons[key]; icon != nil {
		return icon
	}
	if name == "app" {
		// Resource 2 is the application icon in rsrc_windows_amd64.syso.
		// Loading it directly also works when the disk icon cache is unavailable.
		icon, err := walk.NewIconFromResourceIdWithSize(2, walk.Size{Width: 32, Height: 32})
		if err != nil {
			icon = walk.IconApplication()
		}
		toolbarIcons[key] = icon
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
	icon, err := walk.NewIconFromFileWithSize(path, walk.Size{Width: px, Height: px})
	if err != nil {
		return nil
	}
	toolbarIcons[key] = icon
	return icon
}
