package ui

import (
	"context"
	"fmt"
	"github.com/chassehiboux/FSSPReestrParser/internal/update"
	"github.com/lxn/walk"
	"os"
	"time"
)

func (a *App) startupUpdates() bool {
	ctx, cancel := context.WithTimeout(a.lifecycle, 25*time.Second)
	r, err := update.Check(ctx, a.local)
	cancel()
	if err != nil {
		a.window.Synchronize(func() {
			walk.MsgBox(a.window, "Обновления недоступны", "Продолжаем работу на текущей версии.\n"+err.Error(), walk.MsgBoxIconWarning)
		})
		return true
	}
	if r != nil {
		return a.installUpdate(*r)
	}
	return true
}

// installUpdate работает вне потока интерфейса и не запускается во время выгрузки.
func (a *App) installUpdate(release update.Release) bool {
	prepared := make(chan struct{}, 1)
	a.window.Synchronize(func() {
		a.updating = true
		a.pendingUpdate = nil
		a.updateButton.SetVisible(false)
		a.enable()
		walk.MsgBox(a.window, "Доступно обновление", "Доступна версия "+release.Version+". Нажмите ОК для обновления.", walk.MsgBoxIconInformation)
		a.updateStatus.SetText("Скачивание обновления " + release.Version + "…")
		a.updateProgress.SetValue(0)
		a.updatePanel.SetVisible(true)
		prepared <- struct{}{}
	})
	select {
	case <-a.lifecycle.Done():
		return false
	case <-prepared:
	}
	ctx, cancel := context.WithTimeout(a.lifecycle, 5*time.Minute)
	candidate, err := update.Download(ctx, release, a.local, func(done, total int64) {
		a.window.Synchronize(func() {
			if total > 0 {
				a.updateProgress.SetValue(int(done * 100 / total))
				a.updateStatus.SetText(fmt.Sprintf("Обновление: %d%% · %.1f / %.1f МБ", done*100/total, float64(done)/1048576, float64(total)/1048576))
			} else {
				a.updateStatus.SetText(fmt.Sprintf("Обновление: %.1f МБ", float64(done)/1048576))
			}
		})
	})
	cancel()
	if err == nil {
		a.window.Synchronize(func() {
			a.updateProgress.SetValue(100)
			a.updateStatus.SetText("Установка обновления…")
		})
		var target string
		target, err = os.Executable()
		if err == nil {
			err = update.LaunchHelper(candidate, target)
		}
		if err == nil {
			a.window.Synchronize(func() { a.window.Close() })
			return false
		}
	}
	a.window.Synchronize(func() {
		a.updating = false
		a.updatePanel.SetVisible(false)
		a.enable()
		walk.MsgBox(a.window, "Обновление не установлено", "Продолжаем работу на текущей версии.\n"+err.Error(), walk.MsgBoxIconWarning)
	})
	return true
}

func (a *App) backgroundUpdates() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.lifecycle.Done():
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(a.lifecycle, 25*time.Second)
		r, err := update.Check(ctx, a.local)
		cancel()
		if err != nil || r == nil {
			continue
		}
		a.window.Synchronize(func() {
			if a.updating || a.pendingUpdate != nil && a.pendingUpdate.Version == r.Version {
				return
			}
			a.pendingUpdate = r
			a.updateButton.SetText("Обновить до " + r.Version)
			a.updateButton.SetVisible(true)
			a.status.SetText("Доступно обновление " + r.Version)
			a.enable()
			a.notify("Доступно обновление", "Версия "+r.Version+". Нажмите уведомление или кнопку «Обновить» в приложении. Текущая выгрузка продолжится.")
		})
	}
}
func (a *App) runPendingUpdate() {
	if a.running || a.updating || a.pendingUpdate == nil {
		return
	}
	release := *a.pendingUpdate
	a.updating = true
	a.enable()
	go a.installUpdate(release)
}
