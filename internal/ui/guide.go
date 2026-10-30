package ui

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/chassehiboux/FSSPReestrParser/internal/settings"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// Страницы утверждённого пользователем документа, включая его текст и стрелки.
//
//go:embed guide/*.png
var guidePages embed.FS

const guidePageCount = 7

type guideCompletion struct {
	Completed bool `json:"completed"`
}

func guideCompleted(local string) bool {
	raw, err := os.ReadFile(filepath.Join(local, "instruction.json"))
	var state guideCompletion
	return err == nil && json.Unmarshal(raw, &state) == nil && state.Completed
}

func (a *App) showGuide() {
	if a.guideOpen {
		return
	}
	a.guideOpen = true
	defer func() { a.guideOpen = false }()
	var dlg *walk.Dialog
	var view *walk.CustomWidget
	var back, next *walk.PushButton
	var counter *walk.Label
	var bitmap *walk.Bitmap
	defer func() {
		if bitmap != nil {
			bitmap.Dispose()
		}
	}()
	page := 0
	load := func() error {
		raw, err := guidePages.ReadFile(fmt.Sprintf("guide/page-%d.png", page+1))
		if err != nil {
			return err
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		fresh, err := walk.NewBitmapFromImageForDPI(img, 96)
		if err != nil {
			return err
		}
		if bitmap != nil {
			bitmap.Dispose()
		}
		bitmap = fresh
		counter.SetText(fmt.Sprintf("Страница %d из %d", page+1, guidePageCount))
		back.SetEnabled(page > 0)
		text := "Далее →"
		if page == guidePageCount-1 {
			text = "Готово"
		}
		next.SetText(text)
		return view.Invalidate()
	}
	change := func(delta int) {
		page += delta
		if err := load(); err != nil {
			a.error(err)
			dlg.Cancel()
		}
	}
	err := (Dialog{AssignTo: &dlg, Title: "Инструкция · Реестры ФССП", DefaultButton: &next, Font: Font{Family: "Segoe UI", PointSize: 10}, Size: Size{Width: 1200, Height: 870}, MinSize: Size{Width: 640, Height: 480}, Layout: VBox{MarginsZero: true}, Children: []Widget{
		CustomWidget{AssignTo: &view, StretchFactor: 1, InvalidatesOnResize: true, PaintPixels: func(canvas *walk.Canvas, _ walk.Rectangle) error {
			bounds := view.ClientBoundsPixels()
			brush, err := walk.NewSolidColorBrush(walk.RGB(255, 255, 255))
			if err != nil {
				return err
			}
			defer brush.Dispose()
			if err = canvas.FillRectanglePixels(brush, bounds); err != nil || bitmap == nil {
				return err
			}
			size := bitmap.Size()
			width := bounds.Width
			height := width * size.Height / size.Width
			if height > bounds.Height {
				height = bounds.Height
				width = height * size.Width / size.Height
			}
			if width <= 0 || height <= 0 {
				return nil
			}
			return canvas.DrawImageStretchedPixels(bitmap, walk.Rectangle{X: (bounds.Width - width) / 2, Y: (bounds.Height - height) / 2, Width: width, Height: height})
		}},
		Composite{Layout: HBox{Margins: Margins{Left: 16, Top: 8, Right: 16, Bottom: 12}, Spacing: 12}, Children: []Widget{
			PushButton{AssignTo: &back, Text: "← Назад", OnClicked: func() { change(-1) }}, HSpacer{},
			Label{AssignTo: &counter}, HSpacer{},
			PushButton{AssignTo: &next, Text: "Далее →", OnClicked: func() {
				if page < guidePageCount-1 {
					change(1)
					return
				}
				if err := settings.AtomicJSON(filepath.Join(a.local, "instruction.json"), guideCompletion{Completed: true}); err != nil {
					walk.MsgBox(dlg, "Инструкция", "Не удалось сохранить отметку о просмотре: "+err.Error(), walk.MsgBoxIconWarning)
					return
				}
				dlg.Accept()
			}},
		}},
	}}).Create(a.window)
	if err != nil {
		a.error(err)
		return
	}
	defer dlg.Dispose()
	if err = load(); err != nil {
		a.error(err)
		return
	}
	// Почти вся рабочая область текущего монитора: текст страниц остаётся читаемым.
	info := win.MONITORINFO{CbSize: uint32(unsafe.Sizeof(win.MONITORINFO{}))}
	dlg.Starting().Attach(func() {
		if win.GetMonitorInfo(win.MonitorFromWindow(a.window.Handle(), win.MONITOR_DEFAULTTONEAREST), &info) {
			r := info.RcWork
			dlg.SetBoundsPixels(walk.Rectangle{X: int(r.Left) + 16, Y: int(r.Top) + 16, Width: int(r.Right-r.Left) - 32, Height: int(r.Bottom-r.Top) - 32})
		}
	})
	previous := a.owner
	a.owner = dlg
	defer func() { a.owner = previous }()
	dlg.Run()
}
