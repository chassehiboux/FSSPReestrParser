package ui

import (
	"github.com/chassehiboux/FSSPReestrParser/internal/queue"
	"github.com/chassehiboux/FSSPReestrParser/internal/registry"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"unsafe"
)

type operation struct {
	panel     *walk.Composite
	status    *walk.Label
	progress  *walk.ProgressBar
	stop      *walk.PushButton
	table     *walk.TableView
	model     *statusModel
	running   bool
	withTable bool
	cancel    func()
	retry     *walk.PushButton
	retryFunc func()
}

func appIcon() any {
	icon, err := walk.NewIconFromResourceId(2)
	if err != nil {
		return nil
	}
	return icon
}
func newOperation(a *App, title string, withTable bool, openFolder func(), stages ...bool) (*operation, error) {
	op := &operation{model: &statusModel{events: map[string]queue.Event{}}, withTable: withTable}
	stageTable := len(stages) > 0 && stages[0]
	if stageTable {
		op.model.filterStages(registry.Options{SkipAnalytics: a.cfg.SkipAnalytics, SkipCaseRegisters: a.cfg.SkipCaseRegisters}, 0)
		for i := range registry.StageNames {
			op.model.stages = append(op.model.stages, registry.Event{Stage: i, State: "Ожидание"})
		}
	}
	children := []Widget{Label{Text: title, Font: Font{Family: "Segoe UI", PointSize: 14, Bold: true}}, Label{AssignTo: &op.status, Text: "Подготовка…"}, ProgressBar{AssignTo: &op.progress, MaxValue: 100}}
	if withTable {
		tableHeight := 480
		columns := []TableViewColumn{{Title: "№", Width: 40}, {Title: "Департамент", Width: 160}, {Title: "Условия", Width: 360}, {Title: "Состояние", Width: 130}, {Title: "Подробности", Width: 300}}
		if stageTable {
			columns = []TableViewColumn{{Title: "№", Width: 35}, {Title: "Этап", Width: 400}, {Title: "Состояние", Width: 120}, {Title: "Подробности", Width: 800}}
		}
		children = append(children, TableView{AssignTo: &op.table, Model: op.model, StretchFactor: 1, Font: Font{Family: "Segoe UI", PointSize: 9}, MinSize: Size{Width: 800, Height: tableHeight}, AlternatingRowBG: true, LastColumnStretched: true, ColumnsSizable: true, StyleCell: func(style *walk.CellStyle) {
			if stageTable {
				if style.Col() == 2 && style.Row() >= 0 && style.Row() < op.model.RowCount() {
					switch op.model.stageState(style.Row()) {
					case "Готово", "Сохранён":
						style.BackgroundColor = walk.RGB(208, 239, 218)
						style.TextColor = walk.RGB(24, 89, 45)
					case "Ошибка":
						style.BackgroundColor = walk.RGB(252, 218, 218)
						style.TextColor = walk.RGB(151, 28, 28)
					case "Выполняется", queue.Generating:
						style.BackgroundColor = walk.RGB(255, 240, 187)
						style.TextColor = walk.RGB(114, 79, 0)
					case "Подготовлен", queue.Downloading:
						style.BackgroundColor = walk.RGB(207, 232, 255)
						style.TextColor = walk.RGB(20, 76, 130)
					case queue.Preparing:
						style.BackgroundColor = walk.RGB(228, 220, 249)
						style.TextColor = walk.RGB(83, 51, 126)
					default:
						style.BackgroundColor = walk.RGB(238, 238, 238)
						style.TextColor = walk.RGB(100, 100, 100)
					}
				}
				return
			}
			if style.Col() != 3 || style.Row() < 0 || style.Row() >= len(op.model.jobs) {
				return
			}
			state := op.model.events[op.model.jobs[style.Row()].ID].State
			switch state {
			case queue.Saved:
				style.BackgroundColor = walk.RGB(208, 239, 218)
				style.TextColor = walk.RGB(24, 89, 45)
			case queue.Failed:
				style.BackgroundColor = walk.RGB(252, 218, 218)
				style.TextColor = walk.RGB(151, 28, 28)
			case queue.Generating:
				style.BackgroundColor = walk.RGB(255, 240, 187)
				style.TextColor = walk.RGB(114, 79, 0)
			case queue.Downloading:
				style.BackgroundColor = walk.RGB(207, 232, 255)
				style.TextColor = walk.RGB(20, 76, 130)
			case queue.Preparing:
				style.BackgroundColor = walk.RGB(228, 220, 249)
				style.TextColor = walk.RGB(83, 51, 126)
			case queue.Empty:
				style.BackgroundColor = walk.RGB(232, 237, 241)
				style.TextColor = walk.RGB(76, 88, 99)
			case queue.Canceled:
				style.BackgroundColor = walk.RGB(238, 238, 238)
				style.TextColor = walk.RGB(100, 100, 100)
			default:
				style.BackgroundColor = walk.RGB(246, 247, 249)
				style.TextColor = walk.RGB(75, 82, 90)
			}
		}, Columns: columns})
	}
	buttons := []Widget{PushButton{AssignTo: &op.retry, Text: "Продолжить", Visible: false, OnClicked: func() {
		if op.retryFunc != nil {
			op.retryFunc()
		}
	}}, PushButton{AssignTo: &op.stop, Text: "Остановить", Enabled: false, Visible: withTable, OnClicked: func() {
		if op.cancel != nil {
			op.cancel()
			op.status.SetText("Остановка…")
		}
	}}}
	if openFolder != nil {
		buttons = append(buttons, PushButton{Text: "Открыть папку", OnClicked: openFolder})
	}
	buttons = append(buttons, HSpacer{}, PushButton{Text: "Свернуть", OnClicked: func() { a.hideOperation(op) }})
	children = append(children, Composite{Layout: HBox{}, Children: buttons})
	err := (Composite{AssignTo: &op.panel, Visible: false, Layout: VBox{Spacing: 8}, Children: children}).Create(NewBuilder(a.host))
	if err != nil {
		return nil, err
	}
	a.operations = append(a.operations, op)
	a.showOperation(op)
	return op, nil
}
func (a *App) showOperation(op *operation) {
	for _, other := range a.operations {
		other.panel.SetVisible(other == op)
	}
	a.host.SetVisible(true)
	a.statusPlaceholder.SetVisible(false)
}

func (a *App) sizeStatusWindow() {
	var mi win.MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if win.GetMonitorInfo(win.MonitorFromWindow(a.window.Handle(), win.MONITOR_DEFAULTTONEAREST), &mi) {
		area := mi.RcWork
		main := a.window.BoundsPixels()
		width := int(area.Right - area.Left)
		height := min(a.window.IntFrom96DPI(780), int(area.Bottom-area.Top))
		a.window.SetBoundsPixels(walk.Rectangle{X: int(area.Left), Y: max(int(area.Top), min(main.Y, int(area.Bottom)-height)), Width: width, Height: height})
	}
}
func (a *App) hideOperation(op *operation) {
	op.panel.SetVisible(false)
	a.statusPlaceholder.SetVisible(true)
}
