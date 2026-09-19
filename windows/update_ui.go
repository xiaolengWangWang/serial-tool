//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"serial-tool/internal/wincore"
)

// settingAutoUpdate 记录是否在启动时检查更新。检查只发一个 GET,
// 不带任何用户数据,因此默认开启;关掉后完全不联网。
const settingAutoUpdate = "update.auto_check"

// autoUpdateEnabled 空值按开启处理:老用户升级上来不需要先去设置里打开。
func (a *application) autoUpdateEnabled() bool {
	return a.engine.GetSetting(settingAutoUpdate) != "0"
}

func (a *application) toggleAutoUpdate() {
	if a.autoUpdateAction == nil {
		return
	}
	on := a.autoUpdateAction.Checked()
	value := "0"
	if on {
		value = "1"
	}
	if err := a.engine.SetSetting(settingAutoUpdate, value); err != nil {
		a.showError(err)
		return
	}
	a.appendLog(map[bool]string{true: "已开启启动时检查更新", false: "已关闭启动时检查更新"}[on])
}

// autoCheckUpdate 启动后台检查:只有确实存在新版本才打扰用户,
// 网络不通或接口出错只写日志,不弹窗。
func (a *application) autoCheckUpdate() {
	if !a.autoUpdateEnabled() {
		return
	}
	// 让出启动阶段:端口枚举、SQLite 打开都在这几秒里。
	time.Sleep(3 * time.Second)
	if a.closed.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	info, err := wincore.CheckUpdate(ctx, wincore.Version)
	if a.closed.Load() {
		return
	}
	if err != nil {
		a.appendLog("检查更新失败: " + err.Error())
		return
	}
	if !info.Newer {
		a.appendLog(fmt.Sprintf("已是最新版本 v%s", wincore.Version))
		return
	}
	a.mw.Synchronize(func() {
		if !a.closed.Load() {
			a.showUpdateDialog(info)
		}
	})
}

// checkUpdate 是"帮助 → 检查更新"的手动入口:无论结果如何都给出反馈。
func (a *application) checkUpdate() {
	if a.checkingUpdate {
		return
	}
	a.checkingUpdate = true
	a.appendLog("正在检查更新…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		info, err := wincore.CheckUpdate(ctx, wincore.Version)
		if a.closed.Load() {
			return
		}
		a.mw.Synchronize(func() {
			a.checkingUpdate = false
			if a.closed.Load() {
				return
			}
			if err != nil {
				a.showError(fmt.Errorf("检查更新失败: %w\n\n可手动访问发布页: %s", err, updateReleasesPage))
				return
			}
			a.showUpdateDialog(info)
		})
	}()
}

const updateReleasesPage = "https://github.com/xiaolengWangWang/serial-tool/releases"

// updateSummary 给出对话框顶部那行结论。
func updateSummary(info wincore.UpdateInfo, current string) string {
	if info.Newer {
		return fmt.Sprintf("发现新版本 v%s（当前 v%s）", info.Version, current)
	}
	return fmt.Sprintf("已是最新版本 v%s", current)
}

// updateDetail 是结论下面那行:发布标题与安装包大小。
func updateDetail(info wincore.UpdateInfo) string {
	if info.AssetName == "" {
		return info.Name
	}
	size := wincore.FormatBytes(uint64(info.AssetSize))
	if info.Name == "" {
		return fmt.Sprintf("%s · %s", info.AssetName, size)
	}
	return fmt.Sprintf("%s · %s（%s）", info.Name, info.AssetName, size)
}

// updateProgress 下载进度文案。总长度未知时只报已下载量。
func updateProgress(done, total int64) string {
	if total <= 0 {
		return "正在下载… " + wincore.FormatBytes(uint64(done))
	}
	return fmt.Sprintf("正在下载… %s / %s（%d%%）",
		wincore.FormatBytes(uint64(done)), wincore.FormatBytes(uint64(total)), done*100/total)
}

// updateDownloadDir 优先放到"下载"文件夹,取不到时退回临时目录。
func updateDownloadDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		dir := filepath.Join(home, "Downloads")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return filepath.Join(os.TempDir(), "CommBox-update")
}

func (a *application) showUpdateDialog(info wincore.UpdateInfo) {
	var dlg *walk.Dialog
	var status *walk.Label
	var download, reveal *walk.PushButton
	var notes *walk.TextEdit
	ctx, cancel := context.WithCancel(context.Background())
	var saved string
	var busy bool

	notesText := info.Notes
	if notesText == "" {
		notesText = "（本次发布没有填写说明）"
	}
	if err := (Dialog{
		AssignTo: &dlg, Title: "检查更新", Size: Size{Width: 620, Height: 520}, MinSize: Size{Width: 520, Height: 420},
		Font:   Font{Family: fontUI, PointSize: sizeBody},
		Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 12, Top: 10, Right: 12, Bottom: 12}, Spacing: 8},
		Children: []Widget{
			Label{Text: updateSummary(info, wincore.Version), Font: fontSection, TextColor: colorBlue, EllipsisMode: EllipsisEnd},
			Label{Text: updateDetail(info), TextColor: colorMuted, EllipsisMode: EllipsisEnd},
			TextEdit{AssignTo: &notes, ReadOnly: true, VScroll: true, StretchFactor: 1, Text: normalizeNotes(notesText)},
			Label{AssignTo: &status, Text: "下载完成后请关闭 CommBox 再解压覆盖。", TextColor: colorMuted, EllipsisMode: EllipsisEnd},
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 8}, Children: []Widget{
				PushButton{AssignTo: &download, Text: "下载安装包", Image: uiIcon("save"), MinSize: Size{Width: 128, Height: btnH}, MaxSize: Size{Width: 128}, Enabled: info.AssetURL != "", OnClicked: func() {
					if busy {
						return
					}
					busy = true
					download.SetEnabled(false)
					status.SetText("正在下载…")
					dir := updateDownloadDir()
					go func() {
						path, err := wincore.DownloadUpdate(ctx, info, dir, func(done, total int64) {
							text := updateProgress(done, total)
							a.mw.Synchronize(func() {
								if !status.IsDisposed() {
									status.SetText(text)
								}
							})
						})
						a.mw.Synchronize(func() {
							busy = false
							if status.IsDisposed() {
								return
							}
							if err != nil {
								status.SetText("下载失败: " + err.Error())
								status.SetTextColor(colorRed)
								download.SetEnabled(true)
								return
							}
							saved = path
							status.SetText("已下载到 " + path + "，关闭 CommBox 后解压覆盖即可。")
							status.SetTextColor(colorGreen)
							reveal.SetEnabled(true)
							a.appendLog("更新包已下载: " + path)
						})
					}()
				}},
				PushButton{AssignTo: &reveal, Text: "打开所在文件夹", MinSize: Size{Width: 140, Height: btnH}, MaxSize: Size{Width: 140}, Enabled: false, OnClicked: func() {
					if saved != "" {
						revealInExplorer(saved)
					}
				}},
				PushButton{Text: "打开发布页", MinSize: Size{Width: 120, Height: btnH}, MaxSize: Size{Width: 120}, OnClicked: func() {
					page := info.PageURL
					if page == "" {
						page = updateReleasesPage
					}
					openExternalURL(page)
				}},
				HSpacer{},
				PushButton{Text: "关闭", MinSize: Size{Width: 88, Height: btnH}, MaxSize: Size{Width: 88}, OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}).Create(a.mw); err != nil {
		cancel()
		a.showError(err)
		return
	}
	// 关窗即取消下载,半截文件由 DownloadUpdate 自己清理。
	dlg.Closing().Attach(func(*bool, walk.CloseReason) { cancel() })
	defer dlg.Dispose()
	dlg.Run()
	cancel()
}

// normalizeNotes 把发布说明的换行统一成 CRLF,否则 TextEdit 里会连成一行。
func normalizeNotes(s string) string {
	out := make([]rune, 0, len(s)+len(s)/40)
	var prev rune
	for _, r := range s {
		if r == '\n' && prev != '\r' {
			out = append(out, '\r')
		}
		out = append(out, r)
		prev = r
	}
	return string(out)
}

func openExternalURL(page string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", page).Start()
}

func revealInExplorer(path string) {
	_ = exec.Command("explorer.exe", "/select,", filepath.Clean(path)).Start()
}
