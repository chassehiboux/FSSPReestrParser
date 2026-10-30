package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chassehiboux/FSSPReestrParser/internal/model"
	"github.com/chassehiboux/FSSPReestrParser/internal/platform"
	"github.com/chassehiboux/FSSPReestrParser/internal/queue"
	"github.com/go-rod/rod/lib/proto"
)

func TestCredentialClassification(t *testing.T) {
	if !CredentialRejected("Неверное имя пользователя или пароль.") || CredentialRejected("Сервис временно недоступен") {
		t.Fatal("Отказ входа спутан с недоступностью сервиса")
	}
	if LoginURL("https://kgn.pyramid.vostok-electra.ru/login/department/39/ovzid/status/all?token=example") {
		t.Fatal("Выбор департамента ошибочно признан истечением сессии")
	}
	if !LoginURL("https://kgn.pyramid.vostok-electra.ru/login/pyrsso") {
		t.Fatal("Не распознан переход на вход")
	}
}

func TestSlowCredentialEntryAndRepeatedRejectionWithoutNavigation(t *testing.T) {
	if os.Getenv("FSSP_BROWSER_TESTS") != "1" {
		t.Skip("set FSSP_BROWSER_TESTS=1 for browser integration")
	}
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == "POST" {
			posts.Add(1)
			r.ParseForm()
			fmt.Fprint(w, r.FormValue("password") == "correct")
			return
		}
		fmt.Fprint(w, `<form><div class="validation-summary-errors"></div><input id="Input_UserName"><input id="Input_Password"><button id="login-submit" type="submit">Войти</button></form><script>document.querySelector('form').onsubmit=async ev=>{ev.preventDefault();const body=new URLSearchParams({password:document.querySelector('#Input_Password').value});const result=await (await fetch('/',{method:'POST',body})).text();if(result==='true')document.body.innerHTML='<button class="btn-cities">Департамент</button>';else document.querySelector('.validation-summary-errors').textContent='Неверное имя пользователя или пароль.';};</script>`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	prompts := 0
	e, err := New(ctx, t.TempDir(), "", true, func(ctx context.Context, reason, user string) (Credentials, error) {
		prompts++
		if !CredentialRejected(reason) {
			t.Errorf("Не показана причина отказа: %s", reason)
		}
		// Каждый ввод дольше всего бюджета ответа сервера: пользователь может ждать.
		if err := pause(ctx, 2300*time.Millisecond); err != nil {
			return Credentials{}, err
		}
		password := "wrong-again"
		if prompts == 2 {
			password = "correct"
		}
		return Credentials{Username: "test", Password: password}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.credentials = Credentials{Username: "test", Password: "wrong"}
	p, err := e.browser.Page(proto.TargetCreateTarget{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.WaitLoad()
	if err = e.ensureLoginWithin(ctx, p, model.Base{}, 2*time.Second); err != nil {
		t.Fatal("Повторный ввод не продолжил вход", err)
	}
	if prompts != 2 || posts.Load() != 3 {
		t.Fatalf("prompts=%d posts=%d", prompts, posts.Load())
	}
	// Сервис снова вернул форму входа: сохранённые в памяти данные восстанавливают вход.
	if err = p.Navigate(server.URL); err != nil {
		t.Fatal(err)
	}
	p.WaitLoad()
	if err = waitGrid(p, false); !errors.Is(err, queue.ErrAuthRequired) {
		t.Fatal("Возврат на вход не распознан", err)
	}
	if err = e.ensureLoginWithin(ctx, p, model.Base{}, 2*time.Second); err != nil || prompts != 2 || posts.Load() != 4 {
		t.Fatal("Восстановление входа", err, prompts, posts.Load())
	}
}

// Настоящий Chrome и локальный HTTP-сервер: неверные пароли не отправляются в корпоративный сервис.
func TestAuthRetryAndCancelInBrowser(t *testing.T) {
	if os.Getenv("FSSP_BROWSER_TESTS") != "1" {
		t.Skip("set FSSP_BROWSER_TESTS=1 for browser integration")
	}
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == "POST" {
			posts.Add(1)
			r.ParseForm()
			if r.FormValue("password") == "correct" {
				fmt.Fprint(w, `<button class="btn-cities">Департамент</button>`)
				return
			}
		}
		message := ""
		if r.Method == "POST" {
			message = "Неверное имя пользователя или пароль."
		}
		fmt.Fprintf(w, `<form method="post"><div class="validation-summary-errors">%s</div><input id="Input_UserName" name="username"><input id="Input_Password" name="password" type="password"><button id="login-submit" type="submit">Войти</button></form>`, message)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	local := t.TempDir()
	prompts := 0
	e, err := New(ctx, local, "", true, func(_ context.Context, reason, user string) (Credentials, error) {
		prompts++
		if !CredentialRejected(reason) {
			t.Errorf("Missing rejection message: %s", reason)
		}
		return Credentials{Username: "test", Password: "correct", Remember: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.credentials = Credentials{Username: "test", Password: "wrong"}
	p, err := e.browser.Page(proto.TargetCreateTarget{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.WaitLoad()
	if err = e.ensureLogin(ctx, p, model.Base{}); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 || posts.Load() != 2 {
		t.Fatalf("prompts=%d posts=%d", prompts, posts.Load())
	}
	var saved Credentials
	if err = platform.ReadProtected(filepath.Join(local, "credentials.dat"), &saved); err != nil || saved.Password != "correct" {
		t.Fatalf("Protected credentials: %v", err)
	}
	e.credentials = Credentials{}
	e.prompt = func(context.Context, string, string) (Credentials, error) {
		return Credentials{}, errors.New("Отмена пользователем")
	}
	p.Navigate(server.URL)
	p.WaitLoad()
	if err = e.ensureLogin(ctx, p, model.Base{}); err == nil || err.Error() != "Отмена пользователем" {
		t.Fatalf("Cancel: %v", err)
	}
	if posts.Load() != 2 {
		t.Fatal("Cancel submitted credentials")
	}
	_, err = p.Eval(`() => {document.body.innerHTML='<div class="alert-danger">Неизвестная ошибка! Сервер вернул код: 0</div><table id="list"></table>';const t=document.getElementById('list');t.p={records:0};t.grid={hDiv:{loading:false}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if err = waitGrid(p, false); err == nil {
		t.Fatal("Ошибка страницы принята за пустую таблицу")
	}
}
