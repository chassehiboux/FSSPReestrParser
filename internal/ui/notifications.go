package ui

import (
	"fmt"
	"github.com/lxn/walk"
	"github.com/lxn/win"
)

func (a *App) setupNotifications() {
	icon, err := walk.NewNotifyIcon(a.window)
	if err != nil {
		return
	}
	if image, ok := appIcon().(*walk.Icon); ok {
		icon.SetIcon(image)
	}
	icon.SetToolTip("Реестры ФССП")
	icon.SetVisible(true)
	a.notifications = icon
	show := func() {
		win.ShowWindow(a.window.Handle(), win.SW_RESTORE)
		win.SetForegroundWindow(a.window.Handle())
		if a.pendingUpdate != nil && !a.running && !a.updating {
			a.runPendingUpdate()
		}
	}
	icon.MessageClicked().Attach(show)
	icon.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			show()
		}
	})
	open := walk.NewAction()
	open.SetText("Открыть")
	open.Triggered().Attach(show)
	icon.ContextMenu().Actions().Add(open)
	exit := walk.NewAction()
	exit.SetText("Выход")
	exit.Triggered().Attach(func() { a.window.Close() })
	icon.ContextMenu().Actions().Add(exit)
}
func (a *App) notify(title, message string) {
	if a.notifications != nil {
		a.notifications.ShowInfo(title, message)
	}
}
func (a *App) notifyCompletion(saved, total, failed, empty, canceled int) {
	title := "Выгрузка завершена"
	if canceled > 0 {
		title = "Выгрузка остановлена"
	}
	message := fmt.Sprintf("Сохранено %d из %d. Ошибок %d.", saved, total, failed)
	if empty > 0 {
		message += fmt.Sprintf(" Нет данных: %d.", empty)
	}
	a.notify(title, message)
}
