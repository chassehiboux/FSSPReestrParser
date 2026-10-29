package platform

import (
	"golang.org/x/sys/windows"
	"testing"
)

func TestAdminIsBoundToWindowsSID(t *testing.T) {
	old := AdminSID
	defer func() { AdminSID = old }()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	AdminSID = user.User.Sid.String()
	if !IsAdmin() {
		t.Fatal("Current SID not recognized")
	}
	AdminSID = "S-1-0-0"
	if IsAdmin() {
		t.Fatal("Another SID gained administration")
	}
}
