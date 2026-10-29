package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/queue"
)

func TestSessionStartsInDepartmentAndReusesPreparedPage(t *testing.T) {
	if os.Getenv("FSSP_BROWSER_TESTS") != "1" {
		t.Skip("set FSSP_BROWSER_TESTS=1 for browser integration")
	}
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "favicon.ico") {
			w.WriteHeader(404)
			return
		}
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/login/department/") {
			parts := strings.Split(r.URL.Path, "/")
			http.SetCookie(w, &http.Cookie{Name: "department", Value: parts[3], Path: "/"})
			http.Redirect(w, r, "/ovzid/status/all", http.StatusFound)
			return
		}
		if r.URL.Path != "/ovzid/status/all" {
			http.NotFound(w, r)
			return
		}
		cookie, err := r.Cookie("department")
		if err != nil {
			w.WriteHeader(500)
			fmt.Fprint(w, "error: department not selected")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<button class="btn-cities" data-depid="%s">Департамент</button><ul class="dropdown-menu-sub"><li><a data-depid="39" class="%s" href="/login/department/39/ovzid/status/all">Первый</a></li><li><a data-depid="43" class="%s" href="/login/department/43/ovzid/status/all">Второй</a></li></ul><table id="list"></table><script>const t=document.getElementById('list');t.p={records:1,url:'/grid',postData:{filters:'{"groupOp":"AND","rules":[]}'},colModel:[{name:'Name',search:false}],colNames:['Поле']};t.grid={hDiv:{loading:false}};window.jQuery=()=>({off(){return this},on(){return this},val(){return this},trigger(){return this}});t.triggerToolbar=()=>{window.__fsspFilter={done:true,status:200};t.p.records=0};</script>`, cookie.Value, map[bool]string{true: "active", false: ""}[cookie.Value == "39"], map[bool]string{true: "active", false: ""}[cookie.Value == "43"])
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	e, err := New(ctx, t.TempDir(), "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	base := model.Base{ID: "base", URL: server.URL}
	j := model.Job{Base: base, Department: model.Department{BaseID: "base", ID: 39, Name: "Первый"}}
	s, err := e.getSession(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if s.prepared == nil {
		t.Fatal("подготовленная вкладка закрыта")
	}
	id := s.prepared.TargetID
	p := s.prepared
	pending, err := e.Begin(ctx, j)
	if err != nil {
		t.Fatal("первое задание не использовало готовую форму", err)
	}
	defer pending.Close()
	select {
	case event := <-pending.Events:
		if event.State != queue.Empty {
			t.Fatal(event)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = p.Info(); err != nil || p.TargetID != id {
		t.Fatal("подготовленная вкладка закрыта или заменена", err)
	}
	if s.prepared != nil {
		t.Fatal("вкладка может быть передана повторно")
	}
	mu.Lock()
	firstRequests := append([]string(nil), requests...)
	mu.Unlock()
	if len(firstRequests) != 2 || firstRequests[0] != "/login/department/39/ovzid/status/all" || firstRequests[1] != "/ovzid/status/all" {
		t.Fatal("лишний или неверный переход", firstRequests)
	}
	j.Department.ID = 43
	j.Department.Name = "Второй"
	s2, err := e.getSession(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if s.browser.BrowserContextID == s2.browser.BrowserContextID {
		t.Fatal("контексты департаментов смешаны")
	}
	var actual string
	if err = read(p, `() => document.querySelector('button.btn-cities').getAttribute('data-depid')`, &actual); err != nil || actual != "39" {
		t.Fatal("сменился первый департамент", actual, err)
	}
	pending.Close()
	s.mu.Lock()
	next, reused, err := s.takePage(ctx, base)
	s.mu.Unlock()
	if err != nil || reused || next.TargetID == id {
		t.Fatal("следующему заданию не создана собственная вкладка", err)
	}
	defer next.Close()
	// Ошибку страницы по-прежнему нельзя принять за готовность и скрыть перезапуском.
	if err = waitDOM(next); err != nil {
		t.Fatal(err)
	}
	if _, err = next.Eval(`() => {document.body.insertAdjacentHTML('beforeend','<div class="alert-danger">Ошибка сервера</div>')}`); err != nil {
		t.Fatal(err)
	}
	if waitGrid(next, false) == nil {
		t.Fatal("проигнорирована ошибка таблицы")
	}
}
