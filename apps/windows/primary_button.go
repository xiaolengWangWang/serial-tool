//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// Keep native buttons (keyboard, accessibility and click handling); only paint
// the three primary actions with the design's accent color.
type primaryButtonParent struct {
	previous uintptr
	buttons  map[win.HWND]*walk.PushButton
}

var primaryParents = map[win.HWND]*primaryButtonParent{}
var primaryButtonProc = syscall.NewCallback(func(h win.HWND, msg uint32, wp, lp uintptr) uintptr {
	p := primaryParents[h]
	if msg == win.WM_NOTIFY && lp != 0 {
		// lParam is a borrowed native pointer, valid only during this callback.
		n := *(**win.NMHDR)(unsafe.Pointer(&lp))
		if b := p.buttons[n.HwndFrom]; b != nil && n.Code == win.NM_CUSTOMDRAW {
			d := *(**win.NMCUSTOMDRAW)(unsafe.Pointer(&lp))
			if d.DwDrawStage == win.CDDS_PREPAINT && paintPrimaryButton(b, d) {
				return win.CDRF_SKIPDEFAULT
			}
		}
	}
	r := win.CallWindowProc(p.previous, h, msg, wp, lp)
	if msg == win.WM_NCDESTROY {
		delete(primaryParents, h)
	}
	return r
})

func stylePrimaryButton(b *walk.PushButton) {
	h := win.GetParent(b.Handle())
	p := primaryParents[h]
	if p == nil {
		p = &primaryButtonParent{buttons: map[win.HWND]*walk.PushButton{}}
		primaryParents[h] = p
		p.previous = win.SetWindowLongPtr(h, win.GWLP_WNDPROC, primaryButtonProc)
	}
	p.buttons[b.Handle()] = b
	b.Disposing().Attach(func() { delete(p.buttons, b.Handle()) })
	b.Invalidate()
}

func paintPrimaryButton(b *walk.PushButton, d *win.NMCUSTOMDRAW) bool {
	color := walk.RGB(22, 106, 168)
	if !b.Enabled() {
		color = walk.RGB(133, 148, 163)
	} else if d.UItemState&win.CDIS_SELECTED != 0 {
		color = walk.RGB(15, 76, 125)
	} else if d.UItemState&win.CDIS_HOT != 0 {
		color = walk.RGB(27, 122, 189)
	}
	saved := win.SaveDC(d.Hdc)
	if saved == 0 {
		return false
	}
	brush := win.CreateBrushIndirect(&win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.COLORREF(color)})
	if brush == 0 {
		win.RestoreDC(d.Hdc, saved)
		return false
	}
	defer func() {
		win.RestoreDC(d.Hdc, saved)
		win.DeleteObject(win.HGDIOBJ(brush))
	}()
	win.SelectObject(d.Hdc, win.HGDIOBJ(brush))
	win.SelectObject(d.Hdc, win.GetStockObject(win.NULL_PEN))
	r := d.Rc
	win.Rectangle_(d.Hdc, r.Left, r.Top, r.Right, r.Bottom)
	win.SelectObject(d.Hdc, win.HGDIOBJ(b.SendMessage(win.WM_GETFONT, 0, 0)))
	win.SetBkMode(d.Hdc, win.TRANSPARENT)
	win.SetTextColor(d.Hdc, win.COLORREF(walk.RGB(255, 255, 255)))
	win.DrawTextEx(d.Hdc, syscall.StringToUTF16Ptr(b.Text()), -1, &r, win.DT_CENTER|win.DT_VCENTER|win.DT_SINGLELINE|win.DT_NOPREFIX, nil)
	if d.UItemState&win.CDIS_FOCUS != 0 {
		inset := int32(walk.IntFrom96DPI(4, b.DPI()))
		r.Left += inset
		r.Top += inset
		r.Right -= inset
		r.Bottom -= inset
		win.DrawFocusRect(d.Hdc, &r)
	}
	return true
}
