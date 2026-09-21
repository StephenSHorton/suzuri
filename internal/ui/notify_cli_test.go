//go:build windows || darwin

package ui

import "testing"

func TestNotifySoundArgs(t *testing.T) {
	all, err := notifySoundArgs(nil)
	if err != nil || len(all) != 3 || all[0] != "success" || all[2] != "published" {
		t.Fatalf("%v %v", all, err)
	}
	one, err := notifySoundArgs([]string{"error"})
	if err != nil || len(one) != 1 || one[0] != "fail" {
		t.Fatalf("%v %v", one, err)
	}
	if _, err := notifySoundArgs([]string{"nope"}); err == nil {
		t.Fatal("expected error")
	}
	if len(wavForNotifyName("success")) < 44 || len(wavForNotifyName("fail")) < 44 {
		t.Fatal("embedded wav missing")
	}
}
