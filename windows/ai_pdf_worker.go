//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"serial-tool/internal/aiattachment"
)

const pdfWorkerFlag = "--ai-pdf-extract"

// A separate process keeps a malformed PDF parser or decompression bomb away
// from the GUI. The child assigns itself to a Windows job before opening it.
func readPDFAttachment(path string) (aiattachment.Attachment, error) {
	exe, err := os.Executable()
	if err != nil {
		return aiattachment.Attachment{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, pdfWorkerFlag, path)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out := &boundedOutput{max: aiattachment.MaxTextBytes}
	stderr := &boundedOutput{max: 2048}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return aiattachment.Attachment{}, fmt.Errorf("%s：PDF 提取超过 20 秒", filepath.Base(path))
		}
		if errors.Is(err, errPDFOutputLimit) {
			return aiattachment.Attachment{}, fmt.Errorf("%s：提取的文字超过 64 KiB", filepath.Base(path))
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = "解析进程异常或内存不足"
		}
		detail = strings.SplitN(detail, "\n", 2)[0]
		return aiattachment.Attachment{}, fmt.Errorf("%s：PDF 提取失败（%s）", filepath.Base(path), detail)
	}
	if strings.TrimSpace(out.String()) == "" {
		return aiattachment.Attachment{}, fmt.Errorf("%s 没有可提取的文字；扫描版 PDF 暂不支持", filepath.Base(path))
	}
	return aiattachment.Attachment{Name: filepath.Base(path), Text: out.String()}, nil
}

var errPDFOutputLimit = errors.New("PDF output limit")

type boundedOutput struct {
	bytes.Buffer
	max int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errPDFOutputLimit
	}
	return b.Buffer.Write(p)
}

func runAIPDFWorker(path string) int {
	job, err := windows.CreateJobObject(nil, nil)
	if err == nil {
		defer windows.CloseHandle(job)
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
		info.ProcessMemoryLimit = 256 << 20
		_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
		if err == nil {
			var process windows.Handle
			process, err = windows.GetCurrentProcess()
			if err == nil {
				err = windows.AssignProcessToJobObject(job, process)
			}
		}
	}
	if err != nil {
		fmt.Fprint(os.Stderr, "无法限制 PDF 解析内存：", err)
		return 1
	}
	a, err := aiattachment.Read(path)
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		return 1
	}
	if _, err = os.Stdout.WriteString(a.Text); err != nil {
		return 1
	}
	return 0
}
