//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"serial-tool/core"
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
	ctx, cancel := context.WithTimeout(context.Background(), core.UpdateCheckBudget)
	defer cancel()
	info, err := core.CheckUpdate(ctx, core.Version)
	if a.closed.Load() {
		return
	}
	if err != nil {
		a.appendLog("检查更新失败: " + err.Error())
		return
	}
	if !info.Newer {
		a.appendLog(fmt.Sprintf("已是最新版本 v%s", core.Version))
		return
	}
	if !info.ShouldAutoPrompt() {
		a.appendLog(fmt.Sprintf("发现 v%s,但该版本没有 Windows 安装包,暂不提示", info.Version))
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
	if a.installedUpdate != "" {
		a.promptRestart()
		return
	}
	a.checkingUpdate = true
	a.appendLog("正在检查更新…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), core.UpdateCheckBudget)
		defer cancel()
		info, err := core.CheckUpdate(ctx, core.Version)
		if a.closed.Load() {
			return
		}
		a.mw.Synchronize(func() {
			a.checkingUpdate = false
			if a.closed.Load() {
				return
			}
			if err != nil {
				a.showError(fmt.Errorf("检查更新失败: %w\n\n可手动访问发布页: %s", err, core.UpdateReleasesPage))
				return
			}
			a.showUpdateDialog(info)
		})
	}()
}

// updateSummary 给出对话框顶部那行结论。
func updateSummary(info core.UpdateInfo, current string) string {
	if info.Newer {
		return fmt.Sprintf("发现新版本 v%s（当前 v%s）", info.Version, current)
	}
	return fmt.Sprintf("已是最新版本 v%s", current)
}

// updateDetail 是结论下面那行:发布标题、安装包、大小与下载源。大小未知时不显示。
func updateDetail(info core.UpdateInfo) string {
	if info.AssetName == "" {
		return info.Name
	}
	asset := info.AssetName
	var extra []string
	if info.AssetSize > 0 {
		extra = append(extra, core.FormatBytes(uint64(info.AssetSize)))
	}
	if info.Source != "" {
		extra = append(extra, "来自 "+info.Source)
	}
	if len(extra) > 0 {
		asset += "（" + strings.Join(extra, "，") + "）"
	}
	if info.Name == "" {
		return asset
	}
	return info.Name + " · " + asset
}

// updateProgress 下载进度文案。总长度未知时只报已下载量。
func updateProgress(done, total int64) string {
	if total <= 0 {
		return "正在下载… " + core.FormatBytes(uint64(done))
	}
	return fmt.Sprintf("正在下载… %s / %s（%d%%）",
		core.FormatBytes(uint64(done)), core.FormatBytes(uint64(total)), done*100/total)
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

// showUpdateDialog 显示发布说明。能软件内安装时(Windows 绿色版的 CommBox.exe)下载后
// 直接替换程序文件,提示重启;否则把安装包下到"下载"文件夹,由用户解压覆盖。
func (a *application) showUpdateDialog(info core.UpdateInfo) {
	var dlg *walk.Dialog
	var status *walk.Label
	var download, reveal, restart *walk.PushButton
	var notes *walk.TextEdit
	ctx, cancel := context.WithCancel(context.Background())
	var saved string
	var busy, restartNow bool
	install := core.CanInstallUpdate(info)

	notesText := info.Notes
	if notesText == "" {
		notesText = "（本次发布没有填写说明）"
	}
	downloadText, hint, dir := "下载安装包", "下载完成后请关闭 CommBox 再解压覆盖。", updateDownloadDir()
	if install {
		downloadText, hint, dir = "下载并安装", "安装后重启 CommBox 生效，数据和设置不受影响。", core.UpdateCacheDir()
	}
	fail := func(text string) {
		status.SetText(text)
		status.SetTextColor(colorRed)
		download.SetEnabled(true)
	}
	if err := (Dialog{
		AssignTo: &dlg, Title: "检查更新", Size: Size{Width: 620, Height: 520}, MinSize: Size{Width: 520, Height: 420},
		Font:   Font{Family: fontUI, PointSize: sizeBody},
		Layout: VBox{Alignment: AlignHNearVNear, Margins: Margins{Left: 12, Top: 10, Right: 12, Bottom: 12}, Spacing: 8},
		Children: []Widget{
			Label{Text: updateSummary(info, core.Version), Font: fontSection, TextColor: colorBlue, EllipsisMode: EllipsisEnd},
			Label{Text: updateDetail(info), TextColor: colorMuted, EllipsisMode: EllipsisEnd},
			TextEdit{AssignTo: &notes, ReadOnly: true, VScroll: true, StretchFactor: 1, Text: normalizeNotes(notesText)},
			Label{AssignTo: &status, Text: hint, TextColor: colorMuted, EllipsisMode: EllipsisEnd},
			Composite{Layout: HBox{Alignment: AlignHNearVCenter, MarginsZero: true, Spacing: 8}, Children: []Widget{
				PushButton{AssignTo: &download, Text: downloadText, Image: uiIcon("save"), MinSize: Size{Width: 128, Height: btnH}, MaxSize: Size{Width: 128}, Enabled: info.AssetURL != "", OnClicked: func() {
					if busy {
						return
					}
					busy = true
					download.SetEnabled(false)
					status.SetText("正在下载…")
					status.SetTextColor(colorMuted)
					go func() {
						path, err := core.DownloadUpdate(ctx, info, dir, func(done, total int64) {
							text := updateProgress(done, total)
							a.mw.Synchronize(func() {
								if !status.IsDisposed() {
									status.SetText(text)
								}
							})
						})
						var exe string
						if err == nil && install {
							a.mw.Synchronize(func() {
								if !status.IsDisposed() {
									status.SetText("正在安装…")
								}
							})
							exe, err = core.InstallUpdate(path)
							os.Remove(path)
						}
						a.mw.Synchronize(func() {
							busy = false
							if err == nil && install {
								// 对话框关了也要记下:之后再检查更新只提示重启,不再装一遍
								a.installedUpdate, a.installedExe = info.Version, exe
								a.appendLog(fmt.Sprintf("v%s 已安装，重启 CommBox 后生效", info.Version))
							}
							if status.IsDisposed() {
								return
							}
							switch {
							case err != nil && install:
								fail("安装失败: " + err.Error())
							case err != nil:
								fail("下载失败: " + err.Error())
							case install:
								status.SetText(fmt.Sprintf("v%s 已安装，点“立即重启”或下次启动时生效。", info.Version))
								status.SetTextColor(colorGreen)
								restart.SetEnabled(true)
							default:
								saved = path
								status.SetText("已下载到 " + path + "，关闭 CommBox 后解压覆盖即可。")
								status.SetTextColor(colorGreen)
								reveal.SetEnabled(true)
								a.appendLog("更新包已下载: " + path)
							}
						})
					}()
				}},
				PushButton{AssignTo: &restart, Text: "立即重启", Visible: install, MinSize: Size{Width: 120, Height: btnH}, MaxSize: Size{Width: 120}, Enabled: false, OnClicked: func() {
					restartNow = true
					dlg.Accept()
				}},
				PushButton{AssignTo: &reveal, Text: "打开所在文件夹", Visible: !install, MinSize: Size{Width: 140, Height: btnH}, MaxSize: Size{Width: 140}, Enabled: false, OnClicked: func() {
					if saved != "" {
						revealInExplorer(saved)
					}
				}},
				PushButton{Text: "打开发布页", MinSize: Size{Width: 120, Height: btnH}, MaxSize: Size{Width: 120}, OnClicked: func() {
					page := info.PageURL
					if page == "" {
						page = core.UpdateReleasesPage
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
	// 关窗即取消下载,半截文件由 DownloadUpdate 自己清理;已经开始的安装很快,会照常装完。
	dlg.Closing().Attach(func(*bool, walk.CloseReason) { cancel() })
	defer dlg.Dispose()
	dlg.Run()
	cancel()
	if restartNow {
		a.restartForUpdate()
	}
}

// promptRestart 在新版本已装好、还没重启时代替检查更新:正在运行的仍是旧版本,再检查只会再装一遍。
func (a *application) promptRestart() {
	text := fmt.Sprintf("v%s 已安装，重启 CommBox 后生效。\n\n现在重启吗？正在进行的连接和发送任务会结束。", a.installedUpdate)
	if walk.MsgBox(a.mw, "检查更新", text, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == walk.DlgCmdYes {
		a.restartForUpdate()
	}
}

// restartForUpdate 走正常退出(结束连接、关闭 VirtualCOM 与数据库),退出后启动新版本。
func (a *application) restartForUpdate() {
	a.restartAfterExit = true
	a.mw.Close()
}

// startInstalledUpdate 在退出前启动已装好的新版本。main 最后调用,此时窗口和数据库都已关闭。
func (a *application) startInstalledUpdate() {
	if !a.restartAfterExit || a.installedExe == "" {
		return
	}
	if err := core.StartInstalledUpdate(a.installedExe); err != nil {
		walk.MsgBox(nil, "CommBox", "新版本已安装，但没能自动启动："+chineseError(err)+"\n\n请手动运行 "+a.installedExe, walk.MsgBoxOK|walk.MsgBoxIconWarning)
	}
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
