package ui

import (
	"github.com/chassehiboux/FSSPReestrParser/internal/queue"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
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
}

func appIcon() any {
	icon, err := walk.NewIconFromResourceId(2)
	if err != nil {
		return nil
	}
	return icon
}
func newOperation(a *App, title string, withTable bool, openFolder func()) (*operation, error) {
	op := &operation{model: &statusModel{events: map[string]queue.Event{}}, withTable: withTable}
	children := []Widget{Label{Text: title, Font: Font{Family: "Segoe UI", PointSize: 14, Bold: true}}, Label{AssignTo: &op.status, Text: "Подготовка…"}, ProgressBar{AssignTo: &op.progress, MaxValue: 100}}
	if withTable {
		children = append(children, TableView{AssignTo: &op.table, Model: op.model, MinSize: Size{Width: 900, Height: 300}, AlternatingRowBG: true, LastColumnStretched: true, ColumnsSizable: true, StyleCell: func(style *walk.CellStyle) {
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
		}, Columns: []TableViewColumn{{Title: "№", Width: 40}, {Title: "Департамент", Width: 160}, {Title: "Условия", Width: 360}, {Title: "Состояние", Width: 130}, {Title: "Подробности", Width: 300}}})
	}
	buttons := []Widget{PushButton{AssignTo: &op.stop, Text: "Остановить", Enabled: false, Visible: withTable, OnClicked: func() {
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
	err := (Composite{AssignTo: &op.panel, Visible: false, Layout: VBox{Spacing: 10}, Children: children}).Create(NewBuilder(a.host))
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
	height := 560
	width := 760
	if op.withTable {
		height = 820
		width = 1180
	}
	a.window.SetSize(walk.Size{Width: width, Height: height})
}
func (a *App) hideOperation(op *operation) {
	op.panel.SetVisible(false)
	a.host.SetVisible(false)
	a.window.SetSize(walk.Size{Width: 720, Height: 360})
}
