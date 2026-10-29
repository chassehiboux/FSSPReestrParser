package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/go-rod/rod/lib/proto"
)

// Diagnostic содержит метаданные и ошибки, без cookies, токенов и строк должников.
type Diagnostic struct {
	Department string          `json:"department"`
	Events     []string        `json:"events"`
	Snapshot   json.RawMessage `json:"snapshot"`
	Error      string          `json:"error,omitempty"`
}

func safeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid URL"
	}
	return u.Scheme + "://" + u.Host + u.Path
}
func (e *Engine) Diagnose(ctx context.Context, p model.Profile) []Diagnostic {
	result := []Diagnostic{}
	for _, d := range p.Departments {
		fmt.Println("Диагностика:", d.Name, "подготовка контекста")
		item := Diagnostic{Department: d.Name}
		var base model.Base
		for _, b := range p.Bases {
			if b.ID == d.BaseID {
				base = b
			}
		}
		s, err := e.getSession(ctx, model.Job{Base: base, Department: d})
		if err != nil {
			item.Error = err.Error()
			result = append(result, item)
			continue
		}
		page, err := s.browser.Context(ctx).Page(proto.TargetCreateTarget{})
		if err != nil {
			item.Error = err.Error()
			result = append(result, item)
			continue
		}
		eventCtx, cancel := context.WithCancel(ctx)
		var mu sync.Mutex
		events := []string{}
		requests := map[proto.NetworkRequestID]string{}
		add := func(text string) { mu.Lock(); events = append(events, text); mu.Unlock() }
		wait := page.Context(eventCtx).EachEvent(func(ev *proto.NetworkRequestWillBeSent) { requests[ev.RequestID] = safeURL(ev.Request.URL) }, func(ev *proto.NetworkLoadingFailed) {
			add(requests[ev.RequestID] + " FAILED " + ev.ErrorText + " " + string(ev.BlockedReason))
		}, func(ev *proto.NetworkResponseReceived) {
			if ev.Response.Status >= 400 {
				add(fmt.Sprintf("%s HTTP %d", safeURL(ev.Response.URL), ev.Response.Status))
			}
		}, func(ev *proto.PageJavascriptDialogOpening) {
			add("DIALOG " + ev.Message)
			(proto.PageHandleJavaScriptDialog{Accept: false}).Call(page)
		})
		go wait()
		fmt.Println("Диагностика:", d.Name, "открытие формы")
		err = page.Navigate(base.URL + "/ovzid/status/all")
		if err == nil {
			err = waitDOM(page)
		}
		if err == nil {
			err = pause(ctx, 15*time.Second)
		}
		if err == nil {
			item.Snapshot, err = eval(page.Timeout(5*time.Second), `() => ({url:location.origin+location.pathname,department:document.querySelector('button.btn-cities')?.textContent.trim(),active:Array.from(document.querySelectorAll('a[data-depid].active')).map(e=>e.getAttribute('data-depid')),ready:!!document.getElementById('list')?.p,loading:document.getElementById('list')?.grid?.hDiv?.loading,records:document.getElementById('list')?.p?.records,alerts:Array.from(document.querySelectorAll('.alert-danger,.alert-error,.validation-summary-errors')).map(e=>e.textContent.trim()),ua:navigator.userAgent,secure:isSecureContext})`)
		}
		if err != nil {
			item.Error = err.Error()
		}
		cancel()
		page.Context(context.Background()).Close()
		mu.Lock()
		item.Events = append([]string(nil), events...)
		mu.Unlock()
		result = append(result, item)
		fmt.Println("Диагностика:", d.Name, "проверено, сетевых ошибок:", len(item.Events))
	}
	return result
}
