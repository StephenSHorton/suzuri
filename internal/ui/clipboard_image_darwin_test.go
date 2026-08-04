//go:build darwin

package ui

import "testing"

func TestApplescriptEscape(t *testing.T) {
	if got := applescriptEscape(`a"b\c`); got != `a\"b\\c` {
		t.Fatalf("got %q", got)
	}
}
