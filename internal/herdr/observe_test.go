package herdr

import (
	"reflect"
	"testing"
)

// A size Herdr did not report is worse than the default one: an unsized
// stream at least matches whatever Herdr laid the pane out as.
func TestObserveArgs(t *testing.T) {
	t.Parallel()
	got := observeArgs("w1:p1", 204, 57)
	want := []string{"terminal", "session", "observe", "w1:p1", "--cols", "204", "--rows", "57"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("observeArgs() = %+v, want %+v", got, want)
	}
	for _, size := range [][2]int{{0, 57}, {204, 0}, {-1, -1}} {
		got := observeArgs("w1:p1", size[0], size[1])
		if want := []string{"terminal", "session", "observe", "w1:p1"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("observeArgs(%v) = %+v, want no size", size, got)
		}
	}
}
