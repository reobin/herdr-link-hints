package herdr

import (
	"context"
	"reflect"
	"testing"
	"time"
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

// A nudged stream has already cleared the accept poll, so a pane with
// nothing to show must not hold the picker for the unnudged wait.
func TestCollectFramesShortensTheWaitOnceNudged(t *testing.T) {
	t.Parallel()
	woke := make(chan struct{}, 1)
	woke <- struct{}{}

	start := time.Now()
	got := collectFrames(context.Background(), make(chan []byte), make(chan struct{}), woke)

	if len(got) != 0 {
		t.Fatalf("collectFrames() = %q, want nothing", got)
	}
	if took := time.Since(start); took < nudgedFirstFrameWait || took >= firstFrameWait {
		t.Fatalf("waited %v, want about %v", took, nudgedFirstFrameWait)
	}
}

// Without a nudge the accept poll is still in play, so cutting the wait
// short would discard a frame already on its way.
func TestCollectFramesKeepsTheFullWaitWithoutANudge(t *testing.T) {
	t.Parallel()
	frames := make(chan []byte)
	go func() {
		time.Sleep(nudgedFirstFrameWait * 2)
		frames <- []byte("late")
		close(frames)
	}()

	got := collectFrames(context.Background(), frames, make(chan struct{}), make(chan struct{}))

	if string(got) != "late" {
		t.Fatalf("collectFrames() = %q, want the late frame", got)
	}
}

// Every frame holds the window open, or a repaint is cut mid-sequence.
func TestCollectFramesGathersABurst(t *testing.T) {
	t.Parallel()
	frames := make(chan []byte)
	go func() {
		for _, part := range []string{"one", "two", "three"} {
			frames <- []byte(part)
			time.Sleep(quietAfterFrame / 2)
		}
		close(frames)
	}()

	landed := make(chan struct{})
	got := collectFrames(context.Background(), frames, landed, make(chan struct{}))

	if string(got) != "onetwothree" {
		t.Fatalf("collectFrames() = %q, want the whole burst", got)
	}
	select {
	case <-landed:
	default:
		t.Fatal("landed should be closed once a frame arrives, to stop the nudges")
	}
}
