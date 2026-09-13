//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"serial-tool/internal/wincore"
	"strconv"
	"sync/atomic"
	"time"
)

func (a *application) openHTTPWorkspace() {
	var dlg *walk.Dialog
	var method *walk.ComboBox
	var address, timeout, connectTimeout *walk.LineEdit
	var headers, body, curlText, responseBody, responseHeaders *walk.TextEdit
	var follow, insecure, preserve, pretty *walk.CheckBox
	var send, cancelButton *walk.PushButton
	var status *walk.Label
	var imported wincore.HTTPRequestSpec
	var result wincore.HTTPResponseResult
	var cancel context.CancelFunc
	var disposed atomic.Bool
	fields := func() httpWorkspaceFields {
		return httpWorkspaceFields{base: imported, method: method.Text(), url: address.Text(), headers: headers.Text(), body: body.Text(), timeout: timeout.Text(), connectTimeout: connectTimeout.Text(), preserveBody: preserve.Checked(), follow: follow.Checked(), insecure: insecure.Checked()}
	}
	showBody := func() {
		b := result.RawBody
		if pretty.Checked() && len(result.PrettyBody) > 0 {
			b = result.PrettyBody
		}
		responseBody.SetText(httpWorkspaceBounded(string(b)))
	}
	report := func(err error) { status.SetText("错误：" + httpWorkspaceBounded(err.Error())) }
	err := (Dialog{
		AssignTo: &dlg, Title: "HTTP 工作区", MinSize: Size{Width: 800, Height: 660}, Size: Size{Width: 1040, Height: 800}, Layout: VBox{},
		Children: []Widget{
			Label{Text: "先在主窗口选择 HTTP 客户端并连接，再发送请求；同一连接自动保留 Cookie。"},
			Composite{Layout: HBox{}, Children: []Widget{
				ComboBox{AssignTo: &method, Model: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}, CurrentIndex: 0, Editable: true, MinSize: Size{Width: 90}},
				LineEdit{AssignTo: &address, Text: "http://127.0.0.1:8080/", StretchFactor: 1},
				PushButton{AssignTo: &send, Text: "发送请求", OnClicked: func() {
					f := fields()
					if _, err := f.spec(); err != nil {
						report(err)
						return
					}
					ctx, c := context.WithCancel(context.Background())
					cancel = c
					send.SetEnabled(false)
					cancelButton.SetEnabled(true)
					status.SetText("请求中…")
					go func() {
						r, err := httpWorkspaceRequest(ctx, a.engine, f)
						c()
						if disposed.Load() {
							return
						}
						a.mw.Synchronize(func() {
							if disposed.Load() || dlg.IsDisposed() {
								return
							}
							cancel = nil
							send.SetEnabled(true)
							cancelButton.SetEnabled(false)
							if err != nil {
								report(err)
								return
							}
							result = r
							status.SetText(fmt.Sprintf("%s  |  %d bytes  |  %s", r.Status, r.ByteSize, r.Duration.Round(time.Millisecond)))
							responseHeaders.SetText(httpWorkspaceBounded(httpWorkspaceHeaders(r.Headers)))
							showBody()
						})
					}()
				}},
				PushButton{AssignTo: &cancelButton, Text: "取消", Enabled: false, OnClicked: func() {
					if cancel != nil {
						cancel()
						status.SetText("正在取消…")
					}
				}},
			}},
			Composite{Layout: HBox{}, Children: []Widget{
				Label{Text: "总超时(s)"}, LineEdit{AssignTo: &timeout, Text: "30", MaxSize: Size{Width: 70}},
				Label{Text: "连接超时(s)"}, LineEdit{AssignTo: &connectTimeout, Text: "10", MaxSize: Size{Width: 70}},
				CheckBox{AssignTo: &follow, Text: "跟随重定向", Checked: true}, CheckBox{AssignTo: &insecure, Text: "跳过 TLS 证书验证"}, HSpacer{},
			}},
			TabWidget{MinSize: Size{Height: 200}, Pages: []TabPage{
				{Title: "请求头", Layout: VBox{}, Children: []Widget{Label{Text: "每行一个 Name: Value，支持重复请求头"}, TextEdit{AssignTo: &headers, VScroll: true, HScroll: true, MaxLength: 262144}}},
				{Title: "请求体", Layout: VBox{}, Children: []Widget{
					CheckBox{AssignTo: &preserve, Text: "保留导入的 cURL data/form 请求体（下方显示参数，包括文件引用）", OnCheckedChanged: func() {
						body.SetReadOnly(preserve.Checked())
						if !preserve.Checked() {
							body.SetText("")
						}
					}},
					TextEdit{AssignTo: &body, VScroll: true, HScroll: true, MaxLength: 1048576},
				}},
				{Title: "cURL 导入 / 导出", Layout: VBox{}, Children: []Widget{
					Label{Text: "仅解析，不执行 shell。导出内容可能包含认证信息；仅显示在此处，不自动复制或保存。"},
					TextEdit{AssignTo: &curlText, VScroll: true, HScroll: true, MaxLength: 1048576},
					Composite{Layout: HBox{}, Children: []Widget{
						PushButton{Text: "导入到请求", OnClicked: func() {
							s, err := wincore.ParseCURL(curlText.Text())
							if err != nil {
								report(err)
								return
							}
							imported = s
							method.SetText(s.Method)
							address.SetText(s.URL)
							headers.SetText(httpWorkspaceHeaders(s.Headers))
							timeout.SetText(strconv.FormatFloat(s.Timeout.Seconds(), 'f', -1, 64))
							connectTimeout.SetText(strconv.FormatFloat(s.ConnectTimeout.Seconds(), 'f', -1, 64))
							follow.SetChecked(s.FollowRedirects)
							insecure.SetChecked(s.Insecure)
							structured := len(s.Data) > 0 || len(s.Form) > 0
							preserve.SetChecked(structured)
							body.SetReadOnly(structured)
							if structured {
								b, _ := json.MarshalIndent(struct {
									Data []wincore.HTTPDataPart
									Form []wincore.HTTPFormField
								}{s.Data, s.Form}, "", "  ")
								body.SetText(string(b))
							} else {
								body.SetText(string(s.Body))
							}
							status.SetText("已导入；认证、Cookie 和 User-Agent 参数保留在请求中。发送前请检查 cURL 及请求体文件引用。")
						}},
						PushButton{Text: "生成 cURL", OnClicked: func() {
							s, err := fields().spec()
							if err != nil {
								report(err)
								return
							}
							text, err := wincore.FormatCURL(s)
							if err != nil {
								report(err)
								return
							}
							curlText.SetText(text)
							status.SetText("已生成 cURL（可能包含认证信息）。")
						}},
					}},
				}},
			}},
			Label{AssignTo: &status, Text: "就绪；尚未发送请求"},
			CheckBox{AssignTo: &pretty, Text: "格式化 JSON 响应", Checked: true, OnCheckedChanged: showBody},
			TabWidget{MinSize: Size{Height: 220}, Pages: []TabPage{
				{Title: "响应正文", Layout: VBox{}, Children: []Widget{TextEdit{AssignTo: &responseBody, ReadOnly: true, VScroll: true, HScroll: true, MaxLength: 300000}}},
				{Title: "响应头", Layout: VBox{}, Children: []Widget{TextEdit{AssignTo: &responseHeaders, ReadOnly: true, VScroll: true, HScroll: true, MaxLength: 300000}}},
			}},
		},
	}).Create(a.mw)
	if err != nil {
		a.showError(err)
		return
	}
	dlg.Disposing().Attach(func() {
		disposed.Store(true)
		if cancel != nil {
			cancel()
		}
	})
	dlg.Show()
}
