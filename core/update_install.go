package core

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// 软件内安装只支持 Windows 绿色版:下载的 zip 解压后直接替换 CommBox.exe 所在目录里的文件。
// macOS 仍是下载 DMG 后由用户拖进"应用程序",Linux 只发命令行版。
const (
	updateMainExe   = "CommBox.exe"
	updateOldSuffix = ".old"                    // 被替换下来的旧文件,下次启动时删除
	updateOldList   = ".commbox-update-old"     // 记下换下来的旧文件,只删这些,不碰目录里别的 *.old
	updateStageDir  = ".commbox-update-staged"  // 解压新文件的临时目录,与程序同盘,改名才不会跨盘
	updateCacheName = "CommBox-update-download" // 软件内安装时安装包的下载目录名
)

// ErrUpdateUnsupported 表示当前平台或运行方式不能软件内安装,只能下载后手动安装。
var ErrUpdateUnsupported = errors.New("当前平台或运行方式不支持软件内安装")

// updateInstallDir 返回要替换的程序目录。只认 Windows 上名为 CommBox.exe 的程序,
// go run 出来的临时 exe、改过名的 exe 都不自动替换。
func updateInstallDir() (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	if !strings.EqualFold(filepath.Base(exe), updateMainExe) {
		return "", false
	}
	return filepath.Dir(exe), true
}

// CanInstallUpdate 判断这次发布能否在软件内直接安装。
func CanInstallUpdate(info UpdateInfo) bool {
	_, ok := updateInstallDir()
	return ok && info.AssetURL != "" && strings.HasSuffix(info.AssetName, ".zip")
}

// UpdateCacheDir 是软件内安装时下载安装包的目录,装好后安装包即删除。
func UpdateCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "CommBox", updateCacheName)
}

// InstallUpdate 用下载好的 zip 替换正在运行的程序,返回重启时要运行的 exe。
// 安装包已在下载时按发布说明的 SHA256 校验过。
func InstallUpdate(pkg string) (string, error) {
	dir, ok := updateInstallDir()
	if !ok {
		return "", ErrUpdateUnsupported
	}
	return installZip(pkg, dir)
}

// installZip 把 zip 里的文件装进 dir。Windows 不能覆盖正在运行的 exe,但可以改名:
// 旧文件改名为 *.old,新文件放到原位置,任何一步失败都把已改名的文件改回来。
// 用户数据在 %APPDATA%\CommBox,不在程序目录,不受影响。
func installZip(pkg, dir string) (string, error) {
	z, err := zip.OpenReader(pkg)
	if err != nil {
		return "", fmt.Errorf("安装包不是有效的 zip: %w", err)
	}
	defer z.Close()

	stage := filepath.Join(dir, updateStageDir)
	os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return "", fmt.Errorf("程序目录不能写入(%w),请下载安装包后手动解压覆盖", err)
	}
	defer os.RemoveAll(stage)

	var files []string // 相对 dir 的路径
	hasMain := false
	for _, f := range z.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		clean := path.Clean(strings.ReplaceAll(f.Name, `\`, "/"))
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, ":") {
			return "", fmt.Errorf("安装包里的路径不安全: %s", f.Name)
		}
		rel := filepath.FromSlash(clean)
		dst := filepath.Join(stage, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := extractZipFile(f, dst); err != nil {
			return "", fmt.Errorf("解压 %s 失败: %w", f.Name, err)
		}
		files = append(files, rel)
		hasMain = hasMain || strings.EqualFold(rel, updateMainExe)
	}
	if !hasMain {
		return "", fmt.Errorf("安装包里没有 %s", updateMainExe)
	}

	type moved struct{ target, old string }
	var done []moved
	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			os.Remove(done[i].target)
			if done[i].old != "" {
				os.Rename(done[i].old, done[i].target)
			}
		}
	}
	for _, rel := range files {
		target := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			rollback()
			return "", err
		}
		m := moved{target: target}
		if _, err := os.Stat(target); err == nil {
			m.old = target + updateOldSuffix
			os.Remove(m.old) // 上次更新换下来、还没删掉的
			if err := os.Rename(target, m.old); err != nil {
				rollback()
				return "", fmt.Errorf("不能替换 %s(%w),请关掉占用它的程序后重试,或下载安装包手动更新", rel, err)
			}
		}
		done = append(done, m)
		if err := os.Rename(filepath.Join(stage, rel), target); err != nil {
			rollback()
			return "", fmt.Errorf("不能写入 %s: %w", rel, err)
		}
	}
	var old []string
	for _, m := range done {
		if m.old != "" {
			old = append(old, m.old)
		}
	}
	addOldFiles(dir, old) // 记不下来只是旧文件留着,新版本已经装好
	return filepath.Join(dir, updateMainExe), nil
}

// addOldFiles 把换下来的旧文件追加进清单。连续更新两次而中间没重启时,上一次的也还在里面。
func addOldFiles(dir string, old []string) {
	list := filepath.Join(dir, updateOldList)
	seen := map[string]bool{}
	var names []string
	for _, name := range append(readOldFiles(list), old...) {
		name = filepath.Base(name)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	os.WriteFile(list, []byte(strings.Join(names, "\n")+"\n"), 0o644)
}

func readOldFiles(list string) []string {
	data, err := os.ReadFile(list)
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		// 只认本目录的 *.old 文件名:清单被改坏时也不会删到别的文件或别的目录
		if name := strings.TrimSpace(line); name != "" && !strings.ContainsAny(name, `\/:`) && strings.HasSuffix(name, updateOldSuffix) {
			names = append(names, name)
		}
	}
	return names
}

func extractZipFile(f *zip.File, dst string) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// CleanupUpdate 删除上次更新换下来的旧文件和残留的解压目录、下载目录。
// 启动时调用,失败不影响运行:刚重启时旧进程可能还没退出,删不掉的下次再删。
func CleanupUpdate() {
	dir, ok := updateInstallDir()
	if !ok {
		return
	}
	cleanupUpdateDir(dir)
	os.RemoveAll(UpdateCacheDir())
}

func cleanupUpdateDir(dir string) {
	os.RemoveAll(filepath.Join(dir, updateStageDir))
	list := filepath.Join(dir, updateOldList)
	var left []string
	for _, name := range readOldFiles(list) {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			left = append(left, name)
		}
	}
	if len(left) == 0 {
		os.Remove(list)
		return
	}
	os.WriteFile(list, []byte(strings.Join(left, "\n")+"\n"), 0o644)
}

// StartInstalledUpdate 启动新安装的程序,调用方随后退出当前进程。
func StartInstalledUpdate(exe string) error {
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	return cmd.Start()
}
