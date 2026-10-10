package core

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeZip 按给定的文件名和内容写一个 zip,模拟发布的 Windows 绿色版包(平铺,没有顶层目录)。
func writeZip(t *testing.T, files [][2]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "CommBox-9.9.9-Windows-x64.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, file := range files {
		e, err := w.Create(file[0])
		if err != nil {
			t.Fatal(err)
		}
		e.Write([]byte(file[1]))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstallZipReplacesFiles(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"CommBox.exe": "old exe", "README-Windows.txt": "old readme", "keep.old": "用户自己的文件", "notes.txt": "用户的笔记"})
	pkg := writeZip(t, [][2]string{{"CommBox.exe", "new exe"}, {"README-Windows.txt", "new readme"}, {"CommBox使用手册.md", "manual"}})

	exe, err := installZip(pkg, dir)
	if err != nil {
		t.Fatal(err)
	}
	if exe != filepath.Join(dir, "CommBox.exe") {
		t.Fatalf("重启路径错误: %q", exe)
	}
	for name, want := range map[string]string{"CommBox.exe": "new exe", "README-Windows.txt": "new readme", "CommBox使用手册.md": "manual", "CommBox.exe.old": "old exe", "notes.txt": "用户的笔记"} {
		if got := readFile(t, filepath.Join(dir, name)); got != want {
			t.Fatalf("%s = %q, 应为 %q", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, updateStageDir)); !os.IsNotExist(err) {
		t.Fatal("解压目录应已删除")
	}

	cleanupUpdateDir(dir)
	for _, name := range []string{"CommBox.exe.old", "README-Windows.txt.old", updateOldList} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("清理后 %s 应已删除", name)
		}
	}
	// 不是这次换下来的 *.old 不能删:程序可能放在"下载"这类杂乱的目录里
	if got := readFile(t, filepath.Join(dir, "keep.old")); got != "用户自己的文件" {
		t.Fatal("不应删除用户自己的 .old 文件")
	}
}

// 删不掉的旧文件(旧进程还没退出)留在清单里,下次再删;已删掉的从清单里去掉。
func TestCleanupKeepsUndeletedOldFiles(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.exe.old": "1"})
	if err := os.Mkdir(filepath.Join(dir, "busy.exe.old"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 非空目录 os.Remove 删不掉,用来模拟正在运行、删不掉的旧 exe
	writeFiles(t, filepath.Join(dir, "busy.exe.old"), map[string]string{"x": "x"})
	addOldFiles(dir, []string{filepath.Join(dir, "a.exe.old"), filepath.Join(dir, "busy.exe.old"), "missing.exe.old"})

	cleanupUpdateDir(dir)
	if _, err := os.Stat(filepath.Join(dir, "a.exe.old")); !os.IsNotExist(err) {
		t.Fatal("能删的旧文件应已删除")
	}
	if got := readOldFiles(filepath.Join(dir, updateOldList)); len(got) != 1 || got[0] != "busy.exe.old" {
		t.Fatalf("清单里应只剩删不掉的文件: %q", got)
	}
}

// 清单被改坏时只认本目录的 *.old 文件名,不会删到别处。
func TestReadOldFilesIgnoresUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, updateOldList)
	os.WriteFile(list, []byte("ok.exe.old\n../evil.old\nC:\\Windows\\x.old\nnotes.txt\n\n"), 0o644)
	if got := readOldFiles(list); len(got) != 1 || got[0] != "ok.exe.old" {
		t.Fatalf("只应认出 ok.exe.old: %q", got)
	}
}

// 中途失败时把已替换的文件全部换回去,程序目录保持原样。
func TestInstallZipRollsBack(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"CommBox.exe": "old exe", "sub": "这是个文件,挡住同名目录"})
	pkg := writeZip(t, [][2]string{{"CommBox.exe", "new exe"}, {"sub/a.txt", "x"}})

	if _, err := installZip(pkg, dir); err == nil {
		t.Fatal("目标路径被文件挡住时应报错")
	}
	if got := readFile(t, filepath.Join(dir, "CommBox.exe")); got != "old exe" {
		t.Fatalf("失败后应恢复旧 exe, 实际 %q", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), updateOldSuffix) || e.Name() == updateStageDir || e.Name() == updateOldList {
			t.Fatalf("失败后不应留下 %s", e.Name())
		}
	}
}

func TestInstallZipRejectsBadPackages(t *testing.T) {
	for name, files := range map[string][][2]string{
		"缺主程序":  {{"README-Windows.txt", "x"}},
		"路径穿越":  {{"CommBox.exe", "x"}, {"../evil.exe", "x"}},
		"绝对路径":  {{"CommBox.exe", "x"}, {"/evil.exe", "x"}},
		"盘符路径":  {{"CommBox.exe", "x"}, {"C:/evil.exe", "x"}},
		"反斜杠穿越": {{"CommBox.exe", "x"}, {`..\evil.exe`, "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{"CommBox.exe": "old exe"})
			if _, err := installZip(writeZip(t, files), dir); err == nil {
				t.Fatal("应拒绝这个安装包")
			}
			if got := readFile(t, filepath.Join(dir, "CommBox.exe")); got != "old exe" {
				t.Fatalf("拒绝后程序不应被改动: %q", got)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.exe")); !os.IsNotExist(err) {
				t.Fatal("不应写到程序目录外")
			}
		})
	}
	if _, err := installZip(filepath.Join(t.TempDir(), "missing.zip"), t.TempDir()); err == nil {
		t.Fatal("不是 zip 时应报错")
	}
}

// 测试程序不叫 CommBox.exe,不能软件内安装,只提供下载。
func TestCanInstallUpdateNeedsCommBoxExe(t *testing.T) {
	info := UpdateInfo{AssetURL: "https://gitee.com/a.zip", AssetName: "CommBox-9.9.9-Windows-x64.zip"}
	if CanInstallUpdate(info) {
		t.Fatal("测试程序不是 CommBox.exe,不应允许软件内安装")
	}
	if _, err := InstallUpdate("x.zip"); err != ErrUpdateUnsupported {
		t.Fatalf("应返回 ErrUpdateUnsupported, 得到 %v", err)
	}
}
