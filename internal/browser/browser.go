// Package browser готовит форму и отправляет выгрузку; ожидание и сохранение принадлежат очереди.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/platform"
	"github.com/chassehiboux/FSSPReestrParser/internal/queue"
	"github.com/chassehiboux/FSSPReestrParser/internal/settings"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

type Credentials struct {
	Username string
	Password string
	Remember bool
}
type Prompt func(context.Context, string, string) (Credentials, error)
type session struct {
	browser *rod.Browser
	temp    string
	mu      sync.Mutex
}
type Engine struct {
	browser             *rod.Browser
	launch              *launcher.Launcher
	local, output, temp string
	prompt              Prompt
	mu                  sync.Mutex
	authMu              sync.Mutex
	catalogMu           sync.Mutex
	sessions            map[string]*session
	credentials         Credentials
	catalog             model.Catalog
	seed                []*proto.NetworkCookieParam
	sharedCatalog       string
	catalogPending      map[string]settings.CatalogDocument
	catalogWake         chan struct{}
	catalogStop         chan struct{}
	catalogWriting      bool
	catalogLastError    error
}

func New(ctx context.Context, local, output string, headless bool, prompt Prompt) (*Engine, error) {
	var pathErr error
	local, pathErr = filepath.Abs(local)
	if pathErr != nil {
		return nil, pathErr
	}
	if output != "" {
		output, pathErr = filepath.Abs(output)
		if pathErr != nil {
			return nil, pathErr
		}
	}
	bin, e := platform.BrowserPath()
	if e != nil {
		return nil, e
	}
	temp, e := os.MkdirTemp(local, "run-*")
	if e != nil {
		return nil, e
	}
	l := launcher.New().Context(ctx).Bin(bin).Headless(headless).UserDataDir(filepath.Join(temp, "profile")).Leakless(false)
	control, e := l.Launch()
	if e != nil {
		os.RemoveAll(temp)
		return nil, fmt.Errorf("Не удалось запустить браузер: %w", e)
	}
	b := rod.New().NoDefaultDevice().Context(ctx).ControlURL(control)
	if e = b.Connect(); e != nil {
		l.Kill()
		os.RemoveAll(temp)
		return nil, e
	}
	engine := &Engine{browser: b, launch: l, local: local, output: output, temp: temp, prompt: prompt, sessions: map[string]*session{}, catalog: settings.LoadCatalog(filepath.Join(local, "catalog.json"))}
	platform.ReadProtected(filepath.Join(local, "credentials.dat"), &engine.credentials)
	return engine, nil
}
func (e *Engine) Close() {
	if e.catalogStop != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e.FlushCatalog(ctx)
		cancel()
		close(e.catalogStop)
	}
	e.browser.Context(context.Background()).Timeout(5 * time.Second).Close()
	e.launch.Kill()
	os.RemoveAll(e.temp)
}
func (e *Engine) Catalog() model.Catalog {
	e.catalogMu.Lock()
	defer e.catalogMu.Unlock()
	raw, _ := json.Marshal(e.catalog)
	var c model.Catalog
	json.Unmarshal(raw, &c)
	return c
}
func (e *Engine) ImportState(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var state struct {
		Cookies []map[string]any `json:"cookies"`
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	for _, cookie := range state.Cookies {
		domain, _ := cookie["domain"].(string)
		if !ssoDomain(domain) {
			continue
		}
		delete(cookie, "partitionKey")
		b, _ := json.Marshal(cookie)
		var c proto.NetworkCookieParam
		if err = json.Unmarshal(b, &c); err != nil {
			return err
		}
		e.seed = append(e.seed, &c)
	}
	return nil
}

func ssoDomain(domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(domain), ".")
	return domain == "pyrsso.vostok-electra.ru" || domain == "identity.vostok-electra.ru"
}
func LoginURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := strings.ToLower(strings.TrimSuffix(u.Path, "/"))
	return p == "/login" || p == "/login/pyrsso" || (ssoDomain(u.Hostname()) && strings.Contains(p, "login"))
}

func eval(p *rod.Page, js string, args ...any) (json.RawMessage, error) {
	v, err := p.Eval(js, args...)
	if err != nil {
		return nil, err
	}
	return v.Value.MarshalJSON()
}
func read(p *rod.Page, js string, target any, args ...any) error {
	b, err := eval(p, js, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// PyramidReports ожидает DOMContentLoaded, не полную загрузку окна.
// Внешние ресурсы не должны блокировать вход и подготовку отчёта.
func waitDOM(p *rod.Page) error {
	return p.Timeout(30 * time.Second).Wait(rod.Eval(`() => document.readyState === 'interactive' || document.readyState === 'complete'`))
}

func waitGrid(p *rod.Page, filtered bool) error {
	return p.Timeout(2 * time.Minute).Wait(rod.Eval(`filtered => {
 const errors=Array.from(document.querySelectorAll('.alert-danger,.alert-error,.validation-summary-errors')).filter(e=>e.getBoundingClientRect().width&&e.getBoundingClientRect().height).map(e=>e.textContent.replace(/×/g,'').trim()).filter(Boolean);
 if(errors.length)throw new Error('Пирамида: '+errors.join(' '));
 const grid=document.getElementById('list');
 if(filtered&&window.__fsspFilter?.done&&window.__fsspFilter.status!==200)throw new Error('Запрос таблицы не выполнен (HTTP '+window.__fsspFilter.status+'). Проверьте подключение и вход в Пирамиду');
 return !!grid?.p&&!grid.grid.hDiv.loading&&(!filtered||!!window.__fsspFilter?.done);
}`, filtered))
}

// AuthSnapshot не содержит паролей и строк данных со страницы.
type AuthSnapshot struct {
	URL   string `json:"url"`
	Form  bool   `json:"form"`
	Ready bool   `json:"ready"`
	Entry bool   `json:"entry"`
	Error string `json:"error"`
	Stamp string `json:"stamp"`
}

const authJS = `() => ({url:location.origin+location.pathname, form:!!document.querySelector('#Input_UserName,#Input_Password'), ready:!!document.querySelector('button.btn-cities'), entry:!!document.querySelector('a[href*="/login/pyrsso/redirect"]')||location.pathname==='/login/pyrsso', error:Array.from(document.querySelectorAll('.validation-summary-errors,.field-validation-error,[data-valmsg-summary="true"],#Input_Password-error,#Input_UserName-error')).map(e=>e.textContent.trim()).filter(Boolean).join(' '),stamp:String(performance.timeOrigin)})`

func CredentialRejected(text string) bool {
	t := strings.ToLower(text)
	for _, s := range []string{"неверн", "invalid credentials", "invalid login", "неправильн", "заблокирован", "locked out", "incorrect"} {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

func (e *Engine) ensureLogin(ctx context.Context, p *rod.Page, base model.Base) error {
	var submitted bool
	var submittedAt time.Time
	var stamp string
	var remembered bool
	var lastRead error
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var state AuthSnapshot
		err := read(p.Timeout(5*time.Second), authJS, &state)
		if err != nil {
			lastRead = err
			if pause(ctx, 300*time.Millisecond) != nil {
				return ctx.Err()
			}
			continue
		}
		if state.Ready {
			if submitted && remembered {
				if err := platform.WriteProtected(filepath.Join(e.local, "credentials.dat"), e.credentials); err != nil {
					return fmt.Errorf("Вход выполнен, но пароль не удалось сохранить: %w", err)
				}
			}
			return nil
		}
		if state.Form {
			if submitted {
				if state.Stamp != stamp && state.Error != "" {
					if CredentialRejected(state.Error) {
						e.credentials.Password = ""
						os.Remove(filepath.Join(e.local, "credentials.dat"))
					}
					submitted = false
					if !CredentialRejected(state.Error) {
						return fmt.Errorf("Сервис авторизации отклонил вход: %s", state.Error)
					}
				} else if time.Since(submittedAt) > 40*time.Second {
					return fmt.Errorf("Сервис авторизации не подтвердил вход. Проверьте сеть и повторите попытку")
				} else {
					pause(ctx, 250*time.Millisecond)
					continue
				}
			}
			reason := "Введите логин и пароль Пирамиды"
			if state.Error != "" {
				reason = "Вход не выполнен: " + state.Error + ". Введите логин и пароль заново"
			}
			if e.credentials.Username == "" || e.credentials.Password == "" {
				if e.prompt == nil {
					return errors.New(reason)
				}
				creds, err := e.prompt(ctx, reason, e.credentials.Username)
				if err != nil {
					return err
				}
				if creds.Username == "" || creds.Password == "" {
					return errors.New("Ввод учётных данных отменён")
				}
				e.credentials = creds
				remembered = creds.Remember
			}
			_, err := p.Eval(`(u,pw) => {const uEl=document.querySelector('#Input_UserName'),pEl=document.querySelector('#Input_Password');if(!uEl||!pEl)throw new Error('Форма входа изменилась');uEl.value=u;pEl.value=pw;uEl.dispatchEvent(new Event('input',{bubbles:true}));pEl.dispatchEvent(new Event('input',{bubbles:true}));const submit=document.querySelector('#login-submit,button[type=submit]');if(!submit)throw new Error('Кнопка входа отсутствует');setTimeout(()=>submit.click(),0);}`, e.credentials.Username, e.credentials.Password)
			if err != nil {
				return err
			}
			submitted = true
			submittedAt = time.Now()
			stamp = state.Stamp
			continue
		}
		if state.Entry {
			_, err := p.Eval(`() => {const b=Array.from(document.querySelectorAll('button,a')).find(e=>/^войти$/i.test(e.textContent.trim()))||document.querySelector('a[href*="/login/pyrsso/redirect"]');if(b)setTimeout(()=>b.click(),0)}`)
			if err != nil {
				lastRead = err
			}
			pause(ctx, 500*time.Millisecond)
			continue
		}
		pause(ctx, 250*time.Millisecond)
	}
	if lastRead != nil {
		return fmt.Errorf("Не удалось прочитать ответ сервиса авторизации: %w", lastRead)
	}
	return errors.New("Не удалось завершить вход. Проверьте подключение к корпоративной сети")
}

func (e *Engine) getSession(ctx context.Context, j model.Job) (*session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := model.Scope(j.Department)
	if s := e.sessions[key]; s != nil {
		return s, nil
	}
	b, err := e.browser.Incognito()
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			b.Close()
		}
	}()
	var cookies []*proto.NetworkCookieParam
	platform.ReadProtected(filepath.Join(e.local, "session-"+model.SafeName(key)+".dat"), &cookies)
	if len(cookies) == 0 {
		cookies = e.seed
	}
	if len(cookies) > 0 {
		if err = b.SetCookies(cookies); err != nil {
			return nil, err
		}
	}
	p, err := b.Context(ctx).Page(proto.TargetCreateTarget{URL: strings.TrimRight(j.Base.URL, "/") + "/ovzid/status/all"})
	if err != nil {
		return nil, err
	}
	defer p.Close()
	if err = waitDOM(p); err != nil {
		return nil, err
	}
	e.authMu.Lock()
	err = e.ensureLogin(ctx, p, j.Base)
	e.authMu.Unlock()
	if err != nil {
		return nil, err
	}
	// Выбранный департамент хранится в сессии сервера; контексты и прикладные cookies изолированы.
	var href string
	err = read(p.Timeout(20*time.Second), `id => {const a=document.querySelector('a[data-depid="'+id+'"]');return a?.href||''}`, &href, j.Department.ID)
	if err != nil {
		return nil, err
	}
	if href == "" {
		return nil, fmt.Errorf("Нет доступа к департаменту %s", j.Department.Name)
	}
	if err = p.Navigate(href); err != nil {
		return nil, err
	}
	if err = waitDOM(p); err != nil {
		return nil, err
	}
	if err = p.Navigate(strings.TrimRight(j.Base.URL, "/") + "/ovzid/status/all"); err != nil {
		return nil, err
	}
	if err = waitDOM(p); err != nil {
		return nil, err
	}
	var selected string
	err = read(p, `() => document.querySelector('ul.dropdown-menu-sub a[data-depid].active,ul.dropdown-menu-sub li.active a[data-depid]')?.getAttribute('data-depid')||document.querySelector('button.btn-cities')?.getAttribute('data-depid')||''`, &selected)
	if err != nil {
		return nil, err
	}
	if selected != fmt.Sprint(j.Department.ID) {
		return nil, fmt.Errorf("Не подтверждён департамент %s (получен %s)", j.Department.Name, selected)
	}
	if err = waitGrid(p, false); err != nil {
		return nil, err
	}
	if fields, err := collect(p); err == nil {
		e.rememberCatalog(j.Base.ID, j.Department.ID, fields)
	} else {
		return nil, err
	}
	all, err := b.GetCookies()
	if err == nil {
		params := make([]*proto.NetworkCookieParam, 0, len(all))
		for _, c := range all {
			params = append(params, &proto.NetworkCookieParam{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Secure: c.Secure, HTTPOnly: c.HTTPOnly, SameSite: c.SameSite, Expires: c.Expires})
		}
		platform.WriteProtected(filepath.Join(e.local, "session-"+model.SafeName(key)+".dat"), params)
		e.seed = nil
		for _, c := range params {
			if ssoDomain(c.Domain) {
				e.seed = append(e.seed, c)
			}
		}
	}
	temp := filepath.Join(e.temp, model.SafeName(key))
	if err = os.MkdirAll(temp, 0700); err != nil {
		return nil, err
	}
	if err = (proto.BrowserSetDownloadBehavior{Behavior: proto.BrowserSetDownloadBehaviorBehaviorAllowAndName, BrowserContextID: b.BrowserContextID, DownloadPath: temp, EventsEnabled: true}).Call(b); err != nil {
		return nil, err
	}
	s := &session{browser: b, temp: temp}
	e.sessions[key] = s
	ok = true
	return s, nil
}

const fieldsJS = `() => {const t=document.getElementById('list');if(!t?.p?.colModel)throw new Error('Таблица ОВЗИД не загружена');const clean=v=>String(v||'').replace(/\s+/g,' ').trim();return t.p.colModel.filter(c=>c.name&&!c.hidden&&!['rn','cb'].includes(c.name)).map(c=>{const input=Array.from(document.querySelectorAll('.ui-search-toolbar input,.ui-search-toolbar select')).find(e=>e.id==='gs_list_'+c.name);return {key:c.name,filter_field:c.index||c.name,label:clean(document.getElementById('list_'+c.name)?.textContent||(t.p.colNames||[])[t.p.colModel.indexOf(c)]||c.name),type:input?.tagName==='SELECT'?'select':'text',searchable:c.search!==false&&!!input,options:input?.options?Array.from(input.options).filter(o=>!o.disabled).map(o=>({value:o.value,label:clean(o.textContent)})):[]}})}`

func collect(p *rod.Page) ([]model.Field, error) {
	var result []model.Field
	err := read(p.Timeout(45*time.Second), fieldsJS, &result)
	return result, err
}
func (e *Engine) Refresh(ctx context.Context, p model.Profile) (model.Catalog, error) {
	var errs []string
	for _, d := range p.Departments {
		var base model.Base
		for _, b := range p.Bases {
			if b.ID == d.BaseID {
				base = b
			}
		}
		_, err := e.getSession(ctx, model.Job{Base: base, Department: d})
		if err != nil {
			errs = append(errs, d.Name+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return e.Catalog(), errors.New(strings.Join(errs, "\n"))
	}
	return e.Catalog(), nil
}

func (e *Engine) Begin(ctx context.Context, j model.Job) (*queue.Pending, error) {
	s, err := e.getSession(ctx, j)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.browser.Context(ctx).Page(proto.TargetCreateTarget{URL: strings.TrimRight(j.Base.URL, "/") + "/ovzid/status/all"})
	if err != nil {
		return nil, err
	}
	eventCtx, cancel := context.WithCancel(ctx)
	events := make(chan queue.Event, 32)
	var once sync.Once
	closePage := func() { once.Do(func() { cancel(); p.Context(context.Background()).Timeout(5 * time.Second).Close() }) }
	fail := func(err error) (*queue.Pending, error) { closePage(); return nil, err }
	if err = waitDOM(p); err != nil {
		return fail(err)
	}
	e.authMu.Lock()
	err = e.ensureLogin(ctx, p, j.Base)
	e.authMu.Unlock()
	if err != nil {
		return fail(err)
	}
	var actualDepartment string
	err = read(p, `() => document.querySelector('ul.dropdown-menu-sub a[data-depid].active,ul.dropdown-menu-sub li.active a[data-depid]')?.getAttribute('data-depid')||document.querySelector('button.btn-cities')?.getAttribute('data-depid')||''`, &actualDepartment)
	if err != nil {
		return fail(err)
	}
	if actualDepartment != fmt.Sprint(j.Department.ID) {
		var href string
		if err = read(p, `id => document.querySelector('a[data-depid="'+id+'"]')?.href||''`, &href, j.Department.ID); err != nil || href == "" {
			return fail(fmt.Errorf("Не удалось выбрать департамент %s", j.Department.Name))
		}
		if err = p.Navigate(href); err != nil {
			return fail(err)
		}
		if err = waitDOM(p); err != nil {
			return fail(err)
		}
		if err = read(p, `() => document.querySelector('ul.dropdown-menu-sub a[data-depid].active,ul.dropdown-menu-sub li.active a[data-depid]')?.getAttribute('data-depid')||document.querySelector('button.btn-cities')?.getAttribute('data-depid')||''`, &actualDepartment); err != nil || actualDepartment != fmt.Sprint(j.Department.ID) {
			return fail(fmt.Errorf("Не подтверждён департамент %s", j.Department.Name))
		}
	}
	if err = waitGrid(p, false); err != nil {
		return fail(fmt.Errorf("Таблица не загрузилась: %w", err))
	}
	fields, err := collect(p)
	if err != nil {
		return fail(err)
	}
	e.rememberCatalog(j.Base.ID, j.Department.ID, fields)
	filters := []map[string]string{}
	for _, c := range j.Conditions {
		var field *model.Field
		for i := range fields {
			if fields[i].Key == c.Key && fields[i].Searchable {
				field = &fields[i]
				break
			}
		}
		if field == nil {
			return fail(fmt.Errorf("Поле %s недоступно", c.Key))
		}
		value := c.Value
		if field.Type == "select" {
			matches := []model.Option{}
			for _, o := range field.Options {
				if o.Label == c.Value {
					matches = append(matches, o)
				}
			}
			if len(matches) == 0 {
				for _, o := range field.Options {
					if o.Value == c.Value {
						matches = append(matches, o)
					}
				}
			}
			if len(matches) != 1 {
				return fail(fmt.Errorf("Значение «%s» поля «%s» отсутствует или неоднозначно", c.Value, field.Label))
			}
			value = matches[0].Value
			if matches[0].Label == "Все" {
				return fail(fmt.Errorf("Значение «Все» не задаёт ограничение поля «%s»", field.Label))
			}
		}
		filters = append(filters, map[string]string{"key": field.Key, "field": field.FilterField, "value": value})
	}
	_, err = p.Eval(`conditions => {const t=document.getElementById('list');window.__fsspFilter={done:false,status:0};const endpoint=new URL(t.p.url,location.href).pathname;jQuery(document).off('ajaxComplete.fsspExport').on('ajaxComplete.fsspExport',(event,xhr,opts)=>{if(new URL(opts.url,location.href).pathname===endpoint)window.__fsspFilter={done:true,status:xhr.status}});const inputs=Array.from(document.querySelectorAll('.ui-search-toolbar input,.ui-search-toolbar select'));for(const input of inputs)jQuery(input).val('').trigger('change.select2');for(const c of conditions){const input=inputs.find(el=>el.id==='gs_list_'+c.key);if(!input)throw new Error('Не найдено поле '+c.key);jQuery(input).val(c.value).trigger('change.select2');if(String(input.value)!==c.value)throw new Error('Не установлено значение '+c.key)}t.triggerToolbar();}`, filters)
	if err != nil {
		return fail(err)
	}
	if err = waitGrid(p, true); err != nil {
		return fail(err)
	}
	var records int
	err = read(p, `conditions => {const t=document.getElementById('list');const f=JSON.parse(t.p.postData.filters||'{}');function flatten(g){if(g.groupOp&&g.groupOp!=='AND')throw new Error('Условия должны быть связаны через И');return [...(g.rules||[]),...(g.groups||[]).flatMap(flatten)]}const rules=flatten(f);if(rules.length!==conditions.length)throw new Error('Количество применённых фильтров отличается');for(const c of conditions){const rs=rules.filter(r=>r.field===c.field);if(rs.length!==1||String(rs[0].data)!==c.value)throw new Error('Пирамида не применила фильтр '+c.key)}return Number(t.p.records||0)}`, &records, filters)
	if err != nil {
		return fail(err)
	}
	pending := &queue.Pending{Events: events, Close: closePage}
	send := func(ev queue.Event) {
		select {
		case events <- ev:
		case <-eventCtx.Done():
		}
	}
	if records == 0 {
		if err := queue.ClearEmptyResult(filepath.Join(e.output, j.Filename), j.ReplaceExisting); err != nil {
			return fail(err)
		}
		events <- queue.Event{State: queue.Empty}
		return pending, nil
	}
	// EachEvent устанавливает подписку синхронно, до отправки запроса выгрузки.
	guid := ""
	wait := s.browser.Context(eventCtx).EachEvent(func(ev *proto.BrowserDownloadWillBegin) {
		if ev.FrameID == p.FrameID {
			guid = ev.GUID
			send(queue.Event{State: queue.Downloading})
		}
	}, func(ev *proto.BrowserDownloadProgress) bool {
		if guid == "" || ev.GUID != guid {
			return false
		}
		if ev.State == proto.BrowserDownloadProgressStateCompleted {
			source := filepath.Join(s.temp, guid)
			target := filepath.Join(e.output, j.Filename)
			if eventCtx.Err() != nil {
				return true
			}
			if err := queue.SaveResult(source, target, j.ReplaceExisting); err != nil {
				send(queue.Event{State: queue.Failed, Detail: err.Error()})
			} else {
				os.Remove(source)
				send(queue.Event{State: queue.Saved, Path: target, Bytes: int64(ev.ReceivedBytes), Total: int64(ev.TotalBytes)})
			}
			return true
		}
		if ev.State == proto.BrowserDownloadProgressStateCanceled {
			send(queue.Event{State: queue.Failed, Detail: "Браузер отменил скачивание"})
			return true
		}
		send(queue.Event{State: queue.Downloading, Bytes: int64(ev.ReceivedBytes), Total: int64(ev.TotalBytes)})
		return false
	}, func(ev *proto.TargetTargetDestroyed) {
		if ev.TargetID == p.TargetID && eventCtx.Err() == nil {
			send(queue.Event{State: queue.Failed, Detail: "Вкладка Пирамиды неожиданно закрыта"})
		}
	})
	go func() {
		wait()
		if eventCtx.Err() == nil {
			send(queue.Event{State: queue.Failed, Detail: "Соединение с браузером завершилось"})
		}
	}()
	dialogWait := p.Context(eventCtx).EachEvent(func(ev *proto.PageJavascriptDialogOpening) {
		accept := strings.Contains(ev.Message, "Выгрузить все")
		(proto.PageHandleJavaScriptDialog{Accept: accept}).Call(p)
		if !accept {
			send(queue.Event{State: queue.Failed, Detail: "Сообщение Пирамиды: " + ev.Message})
		}
	})
	go dialogWait()
	exportRequests := map[proto.NetworkRequestID]bool{}
	responseWait := p.Context(eventCtx).EachEvent(func(ev *proto.NetworkRequestWillBeSent) {
		if strings.Contains(ev.Request.URL, "/ovzid/status/all/excel") {
			exportRequests[ev.RequestID] = true
		}
	}, func(ev *proto.NetworkLoadingFailed) {
		if exportRequests[ev.RequestID] && eventCtx.Err() == nil && ev.ErrorText != "net::ERR_ABORTED" {
			send(queue.Event{State: queue.Failed, Detail: "Сетевая ошибка выгрузки: " + ev.ErrorText})
		}
	}, func(ev *proto.NetworkResponseReceived) {
		if strings.Contains(ev.Response.URL, "/ovzid/status/all/excel") && ev.Response.Status >= 400 {
			send(queue.Event{State: queue.Failed, Detail: fmt.Sprintf("Сервер выгрузки вернул HTTP %d", ev.Response.Status)})
		}
		if strings.Contains(ev.Response.URL, "/ovzid/status/all/excel") && ev.Response.Status >= 200 && ev.Response.Status < 300 && strings.Contains(ev.Response.MIMEType, "text/html") {
			send(queue.Event{State: queue.Failed, Detail: "Сервер вернул страницу вместо Excel. Проверьте авторизацию или повторите попытку позднее"})
		}
	}, func(ev *proto.PageFrameNavigated) {
		if ev.Frame.ParentID == "" && LoginURL(ev.Frame.URL) {
			send(queue.Event{State: queue.Failed, RetryAuth: true, Detail: "Требуется восстановить вход в Пирамиду"})
		}
	})
	go responseWait()
	send(queue.Event{State: queue.Generating})
	_, err = p.Eval(`() => {const text=Array.from(document.querySelectorAll('.ui-pg-button-text')).find(e=>e.textContent.trim()==='Выгрузить в Excel');const button=text?.closest('[role="button"],.ui-pg-button')||document.querySelector('td#list_pager_left div[title="Выгрузить в Excel"]')||document.querySelector('div[title="Выгрузить в Excel"]');if(!button)throw new Error('Кнопка выгрузки отсутствует');setTimeout(()=>button.click(),0)}`)
	if err != nil {
		return fail(err)
	}
	return pending, nil
}
